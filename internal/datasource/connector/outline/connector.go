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
var (
	_ datasource.Connector          = (*Connector)(nil)
	_ datasource.FullSyncWithCursor = (*Connector)(nil)
)

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
	_ context.Context, _ *types.DataSourceConfig, _ []string,
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

// FetchAllFromCursor re-fetches every document and reconciles deletions against
// the previous cursor, so a full sync (ForceFull or sync_mode=full) still honours
// deletion_sync and leaves a fresh cursor for the next incremental run.
func (c *Connector) FetchAllFromCursor(
	ctx context.Context, config *types.DataSourceConfig, resourceIDs []string, cursor *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	return c.walkAndEncode(ctx, config, resourceIDs, decodeCursor(ctx, cursor), false)
}

// FetchIncremental returns documents changed (or deleted) since the prior cursor.
//
// Change detection compares Outline's per-document `revision`, which increments
// only on a content or title edit — a tighter signal than updatedAt, which also
// moves on activity that leaves the document unchanged.
//
// Deletion detection: a document listed in the prior cursor but absent from
// every selected collection is emitted as an IsDeleted placeholder. Outline
// omits trashed and archived documents from documents.list, so this covers both.
func (c *Connector) FetchIncremental(
	ctx context.Context, config *types.DataSourceConfig, cursor *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	if len(config.ResourceIDs) == 0 {
		return nil, nil, fmt.Errorf("no resource IDs (collection IDs) configured")
	}
	return c.walkAndEncode(ctx, config, config.ResourceIDs, decodeCursor(ctx, cursor), true)
}

// decodeCursor reads the prior Outline cursor, or nil when there is none.
func decodeCursor(ctx context.Context, cursor *types.SyncCursor) *outlineCursor {
	if cursor == nil || cursor.ConnectorCursor == nil {
		return nil
	}
	var p outlineCursor
	if b, err := json.Marshal(cursor.ConnectorCursor); err == nil {
		if err := json.Unmarshal(b, &p); err == nil {
			return &p
		}
	}
	// An undecodable cursor means one full pass, which is correct but
	// expensive — say so rather than failing the sync silently.
	logger.Warnf(ctx, "[Outline] prior cursor could not be decoded; falling back to a full pass")
	return nil
}

// walkAndEncode runs walk and wraps its cursor in the framework's SyncCursor.
func (c *Connector) walkAndEncode(
	ctx context.Context,
	config *types.DataSourceConfig,
	resourceIDs []string,
	prev *outlineCursor,
	skipUnchanged bool,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	items, newCursor, err := c.walk(ctx, config, resourceIDs, prev, skipUnchanged)
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

// walk is the shared implementation behind every fetch. skipUnchanged drops
// documents whose revision matches prev; deletions are reconciled whenever prev
// is non-nil, so a full re-fetch from a cursor reports them too.
func (c *Connector) walk(
	ctx context.Context,
	config *types.DataSourceConfig,
	resourceIDs []string,
	prev *outlineCursor,
	skipUnchanged bool,
) ([]types.FetchedItem, *outlineCursor, error) {
	cfg, err := parseOutlineConfig(config)
	if err != nil {
		return nil, nil, err
	}
	cli := newClient(cfg)
	base := cfg.GetBaseURL()

	// Collection names head every document's folder path.
	cols, err := cli.ListCollections(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("list collections: %w", err)
	}
	collectionNames := make(map[string]string, len(cols))
	for _, col := range cols {
		collectionNames[col.ID] = col.Name
	}

	newCursor := &outlineCursor{
		LastSyncTime:           time.Now(),
		CollectionDocRevisions: make(map[string]map[string]int),
		CollectionDocFolders:   make(map[string]map[string]string),
	}
	var out []types.FetchedItem
	// current spans every selected collection, not just one: see the deletion
	// pass after the loop.
	current := make(map[string]bool)

	for _, collectionID := range resourceIDs {
		docs, err := cli.ListCollectionDocuments(ctx, collectionID)
		if err != nil {
			return nil, nil, fmt.Errorf("list documents for collection %s: %w", collectionID, err)
		}

		newCursor.CollectionDocRevisions[collectionID] = make(map[string]int, len(docs))
		newCursor.CollectionDocFolders[collectionID] = make(map[string]string, len(docs))
		byID := make(map[string]document, len(docs))
		for _, d := range docs {
			byID[d.ID] = d
		}

		var skippedGone, skippedTemplate, skippedUnchanged, moved, kept int
		for _, d := range docs {
			// Defensive: documents.list normally omits these, but a document that
			// Outline considers removed must never be ingested as content.
			if d.isGone() {
				skippedGone++
				continue
			}
			if d.Template {
				skippedTemplate++
				continue
			}

			folder := docFolder(collectionNames[collectionID], d, byID)
			current[d.ID] = true
			newCursor.CollectionDocRevisions[collectionID][d.ID] = d.Revision
			newCursor.CollectionDocFolders[collectionID][d.ID] = folder

			title := docTitle(d)
			fileName := datasource.SanitizeFileName(title) + ".md"
			if folder != "" {
				fileName = folder + "/" + fileName
			}

			// Moving a document or renaming an ancestor leaves its revision alone,
			// so the folder is compared too: otherwise it would stay filed under
			// the old path until its next edit. Such a document is re-filed, not
			// re-ingested — renaming a parent would otherwise re-embed its whole
			// subtree. A move across collections is re-ingested, since the prior
			// revision is keyed by the old collection.
			if skipUnchanged && prev != nil {
				r, seen := prev.CollectionDocRevisions[collectionID][d.ID]
				if seen && r == d.Revision {
					if prev.CollectionDocFolders[collectionID][d.ID] == folder {
						skippedUnchanged++
						continue
					}
					moved++
					out = append(out, types.FetchedItem{
						ExternalID:       d.ID,
						Title:            title,
						FileName:         fileName,
						SourceResourceID: collectionID,
						MoveOnly:         true,
					})
					continue
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

			out = append(out, types.FetchedItem{
				ExternalID:       d.ID,
				Title:            title,
				Content:          []byte(body),
				ContentType:      "text/markdown",
				FileName:         fileName,
				URL:              absoluteURL(base, d.URL),
				UpdatedAt:        parseOutlineTime(d.UpdatedAt),
				CreatedAt:        parseOutlineTime(d.CreatedAt),
				SourceResourceID: collectionID,
				Metadata:         meta,
			})
		}

		logger.Infof(ctx,
			"[Outline] collection %s: total=%d kept=%d moved=%d skipped_unchanged=%d "+
				"skipped_removed=%d skipped_template=%d",
			collectionID, len(docs), kept, moved, skippedUnchanged, skippedGone, skippedTemplate)
	}

	// A document is deleted only when no selected collection lists it any more.
	// One moved between two selected collections is still present, and the
	// service deletes by external_id alone, so a per-collection check would
	// delete the copy the other collection had just re-ingested — and the cursor
	// would then keep it skipped as unchanged.
	if prev != nil {
		for _, collectionID := range resourceIDs {
			for prevDocID := range prev.CollectionDocRevisions[collectionID] {
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

	return out, newCursor, nil
}

func docTitle(d document) string {
	if title := strings.TrimSpace(d.Title); title != "" {
		return title
	}
	return "Untitled"
}

// docFolder returns the knowledge-base folder a document is filed under: its
// collection, then each ancestor document's title, root first. The service
// splits the folder off FileName and caps its depth and length.
//
// The walk stops at a parent missing from byID (an archived or trashed parent
// is not listed) and on a cycle, so malformed data cannot loop.
func docFolder(collectionName string, d document, byID map[string]document) string {
	var segs []string
	seen := map[string]bool{d.ID: true}
	for pid := strings.TrimSpace(d.ParentDocumentID); pid != "" && !seen[pid]; {
		p, ok := byID[pid]
		if !ok {
			break
		}
		seen[pid] = true
		segs = append(segs, datasource.SanitizeFileName(docTitle(p)))
		pid = strings.TrimSpace(p.ParentDocumentID)
	}
	if name := strings.TrimSpace(collectionName); name != "" {
		segs = append(segs, datasource.SanitizeFileName(name))
	}
	for i, j := 0, len(segs)-1; i < j; i, j = i+1, j-1 {
		segs[i], segs[j] = segs[j], segs[i]
	}
	return strings.Join(segs, "/")
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
