// Package confluence imports Confluence Server/Data Center and Cloud spaces as Markdown.
package confluence

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	htmltomd "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

var (
	_ datasource.StreamingConnector     = (*Connector)(nil)
	_ datasource.FullStreamingConnector = (*Connector)(nil)
)

// Connector implements datasource.StreamingConnector for Confluence.
type Connector struct {
	newClient  func(config) (*client, error)
	spaceCache spaceCache
}

const pickerSpaceCacheTTL = time.Minute

type spaceCache struct {
	mu      sync.Mutex
	entries map[string]cachedSpaces
}

type cachedSpaces struct {
	values  []space
	expires time.Time
}

// NewConnector creates a Confluence connector.
func NewConnector() *Connector { return &Connector{newClient: newClient} }

func (c *Connector) pickerSpaces(ctx context.Context, client *client, cfg config) ([]space, error) {
	key := pickerSpaceCacheKey(cfg)
	now := time.Now()
	c.spaceCache.mu.Lock()
	if cached, ok := c.spaceCache.entries[key]; ok && now.Before(cached.expires) {
		values := append([]space(nil), cached.values...)
		c.spaceCache.mu.Unlock()
		return values, nil
	}
	c.spaceCache.mu.Unlock()
	spaces, err := client.spaces(ctx)
	if err != nil {
		return nil, err
	}
	c.spaceCache.mu.Lock()
	if c.spaceCache.entries == nil {
		c.spaceCache.entries = make(map[string]cachedSpaces)
	}
	for key, cached := range c.spaceCache.entries {
		if !now.Before(cached.expires) {
			delete(c.spaceCache.entries, key)
		}
	}
	c.spaceCache.entries[key] = cachedSpaces{values: append([]space(nil), spaces...), expires: now.Add(pickerSpaceCacheTTL)}
	c.spaceCache.mu.Unlock()
	return spaces, nil
}

// pickerSpaceCacheKey partitions cached space visibility by every credential
// component that can affect permissions. The secret is fingerprinted so it
// never becomes a map key retained in process memory or appears in diagnostics.
func pickerSpaceCacheKey(cfg config) string {
	fingerprint := sha256.Sum256([]byte(cfg.secret))
	return cfg.edition + "\x00" + cfg.baseURL + "\x00" + cfg.username + "\x00" + fmt.Sprintf("%x", fingerprint)
}

// Type returns the connector type identifier.
func (*Connector) Type() string { return types.ConnectorTypeConfluence }

func (c *Connector) configured(ds *types.DataSourceConfig) (*client, config, error) {
	cfg, err := parseConfig(ds)
	if err != nil {
		return nil, config{}, err
	}
	factory := c.newClient
	if factory == nil {
		factory = newClient
	}
	client, err := factory(cfg)
	return client, cfg, err
}

// Validate pings Confluence with the supplied credentials.
func (c *Connector) Validate(ctx context.Context, ds *types.DataSourceConfig) error {
	client, _, err := c.configured(ds)
	if err != nil {
		return err
	}
	return client.ping(ctx)
}

// ListResources exposes a lazy Space → Page tree. It never scans a whole
// space merely to render the picker.
func (c *Connector) ListResources(
	ctx context.Context, ds *types.DataSourceConfig, parentID string,
) ([]types.Resource, error) {
	client, cfg, err := c.configured(ds)
	if err != nil {
		return nil, err
	}
	if parentID == "" {
		spaces, err := c.pickerSpaces(ctx, client, cfg)
		if err != nil {
			return nil, err
		}
		out := make([]types.Resource, 0, len(spaces))
		for _, s := range spaces {
			metadata := map[string]interface{}{"space_key": s.Key}
			if cfg.cloud() {
				// Cloud cannot enumerate a space's top-level folders, so pages under
				// them are unselectable in the picker; the frontend surfaces this
				// limitation when the space is expanded.
				metadata["hierarchy_limitation"] = "cloud_top_level_containers"
			}
			out = append(out, types.Resource{
				ExternalID: s.ID, Name: s.Name, Type: "space", URL: client.resourceURL(s.Links.WebUI), HasChildren: true,
				Metadata: metadata,
			})
		}
		return out, nil
	}
	ref, err := parseResourceID(parentID)
	if err != nil {
		return nil, err
	}
	spaces, err := c.pickerSpaces(ctx, client, cfg)
	if err != nil {
		return nil, err
	}
	var selected space
	found := false
	for _, s := range spaces {
		if s.ID == ref.SpaceID {
			selected, found = s, true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("Confluence space %s is unavailable", ref.SpaceID)
	}
	var pages []page
	if ref.Kind == resourceSpace {
		pages, err = client.topLevelPages(ctx, selected)
	} else {
		if ref.Kind == resourcePage {
			if err := client.validatePageOwnership(ctx, selected, ref.PageID); err != nil {
				return nil, fmt.Errorf("validate Confluence page %s: %w", ref.PageID, err)
			}
		}
		pages, err = client.directChildPages(ctx, selected, ref.PageID)
	}
	if err != nil {
		return nil, fmt.Errorf("list Confluence resources under %s: %w", parentID, err)
	}
	out := make([]types.Resource, 0, len(pages))
	for _, p := range pages {
		out = append(out, pageResource(client, selected, parentID, p))
	}
	return out, nil
}

func pageResource(client *client, s space, parentID string, p page) types.Resource {
	return types.Resource{
		ExternalID: makePageResourceID(s.ID, p.ID), ParentID: parentID, Name: p.Title, Type: "page",
		URL: client.resourceURL(p.Links.WebUI), HasChildren: true,
		Metadata: map[string]interface{}{"space_id": s.ID, "page_id": p.ID},
	}
}

// ResolveResourceAncestors expands only the O(depth) path required to reveal
// persisted page selections in the lazy picker. Resolution is best effort:
// a deleted page, an unavailable space, or a malformed id skips just its own
// reveal path instead of failing every saved selection; the sync path keeps
// its strict validation in buildSyncPlan.
func (c *Connector) ResolveResourceAncestors(ctx context.Context, ds *types.DataSourceConfig, resourceIDs []string) ([]string, error) {
	client, _, err := c.configured(ds)
	if err != nil {
		return nil, err
	}
	spaces, err := client.spaces(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]space, len(spaces))
	for _, s := range spaces {
		byID[s.ID] = s
	}
	seen, out := map[string]bool{}, []string{}
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, id := range resourceIDs {
		ref, err := parseResourceID(id)
		if err != nil {
			logger.Warnf(ctx, "skip malformed Confluence resource %q during ancestor resolution: %v", id, err)
			continue
		}
		if ref.Kind == resourceSpace {
			continue
		}
		s, ok := byID[ref.SpaceID]
		if !ok {
			logger.Warnf(ctx, "skip Confluence resource %s: space %s is unavailable", id, ref.SpaceID)
			continue
		}
		ancestors, err := client.pageAncestors(ctx, s, ref.PageID)
		if err != nil {
			logger.Warnf(ctx, "skip Confluence resource %s during ancestor resolution: %v", id, err)
			continue
		}
		add(s.ID)
		for _, ancestor := range ancestors {
			if ancestor.Kind != "" && ancestor.Kind != "page" {
				continue
			}
			add(makePageResourceID(s.ID, ancestor.ID))
		}
	}
	return out, nil
}

// FetchAll syncs every selected space. Deletion reconciliation needs FetchStream.
func (c *Connector) FetchAll(
	ctx context.Context, ds *types.DataSourceConfig, resourceIDs []string,
) ([]types.FetchedItem, error) {
	dsCopy := *ds
	dsCopy.ResourceIDs = resourceIDs
	return c.collect(ctx, &dsCopy, nil)
}

// FetchIncremental syncs pages whose versions changed since the last cursor.
func (c *Connector) FetchIncremental(
	ctx context.Context, ds *types.DataSourceConfig, old *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	items, next, err := c.collectWithCursor(ctx, ds, old)
	return items, next, err
}

type collector struct{ items []types.FetchedItem }

func (h *collector) Emit(_ context.Context, item types.FetchedItem) error {
	h.items = append(h.items, item)
	return nil
}
func (*collector) Checkpoint(context.Context, *types.SyncCursor) error { return nil }

func (c *Connector) collect(
	ctx context.Context, ds *types.DataSourceConfig, old *types.SyncCursor,
) ([]types.FetchedItem, error) {
	items, _, err := c.collectWithCursor(ctx, ds, old)
	return items, err
}

func (c *Connector) collectWithCursor(
	ctx context.Context, ds *types.DataSourceConfig, old *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	h := &collector{}
	next, err := c.FetchStream(ctx, ds, old, h)
	return h.items, next, err
}

// FetchStream is the one sync engine. A version enters its cursor only after
// its Markdown has been fetched and Emit has succeeded, making checkpoints safe.
func (c *Connector) FetchStream(
	ctx context.Context, ds *types.DataSourceConfig, old *types.SyncCursor, h datasource.StreamHandler,
) (*types.SyncCursor, error) {
	return c.fetchStream(ctx, ds, old, h, false)
}

// FetchFullStream forces every page to be re-fetched while retaining old solely
// as a complete baseline for deletion reconciliation. Mid-run checkpoints keep
// that baseline plus the pages already re-fetched so Asynq retries resume
// instead of starting over.
func (c *Connector) FetchFullStream(
	ctx context.Context, ds *types.DataSourceConfig, old *types.SyncCursor, h datasource.StreamHandler,
) (*types.SyncCursor, error) {
	return c.fetchStream(ctx, ds, old, h, true)
}

func (c *Connector) fetchStream(
	ctx context.Context,
	ds *types.DataSourceConfig,
	old *types.SyncCursor,
	h datasource.StreamHandler,
	forceFull bool,
) (*types.SyncCursor, error) {
	if ds == nil || len(ds.ResourceIDs) == 0 {
		return nil, fmt.Errorf("confluence requires at least one selected space")
	}
	client, _, err := c.configured(ds)
	if err != nil {
		return nil, err
	}
	plan, err := c.buildSyncPlan(ctx, client, ds.ResourceIDs)
	if err != nil {
		return nil, err
	}
	baseline, next := prepareSyncCursors(old, forceFull)
	// A prior successful body is reusable when ownership moves to a different
	// selected root and this run cannot fetch the new body. Keep that version in
	// the new root rather than dropping it during ownership reconciliation.
	priorVersionByPage := make(map[string]string)
	for _, pages := range baseline.SpacePages {
		for id, version := range pages {
			if _, exists := priorVersionByPage[id]; !exists {
				priorVersionByPage[id] = version
			}
		}
	}
	// A page may be returned by multiple overlapping roots (for example during
	// a configuration change). Fetch it once, while retaining a cursor entry
	// per scope root so existing cursor JSON remains compatible.
	seenPages := make(map[string]struct{})
	incompleteRoots := make(map[string]bool)
	for _, root := range plan.Roots {
		resourceID, s := root.ResourceID, root.Space
		var pages []page
		complete := true
		if root.Kind == syncWholeSpace {
			pages, complete, err = client.pages(ctx, s)
		} else {
			pages, err = client.pageSubtree(ctx, s, root.PageID)
		}
		if err != nil {
			return nil, fmt.Errorf("list pages in Confluence scope %s: %w", resourceID, err)
		}
		priorPages := baseline.SpacePages[resourceID]
		if !complete {
			logger.Warnf(ctx, "[Confluence] space %s listing was cut short; checking unlisted pages before deleting", s.Key)
			incompleteRoots[resourceID] = true
		}
		if next.SpacePages[resourceID] == nil {
			next.SpacePages[resourceID] = map[string]string{}
		}
		if len(pages) == 0 && len(priorPages) > 0 {
			return nil, fmt.Errorf(
				"refusing Confluence mirror deletion in %s: listing returned 0 pages against a %d-page baseline",
				s.Key, len(priorPages),
			)
		}
		for _, summary := range pages {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if _, duplicate := seenPages[summary.ID]; duplicate {
				// The first scope already owns the emitted document; preserve this
				// root's version record so a later selection change stays incremental.
				if version, ok := priorPages[summary.ID]; ok {
					next.SpacePages[resourceID][summary.ID] = version
				}
				continue
			}
			seenPages[summary.ID] = struct{}{}
			version, versionKnown := pageVersion(summary)
			// An unknown version cannot prove a page unchanged, so it must
			// always refresh the body instead of skipping on a stable "t:".
			if versionKnown {
				if forceFull {
					if next.SpacePages[resourceID][summary.ID] == version {
						continue
					}
				} else if priorPages[summary.ID] == version {
					continue
				}
			}
			full, err := client.body(ctx, summary.ID)
			if err != nil {
				if ctx.Err() != nil {
					return nil, err
				}
				if emitErr := h.Emit(ctx, failedPageItem(resourceID, summary, err)); emitErr != nil {
					return nil, emitErr
				}
				if prior, ok := priorVersionByPage[summary.ID]; ok {
					next.SpacePages[resourceID][summary.ID] = prior
				}
				continue
			}
			item, err := markdownItem(client, resourceID, summary, full)
			if err != nil {
				if emitErr := h.Emit(ctx, failedPageItem(resourceID, summary, err)); emitErr != nil {
					return nil, emitErr
				}
				if prior, ok := priorVersionByPage[summary.ID]; ok {
					next.SpacePages[resourceID][summary.ID] = prior
				}
				continue
			}
			if err := h.Emit(ctx, item); err != nil {
				return nil, err
			}
			next.SpacePages[resourceID][summary.ID] = version
			if err := h.Checkpoint(ctx, next.syncCursor()); err != nil {
				return nil, err
			}
		}
		if !complete {
			if err := reconcileUnlisted(ctx, client, h, s, resourceID, priorPages, seenPages, &next); err != nil {
				return nil, err
			}
		}
	}
	// A root can disappear when a user changes a whole-space selection to a
	// page subtree. Reconcile all previous owners only after every current scope
	// has enumerated successfully (an enumeration error returned above emits no
	// tombstones). This is deliberately global: a page moving between roots must
	// remain present when any current root still contains it.
	previousPages := make(map[string]string)
	for owner, pages := range baseline.SpacePages {
		if incompleteRoots[owner] {
			continue
		}
		for id := range pages {
			if _, exists := previousPages[id]; !exists {
				previousPages[id] = owner
			}
		}
	}
	missing := make([]string, 0)
	for id := range previousPages {
		if _, present := seenPages[id]; !present {
			missing = append(missing, id)
		}
	}
	// Protect every historical scope, including roots which were removed from
	// the selection. A configuration change must not silently turn a broad
	// mirror deletion into hundreds of tombstones; a later explicit confirmation
	// policy can decide how to release this guard.
	owners := make([]string, 0, len(baseline.SpacePages))
	for owner := range baseline.SpacePages {
		owners = append(owners, owner)
	}
	sort.Strings(owners)
	for _, owner := range owners {
		if incompleteRoots[owner] {
			continue
		}
		pages := baseline.SpacePages[owner]
		missingInRoot := 0
		for id := range pages {
			if _, present := seenPages[id]; !present {
				missingInRoot++
			}
		}
		if len(pages) > 0 && missingInRoot >= 20 && missingInRoot*100 >= len(pages)*80 {
			return nil, fmt.Errorf(
				"refusing Confluence mirror deletion: %d/%d previously synced pages would be removed from scope %s",
				missingInRoot, len(pages), owner,
			)
		}
	}
	sort.Strings(missing)
	for _, id := range missing {
		if err := h.Emit(ctx, types.FetchedItem{ExternalID: id, IsDeleted: true, SourceResourceID: previousPages[id], Metadata: map[string]string{"channel": types.ChannelConfluence}}); err != nil {
			return nil, err
		}
		// Drop the tombstoned page before checkpointing so the cursor records
		// the deletion itself; an interrupted run resumes with the remaining
		// candidates instead of re-emitting completed tombstones.
		removePageOwnership(&next, id)
		if err := h.Checkpoint(ctx, next.syncCursor()); err != nil {
			return nil, err
		}
	}
	// Commit exactly the current ownership roots only after reconciliation. This
	// removes cancelled scopes while keeping the on-disk cursor field compatible.
	committedRoots := make(map[string]map[string]string, len(plan.Roots))
	for _, root := range plan.Roots {
		if pages := next.SpacePages[root.ResourceID]; pages != nil {
			committedRoots[root.ResourceID] = pages
		} else {
			committedRoots[root.ResourceID] = map[string]string{}
		}
	}
	next.SpacePages = committedRoots
	next.FullSync = false
	next.FullSyncBaseline = nil
	return next.syncCursor(), nil
}

func reconcileUnlisted(ctx context.Context, c *client, h datasource.StreamHandler, s space, resourceID string, priorPages map[string]string, seen map[string]struct{}, next *cursor) error {
	probed := 0
	for id, version := range priorPages {
		if _, exists := seen[id]; exists {
			continue
		}
		if probed >= maxDeletionProbes {
			next.SpacePages[resourceID][id] = version
			continue
		}
		probed++
		present, err := c.pageInSpace(ctx, id, s.Key)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			logger.Warnf(ctx, "[Confluence] space %s: could not check unlisted page %s, keeping it: %v", s.Key, id, err)
			next.SpacePages[resourceID][id] = version
			continue
		}
		if present {
			next.SpacePages[resourceID][id] = version
			continue
		}
		if err := emitDeleted(ctx, h, next, resourceID, id); err != nil {
			return err
		}
	}
	return nil
}

func emitDeleted(ctx context.Context, h datasource.StreamHandler, next *cursor, resourceID, id string) error {
	if err := h.Emit(ctx, types.FetchedItem{ExternalID: id, IsDeleted: true, SourceResourceID: resourceID, Metadata: map[string]string{"channel": types.ChannelConfluence}}); err != nil {
		return err
	}
	delete(next.SpacePages[resourceID], id)
	return h.Checkpoint(ctx, next.syncCursor())
}

func failedPageItem(resourceID string, summary page, err error) types.FetchedItem {
	code, reason := classifyConfluenceError(err)
	return types.FetchedItem{
		ExternalID:       summary.ID,
		Title:            summary.Title,
		SourceResourceID: resourceID,
		Metadata: map[string]string{
			"channel":           types.ChannelConfluence,
			"error":             err.Error(),
			"error_reason_code": code,
			"error_reason":      reason,
			"page_id":           summary.ID,
		},
	}
}

func classifyConfluenceError(err error) (code, reason string) {
	var api *apiError
	if errors.As(err, &api) {
		switch api.status {
		case http.StatusUnauthorized, http.StatusForbidden:
			return "confluence_auth_or_permission",
				"Authentication or permission error; check credentials and space permissions"
		case http.StatusNotFound:
			return "confluence_not_found", "Confluence page was not found; will retry on the next sync"
		case http.StatusTooManyRequests:
			return "confluence_rate_limited", "Confluence API rate limited; will retry on the next sync"
		default:
			if api.status >= 500 {
				return "confluence_server_unavailable",
					"Confluence service temporarily unavailable; will retry on the next sync"
			}
			return "confluence_api_error", "Confluence API error; see server logs"
		}
	}
	return "confluence_sync_failed", "Confluence page could not be synced; see server logs"
}

func markdownItem(client *client, resourceID string, summary page, full pageBody) (types.FetchedItem, error) {
	html := full.Body.View.Value
	markdown, err := htmltomd.ConvertString(html)
	if err != nil {
		return types.FetchedItem{}, err
	}
	if strings.TrimSpace(markdown) == "" {
		markdown = "# " + summary.Title + "\n"
	}
	if full.Title != "" {
		summary.Title = full.Title
	}
	if full.Version.Number > 0 || full.Version.When != "" {
		summary.Version = full.Version
	}
	if full.Space.Key != "" {
		summary.Space = full.Space
	}
	if webui := strings.TrimSpace(summary.Links.WebUI); webui == "" && full.Links.WebUI != "" {
		summary.Links.WebUI = full.Links.WebUI
	}
	metadata := map[string]string{
		"channel": types.ChannelConfluence, "space_key": summary.Space.Key,
		"space_name": summary.Space.Name, "page_id": summary.ID,
	}
	if creator := strings.TrimSpace(summary.Version.By.DisplayName); creator != "" {
		metadata["creator"] = creator
	}
	return types.FetchedItem{
		ExternalID:       summary.ID,
		Title:            summary.Title,
		Content:          []byte(markdown),
		ContentType:      "text/markdown",
		FileName:         pageFileName(summary.Title, summary.ID),
		URL:              client.resourceURL(summary.Links.WebUI),
		UpdatedAt:        pageUpdatedAt(summary),
		SourceResourceID: resourceID,
		Metadata:         metadata,
	}, nil
}
