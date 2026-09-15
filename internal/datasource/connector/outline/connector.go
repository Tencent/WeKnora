package outline

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// Compile-time proof that *Connector satisfies the datasource.Connector
// interface, so signature drift fails the build rather than container wiring.
var _ datasource.Connector = (*Connector)(nil)

// Connector implements datasource.Connector for Outline.
type Connector struct{}

// NewConnector creates a new Outline connector.
func NewConnector() *Connector { return &Connector{} }

// Type returns the connector type identifier.
func (c *Connector) Type() string { return types.ConnectorTypeOutline }

// Validate verifies the token by asking Outline which team it belongs to.
func (c *Connector) Validate(ctx context.Context, config *types.DataSourceConfig) error {
	cfg, err := parseOutlineConfig(config)
	if err != nil {
		return err
	}
	if err := newClient(cfg).Ping(ctx); err != nil {
		return fmt.Errorf("outline connection failed: %w", err)
	}
	return nil
}

// ResolveResourceAncestors has nothing to do for Outline: collections are a flat
// list, so a selection has no ancestors for the picker to reveal.
func (c *Connector) ResolveResourceAncestors(
	ctx context.Context, config *types.DataSourceConfig, resourceIDs []string,
) ([]string, error) {
	return []string{}, nil
}

// ListResources returns the collections the token can see.
//
// Outline nests documents inside a collection, but a collection is the unit
// users think in and the unit documents.list accepts, so the picker stays flat:
// selecting a collection syncs every document in it, nested ones included.
func (c *Connector) ListResources(
	ctx context.Context, config *types.DataSourceConfig, parentID string,
) ([]types.Resource, error) {
	// Flat list: a lazy-load request for one parent's children has nothing to add.
	if parentID != "" {
		return []types.Resource{}, nil
	}

	cfg, err := parseOutlineConfig(config)
	if err != nil {
		return nil, err
	}
	cols, err := newClient(cfg).ListCollections(ctx)
	if err != nil {
		return nil, fmt.Errorf("list collections: %w", err)
	}

	base := cfg.GetBaseURL()
	out := make([]types.Resource, 0, len(cols))
	for _, col := range cols {
		out = append(out, types.Resource{
			ExternalID:  col.ID,
			Name:        col.Name,
			Type:        "collection",
			Description: col.Description,
			URL:         absoluteURL(base, col.URL),
			ModifiedAt:  parseOutlineTime(col.UpdatedAt),
		})
	}
	// Stable, deterministic order for UI rendering and response-body caching.
	sort.Slice(out, func(i, j int) bool { return out[i].ExternalID < out[j].ExternalID })
	return out, nil
}

// FetchAll performs a full sync of every document in the given collections.
func (c *Connector) FetchAll(
	ctx context.Context, config *types.DataSourceConfig, resourceIDs []string,
) ([]types.FetchedItem, error) {
	items, _, err := c.walk(ctx, config, resourceIDs, nil, false)
	return items, err
}

// FetchIncremental returns documents changed (or deleted) since the prior cursor.
//
// Change detection compares Outline's per-document `revision`, which increments
// only on a content or title edit — a tighter signal than updatedAt, which also
// moves on activity that leaves the document unchanged.
//
// Deletion detection: a document listed in the prior cursor but absent from the
// current listing is emitted as an IsDeleted placeholder. Outline omits trashed
// and archived documents from documents.list, so this covers both.
func (c *Connector) FetchIncremental(
	ctx context.Context, config *types.DataSourceConfig, cursor *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	resourceIDs := config.ResourceIDs
	if len(resourceIDs) == 0 {
		return nil, nil, fmt.Errorf("no resource IDs (collection IDs) configured")
	}

	var prev *outlineCursor
	if cursor != nil && cursor.ConnectorCursor != nil {
		var p outlineCursor
		if b, err := json.Marshal(cursor.ConnectorCursor); err == nil {
			if err := json.Unmarshal(b, &p); err == nil {
				prev = &p
			}
		}
		if prev == nil {
			// An undecodable cursor means one full pass, which is correct but
			// expensive — say so rather than failing the sync silently.
			logger.Warnf(ctx, "[Outline] prior cursor could not be decoded; falling back to a full pass")
		}
	}

	items, newCursor, err := c.walk(ctx, config, resourceIDs, prev, true)
	if err != nil {
		return nil, nil, err
	}

	cursorMap := make(map[string]interface{})
	b, err := json.Marshal(newCursor)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal cursor: %w", err)
	}
	if err := json.Unmarshal(b, &cursorMap); err != nil {
		return nil, nil, fmt.Errorf("encode cursor: %w", err)
	}

	return items, &types.SyncCursor{
		LastSyncTime:    newCursor.LastSyncTime,
		ConnectorCursor: cursorMap,
	}, nil
}

// walk is the shared implementation behind FetchAll and FetchIncremental.
// When incremental is false, prev is ignored and the returned cursor is nil.
func (c *Connector) walk(
	ctx context.Context,
	config *types.DataSourceConfig,
	resourceIDs []string,
	prev *outlineCursor,
	incremental bool,
) ([]types.FetchedItem, *outlineCursor, error) {
	cfg, err := parseOutlineConfig(config)
	if err != nil {
		return nil, nil, err
	}
	cli := newClient(cfg)
	base := cfg.GetBaseURL()

	newCursor := &outlineCursor{
		LastSyncTime:           time.Now(),
		CollectionDocRevisions: make(map[string]map[string]int),
	}
	var out []types.FetchedItem

	for _, collectionID := range resourceIDs {
		docs, err := cli.ListCollectionDocuments(ctx, collectionID)
		if err != nil {
			return nil, nil, fmt.Errorf("list documents for collection %s: %w", collectionID, err)
		}

		current := make(map[string]bool, len(docs))
		newCursor.CollectionDocRevisions[collectionID] = make(map[string]int, len(docs))

		var skippedGone, skippedTemplate, skippedUnchanged, kept int
		for _, d := range docs {
			// Defensive: documents.list normally omits these, but a document that
			// Outline considers removed must never be ingested as content.
			if d.isGone() {
				skippedGone++
				continue
			}
			if strings.TrimSpace(d.TemplateID) != "" {
				skippedTemplate++
				continue
			}

			current[d.ID] = true
			newCursor.CollectionDocRevisions[collectionID][d.ID] = d.Revision

			if incremental && prev != nil && prev.CollectionDocRevisions != nil {
				if prevRevs, ok := prev.CollectionDocRevisions[collectionID]; ok {
					if r, seen := prevRevs[d.ID]; seen && r == d.Revision {
						skippedUnchanged++
						continue
					}
				}
			}
			kept++

			// Images are inlined only when the target KB can actually make use of
			// them: ingesting an image into a KB without VLM is rejected, and the
			// base64 payload would inflate the upload for nothing.
			body := d.Text
			inlined := 0
			if config.MultimodalEnabled {
				body, inlined = embedAttachmentImages(ctx, cli, body)
			}
			body = stripEscapeArtifacts(body)

			meta := map[string]string{
				"channel":        types.ChannelOutline,
				"document_id":    d.ID,
				"collection_id":  d.CollectionID,
				"revision":       strconv.Itoa(d.Revision),
				"images_inlined": strconv.Itoa(inlined),
			}
			if pid := strings.TrimSpace(d.ParentDocumentID); pid != "" {
				meta["parent_document_id"] = pid
			}

			title := strings.TrimSpace(d.Title)
			if title == "" {
				title = "Untitled"
			}

			out = append(out, types.FetchedItem{
				ExternalID:       d.ID,
				Title:            title,
				Content:          []byte(body),
				ContentType:      "text/markdown",
				FileName:         sanitizeFileName(title) + ".md",
				URL:              absoluteURL(base, d.URL),
				UpdatedAt:        parseOutlineTime(d.UpdatedAt),
				CreatedAt:        parseOutlineTime(d.CreatedAt),
				SourceResourceID: collectionID,
				Metadata:         meta,
			})
		}

		logger.Infof(ctx,
			"[Outline] collection %s: total=%d kept=%d skipped_unchanged=%d skipped_removed=%d skipped_template=%d",
			collectionID, len(docs), kept, skippedUnchanged, skippedGone, skippedTemplate)

		if incremental && prev != nil && prev.CollectionDocRevisions != nil {
			if prevRevs, ok := prev.CollectionDocRevisions[collectionID]; ok {
				for prevDocID := range prevRevs {
					if !current[prevDocID] {
						out = append(out, types.FetchedItem{
							ExternalID:       prevDocID,
							IsDeleted:        true,
							SourceResourceID: collectionID,
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

// absoluteURL turns the path Outline returns ("/doc/title-abc123") into a URL a
// browser can open. A value that is already absolute is returned unchanged.
func absoluteURL(baseURL, pathOrURL string) string {
	p := strings.TrimSpace(pathOrURL)
	if p == "" {
		return baseURL
	}
	if strings.Contains(p, "://") {
		return p
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return baseURL + p
}
