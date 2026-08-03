package confluence

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// Confluence supports resumable streaming sync; the service prefers FetchStream
// over FetchAll/FetchIncremental when a connector implements StreamingConnector.
var _ datasource.StreamingConnector = (*Connector)(nil)

// confluenceStreamCheckpointInterval is how many processed pages pass between
// durable checkpoints. PDF export is slow (~seconds each), so a small interval
// ensures progress is not lost on timeout.
var confluenceStreamCheckpointInterval = 20

// confluenceStreamCheckpointMaxInterval bounds checkpointing by wall-clock time.
var confluenceStreamCheckpointMaxInterval = 30 * time.Second

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
// It runs the same traversal as FetchStream: a nil cursor skips nothing, so a
// full sync is just a stream that starts without prior state.
func (c *Connector) FetchAll(
	ctx context.Context, config *types.DataSourceConfig, resourceIDs []string,
) ([]types.FetchedItem, error) {
	scoped := *config
	scoped.ResourceIDs = resourceIDs

	collector := &itemCollector{}
	if _, err := c.FetchStream(ctx, &scoped, nil, collector); err != nil {
		return nil, err
	}
	return collector.items, nil
}

// FetchIncremental returns items changed (or deleted) since the prior cursor.
// Deletion detection: pages present in the prior cursor but absent from a
// complete current listing are emitted as IsDeleted=true placeholder items.
func (c *Connector) FetchIncremental(
	ctx context.Context, config *types.DataSourceConfig, cursor *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	collector := &itemCollector{}
	next, err := c.FetchStream(ctx, config, cursor, collector)
	if err != nil {
		return nil, nil, err
	}
	return collector.items, next, nil
}

// itemCollector adapts a streaming fetch back to the batch FetchAll /
// FetchIncremental signatures by buffering every emitted item. Both APIs then
// share a single traversal, so cursor advancement and deletion detection cannot
// drift between the streaming and non-streaming paths.
type itemCollector struct {
	items []types.FetchedItem
}

func (c *itemCollector) Emit(_ context.Context, item types.FetchedItem) error {
	c.items = append(c.items, item)
	return nil
}

// Checkpoint is a no-op: batch callers only persist the cursor FetchStream
// returns at the end.
func (c *itemCollector) Checkpoint(context.Context, *types.SyncCursor) error { return nil }

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

// FetchStream performs a resumable, memory-bounded sync. It unifies the full
// and incremental paths: with cursor == nil it fetches everything, and with a
// cursor it skips pages whose recorded version time is unchanged. Instead of
// accumulating every item in memory (FetchAll), it Emits each item as it is
// fetched and Checkpoints the cursor periodically, so progress is durable
// across the Asynq task's timeout.
func (c *Connector) FetchStream(
	ctx context.Context, config *types.DataSourceConfig,
	cursor *types.SyncCursor, h datasource.StreamHandler,
) (*types.SyncCursor, error) {
	resourceIDs := config.ResourceIDs
	if len(resourceIDs) == 0 {
		return nil, fmt.Errorf("no resource IDs (space IDs) configured")
	}

	cfg, err := parseConfluenceConfig(config)
	if err != nil {
		return nil, err
	}

	client, err := NewClient(cfg)
	if err != nil {
		return nil, err
	}

	// Decode prior cursor (if any).
	var prev *confluenceCursor
	if cursor != nil && cursor.ConnectorCursor != nil {
		prev = unmarshalCursor(cursor.ConnectorCursor)
	}

	newCursor := &confluenceCursor{
		LastSyncTime:   time.Now(),
		SpacePageTimes: make(map[string]map[string]string),
	}

	// Fetch space list once and build a lookup map.
	spaces, err := client.ListSpaces(ctx)
	if err != nil {
		return nil, fmt.Errorf("list spaces: %w", err)
	}
	spaceMap := make(map[string]*confluenceSpace, len(spaces))
	for i := range spaces {
		spaceMap[spaces[i].ID] = &spaces[i]
	}

	processed := 0
	lastCheckpoint := time.Now()

	for _, resourceID := range resourceIDs {
		prevTimes := prev.pageTimes(resourceID)

		prefix, id := parseResourceID(resourceID)
		if prefix != "s" {
			logger.Warnf(ctx, "[Confluence] skipping unsupported resource type %q", prefix)
			continue
		}

		space, ok := spaceMap[id]
		if !ok {
			// The space may be temporarily invisible (permission change, API
			// hiccup) rather than deleted, so keep its recorded state instead of
			// dropping it — see retain.
			logger.Warnf(ctx, "[Confluence] space %s not found, skipping (retaining %d cursor entries)",
				id, len(prevTimes))
			newCursor.retain(resourceID, prevTimes)
			continue
		}

		pages, lerr := c.listSpacePages(ctx, client, cfg, space)
		if lerr != nil {
			logger.Warnf(ctx, "[Confluence] failed to list pages in space %s: %v", space.Key, lerr)
			newCursor.retain(resourceID, prevTimes)
			continue
		}

		// A space that comes back empty but was synced before is treated as a
		// failed listing rather than as "every page was deleted": Confluence
		// answers 200 with no results while its search index rebuilds or when
		// page-level permissions change, and mirror sync would turn those
		// phantom deletions into real knowledge-base deletions. Reconciling a
		// genuinely emptied space needs an explicit full sync.
		if len(pages) == 0 && len(prevTimes) > 0 {
			logger.Warnf(ctx,
				"[Confluence] space %s returned 0 pages but %d were synced before; "+
					"treating as a listing failure and skipping deletion detection",
				space.Key, len(prevTimes))
			newCursor.retain(resourceID, prevTimes)
			continue
		}

		pageTimes := make(map[string]string, len(pages))
		newCursor.SpacePageTimes[resourceID] = pageTimes
		currentPages := make(map[string]bool, len(pages))

		for i, page := range pages {
			currentPages[page.ID] = true
			prevWhen, hadPrev := prevTimes[page.ID]

			// Resume/incremental fast-path: a page recorded at its current
			// version was already exported, so keep the record and skip it.
			if hadPrev && prevWhen == page.Version.When {
				pageTimes[page.ID] = page.Version.When
				continue
			}

			// Page is new or changed — export as PDF and emit immediately.
			item, ferr := c.fetchPageAsPDF(ctx, client, cfg, page, resourceID)
			if ferr != nil {
				// Do NOT record the current version: the PDF was never
				// exported. Retaining the prior version keeps prev != current
				// on the next run so the page is retried, instead of being
				// skipped forever after one transient export failure. The
				// service relies on this to converge (Tencent/WeKnora#2136).
				if hadPrev {
					pageTimes[page.ID] = prevWhen
				}
				if eerr := h.Emit(ctx, pageErrorItem(page, resourceID, ferr)); eerr != nil {
					return nil, eerr
				}
			} else {
				pageTimes[page.ID] = page.Version.When
				if item != nil {
					if eerr := h.Emit(ctx, *item); eerr != nil {
						return nil, eerr
					}
				}
			}

			processed++
			if processed%confluenceStreamCheckpointInterval == 0 ||
				time.Since(lastCheckpoint) >= confluenceStreamCheckpointMaxInterval {
				if cerr := h.Checkpoint(ctx, newCursor.toSyncCursor()); cerr != nil {
					logger.Warnf(ctx, "[Confluence] stream checkpoint failed: %v", cerr)
				}
				lastCheckpoint = time.Now()
			}
			if n := i + 1; n%100 == 0 {
				logger.Infof(ctx, "[Confluence] stream progress space=%s %d/%d",
					space.Key, n, len(pages))
			}
		}

		logger.Infof(ctx, "[Confluence] space %s (key=%s): total pages=%d",
			space.Name, space.Key, len(pages))

		// Deletion detection: pages recorded last run but absent from this
		// (complete) listing.
		for prevPageID := range prevTimes {
			if currentPages[prevPageID] {
				continue
			}
			if eerr := h.Emit(ctx, types.FetchedItem{
				ExternalID:       prevPageID,
				IsDeleted:        true,
				SourceResourceID: resourceID,
				Metadata: map[string]string{
					"channel": types.ChannelConfluence,
				},
			}); eerr != nil {
				return nil, eerr
			}
		}
	}

	return newCursor.toSyncCursor(), nil
}

// listSpacePages lists every current page in a space using the API the
// configured edition supports.
func (c *Connector) listSpacePages(
	ctx context.Context, client *Client, cfg *Config, space *confluenceSpace,
) ([]confluencePage, error) {
	if cfg.IsCloud() {
		// Cloud: v1 CQL search is unreliable there, so use the v2 page listing.
		return client.GetAllPagesInSpaceV2(ctx, space.ID, space.Key, space.Name)
	}
	return client.GetAllPagesInSpace(ctx, space.Key)
}

// pageErrorItem builds the placeholder emitted for a page whose export failed,
// so the sync log reports the failure per page instead of losing it silently.
func pageErrorItem(page confluencePage, resourceID string, err error) types.FetchedItem {
	return types.FetchedItem{
		ExternalID:       page.ID,
		Title:            page.Title,
		SourceResourceID: resourceID,
		Metadata: map[string]string{
			"error":   err.Error(),
			"channel": types.ChannelConfluence,
		},
	}
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
