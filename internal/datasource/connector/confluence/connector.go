package confluence

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// Connector implements the datasource.Connector interface for Confluence 7.x.
type Connector struct{}

// NewConnector creates a new Confluence connector.
func NewConnector() *Connector {
	return &Connector{}
}

// Type returns the connector type identifier.
func (c *Connector) Type() string {
	return types.ConnectorTypeConfluence
}

// Validate verifies that the Confluence configuration is valid by testing connectivity.
func (c *Connector) Validate(ctx context.Context, config *types.DataSourceConfig) error {
	cfg, err := parseConfluenceConfig(config)
	if err != nil {
		return err
	}

	client, err := NewClient(cfg)
	if err != nil {
		return fmt.Errorf("create confluence client: %w", err)
	}

	if err := client.Ping(ctx); err != nil {
		return fmt.Errorf("confluence connection failed: %w", err)
	}

	return nil
}

// ListResources lists Confluence resources for selection in the UI.
// Only spaces are returned — page-level expansion is not needed
// because we sync entire spaces at once.
func (c *Connector) ListResources(
	ctx context.Context, config *types.DataSourceConfig, parentID string,
) ([]types.Resource, error) {
	// We only support top-level space listing.
	// Spaces have HasChildren=false so the UI will never call with a non-empty parentID,
	// but handle it gracefully just in case.
	if parentID != "" {
		return nil, nil
	}

	cfg, err := parseConfluenceConfig(config)
	if err != nil {
		return nil, err
	}

	client, err := NewClient(cfg)
	if err != nil {
		return nil, err
	}

	baseURL := strings.TrimRight(cfg.BaseURL, "/")

	spaces, err := client.ListSpaces(ctx)
	if err != nil {
		return nil, fmt.Errorf("list confluence spaces: %w", err)
	}

	resources := make([]types.Resource, 0, len(spaces))
	for _, space := range spaces {
		resources = append(resources, spaceToResource(space, baseURL))
	}
	return resources, nil
}

// ResolveResourceAncestors returns ancestor resource IDs for lazy-loaded tree expansion.
// Since we only select at the space level (no page expansion), spaces have no ancestors.
func (c *Connector) ResolveResourceAncestors(
	_ context.Context, _ *types.DataSourceConfig, _ []string,
) ([]string, error) {
	// Spaces are top-level resources — no ancestors needed.
	return nil, nil
}

// FetchAll performs a full sync of all pages in the specified spaces.
func (c *Connector) FetchAll(
	ctx context.Context, config *types.DataSourceConfig, resourceIDs []string,
) ([]types.FetchedItem, error) {
	items, _, err := c.walk(ctx, config, resourceIDs, nil, false)
	return items, err
}

// FetchIncremental returns items changed (or deleted) since the prior cursor.
// Deletion detection: pages present in the prior cursor but absent from the
// current listing are emitted as IsDeleted=true placeholder items.
func (c *Connector) FetchIncremental(
	ctx context.Context, config *types.DataSourceConfig, cursor *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	resourceIDs := config.ResourceIDs
	if len(resourceIDs) == 0 {
		return nil, nil, fmt.Errorf("no resource IDs (space IDs) configured")
	}

	// Decode prior cursor (if any).
	var prev *confluenceCursor
	if cursor != nil && cursor.ConnectorCursor != nil {
		prev = unmarshalCursor(cursor.ConnectorCursor)
	}

	items, newCursor, err := c.walk(ctx, config, resourceIDs, prev, true)
	if err != nil {
		return nil, nil, err
	}

	// Marshal newCursor into a generic map for the SyncCursor wrapper.
	cursorMap := make(map[string]interface{})
	b, _ := json.Marshal(newCursor)
	_ = json.Unmarshal(b, &cursorMap)

	return items, &types.SyncCursor{
		LastSyncTime:    newCursor.LastSyncTime,
		ConnectorCursor: cursorMap,
	}, nil
}

// walk is the shared implementation for FetchAll / FetchIncremental.
// Follows the same pattern as the Yuque connector's walk method:
//  1. For each resource (space), list all pages via CQL
//  2. Compare each page's version.when against the prior cursor
//  3. Export changed pages as PDF
//  4. Detect deletions (pages in prior cursor but absent now)
//
// If incremental is false, prev is ignored and no cursor is returned.
func (c *Connector) walk(
	ctx context.Context,
	config *types.DataSourceConfig,
	resourceIDs []string,
	prev *confluenceCursor,
	incremental bool,
) ([]types.FetchedItem, *confluenceCursor, error) {
	cfg, err := parseConfluenceConfig(config)
	if err != nil {
		return nil, nil, err
	}

	client, err := NewClient(cfg)
	if err != nil {
		return nil, nil, err
	}

	newCursor := &confluenceCursor{
		LastSyncTime:   time.Now(),
		SpacePageTimes: make(map[string]map[string]string),
	}

	var out []types.FetchedItem

	// Fetch space list once and build a lookup map to avoid repeated API calls.
	spaces, err := client.ListSpaces(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("list spaces: %w", err)
	}
	spaceMap := make(map[string]*confluenceSpace, len(spaces))
	for i := range spaces {
		spaceMap[spaces[i].ID] = &spaces[i]
	}

	for _, resourceID := range resourceIDs {
		prefix, id := parseResourceID(resourceID)
		if prefix != "s" {
			logger.Warnf(ctx, "[Confluence] skipping unsupported resource type %q (only space-level sync is supported)", prefix)
			continue
		}

		// Look up the space from the cached map
		space, ok := spaceMap[id]
		if !ok {
			logger.Warnf(ctx, "[Confluence] space %s not found, skipping (may have been deleted)", id)
			continue
		}

		// List all pages in this space
		var pages []confluencePage
		if cfg.IsCloud() {
			// Cloud: use v2 API to list pages (v1 CQL search may not work on Cloud)
			pages, err = client.GetAllPagesInSpaceV2(ctx, space.ID, space.Key, space.Name)
		} else {
			// Server / DC: use v1 CQL search
			pages, err = client.GetAllPagesInSpace(ctx, space.Key)
		}
		if err != nil {
			logger.Warnf(ctx, "[Confluence] failed to list pages in space %s: %v", space.Key, err)
			continue
		}

		currentPages := make(map[string]bool)
		newCursor.SpacePageTimes[resourceID] = make(map[string]string)

		for _, page := range pages {
			currentPages[page.ID] = true
			newCursor.SpacePageTimes[resourceID][page.ID] = page.Version.When

			// Incremental: skip if page hasn't changed since last sync
			if incremental && prev != nil && prev.SpacePageTimes != nil {
				if prevTimes, ok := prev.SpacePageTimes[resourceID]; ok {
					if prevTime, exists := prevTimes[page.ID]; exists {
						if prevTime == page.Version.When {
							// Page unchanged, skip
							continue
						}
					}
				}
			}

			// Page is new or changed — export as PDF
			item, err := c.fetchPageAsPDF(ctx, client, cfg, page, resourceID)
			if err != nil {
				out = append(out, types.FetchedItem{
					ExternalID:       page.ID,
					Title:            page.Title,
					SourceResourceID: resourceID,
					Metadata: map[string]string{
						"error":   err.Error(),
						"channel": types.ChannelConfluence,
					},
				})
				continue
			}
			if item != nil {
				out = append(out, *item)
			}
		}

		logger.Infof(ctx, "[Confluence] space %s (key=%s): total pages=%d",
			space.Name, space.Key, len(pages))

		// Deletion detection (incremental only): pages in prior cursor but absent now
		if incremental && prev != nil && prev.SpacePageTimes != nil {
			if prevTimes, ok := prev.SpacePageTimes[resourceID]; ok {
				for prevPageID := range prevTimes {
					if !currentPages[prevPageID] {
						out = append(out, types.FetchedItem{
							ExternalID:       prevPageID,
							IsDeleted:        true,
							SourceResourceID: resourceID,
							Metadata: map[string]string{
								"channel": types.ChannelConfluence,
							},
						})
					}
				}
			}
		}
	}

	if !incremental {
		return out, nil, nil
	}
	return out, newCursor, nil
}

// fetchPageAsPDF exports a single Confluence page.
// For Server edition: uses the native PDF export action.
// For Cloud edition: fetches body.export_view HTML, inlines images, and
// renders to PDF via headless Chrome (Cloud does not expose the legacy
// PDF export endpoint).
func (c *Connector) fetchPageAsPDF(
	ctx context.Context, client *Client, cfg *Config, page confluencePage, sourceResourceID string,
) (*types.FetchedItem, error) {
	var (
		data        []byte
		filename    string
		err         error
		contentType string
	)

	if cfg.IsCloud() {
		data, filename, err = client.ExportPageAsPDFViaExportView(ctx, page.ID, page.Title)
		contentType = "application/pdf"
	} else {
		data, filename, err = client.ExportPageAsPDF(ctx, page.ID, page.Title)
		contentType = "application/pdf"
	}
	if err != nil {
		return nil, err
	}

	modifiedAt := parseConfluenceTimestamp(page.Version.When)
	baseURL := strings.TrimRight(cfg.BaseURL, "/")

	return &types.FetchedItem{
		ExternalID:       page.ID,
		Title:            page.Title,
		Content:          data,
		ContentType:      contentType,
		FileName:         filename,
		URL:              baseURL + page.Links.WebUI,
		UpdatedAt:        modifiedAt,
		SourceResourceID: sourceResourceID,
		Metadata: map[string]string{
			"channel":    types.ChannelConfluence,
			"space_key":  page.Space.Key,
			"space_name": page.Space.Name,
			"page_id":    page.ID,
			"creator":    page.Version.By.DisplayName,
		},
	}, nil
}

// --- Resource ID helpers ---
// Resource ID format:
//   - Space: "s:{spaceID}" (e.g., "s:12345")
// Legacy format without prefix is also accepted and treated as a space ID.

func parseResourceID(resourceID string) (prefix string, id string) {
	if strings.HasPrefix(resourceID, "s:") {
		return "s", strings.TrimPrefix(resourceID, "s:")
	}
	// Legacy: no prefix, assume it's a space ID (numeric)
	return "s", resourceID
}
