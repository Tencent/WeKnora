// Package confluence imports Confluence Server/Data Center and Cloud spaces as Markdown.
package confluence

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

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
	newClient func(config) (*client, error)
}

// NewConnector creates a Confluence connector.
func NewConnector() *Connector { return &Connector{newClient: newClient} }

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

// ListResources returns the Confluence spaces the credentials can see.
func (c *Connector) ListResources(
	ctx context.Context, ds *types.DataSourceConfig, parentID string,
) ([]types.Resource, error) {
	if parentID != "" {
		return nil, nil
	}
	client, _, err := c.configured(ds)
	if err != nil {
		return nil, err
	}
	spaces, err := client.spaces(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]types.Resource, 0, len(spaces))
	for _, s := range spaces {
		out = append(out, types.Resource{
			ExternalID:  s.ID,
			Name:        s.Name,
			Type:        "space",
			URL:         client.resourceURL(s.Links.WebUI),
			HasChildren: false,
			Metadata:    map[string]interface{}{"space_key": s.Key},
		})
	}
	return out, nil
}

// ResolveResourceAncestors is a no-op: Confluence spaces are a flat list.
func (*Connector) ResolveResourceAncestors(context.Context, *types.DataSourceConfig, []string) ([]string, error) {
	return nil, nil
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
	spaces, err := client.spaces(ctx)
	if err != nil {
		return nil, fmt.Errorf("list Confluence spaces: %w", err)
	}
	byID := make(map[string]space, len(spaces))
	for _, s := range spaces {
		byID[s.ID] = s
	}
	baseline, next := prepareSyncCursors(old, forceFull)
	for _, resourceID := range ds.ResourceIDs {
		s, found := byID[resourceID]
		if !found {
			return nil, fmt.Errorf(
				"selected Confluence space %s is unavailable; refusing destructive reconciliation",
				resourceID,
			)
		}
		pages, complete, err := client.pages(ctx, s)
		if err != nil {
			return nil, fmt.Errorf("list pages in Confluence space %s: %w", s.Key, err)
		}
		if !complete {
			logger.Warnf(ctx,
				"[Confluence] space %s: listing stopped after %d empty pages that still linked onward; "+
					"listed %d pages, checking unlisted ones one by one before deleting",
				s.Key, maxEmptyServerPages, len(pages))
		}
		priorPages, hadBaseline := baseline.SpacePages[resourceID]
		if next.SpacePages[resourceID] == nil {
			next.SpacePages[resourceID] = map[string]string{}
		}
		seen := make(map[string]struct{}, len(pages))
		for _, summary := range pages {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			seen[summary.ID] = struct{}{}
			version := pageVersion(summary)
			if forceFull {
				if next.SpacePages[resourceID][summary.ID] == version {
					continue
				}
			} else if priorPages[summary.ID] == version {
				continue
			}
			full, err := client.body(ctx, summary.ID)
			if err != nil {
				if ctx.Err() != nil {
					return nil, err
				}
				if emitErr := h.Emit(ctx, failedPageItem(resourceID, summary, err)); emitErr != nil {
					return nil, emitErr
				}
				continue
			}
			item, err := markdownItem(client, resourceID, summary, full)
			if err != nil {
				if emitErr := h.Emit(ctx, failedPageItem(resourceID, summary, err)); emitErr != nil {
					return nil, emitErr
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
			if err := reconcileUnlisted(ctx, client, h, s, resourceID, priorPages, seen, &next); err != nil {
				return nil, err
			}
		} else if hadBaseline {
			if len(pages) == 0 && len(priorPages) > 0 {
				return nil, fmt.Errorf(
					"refusing Confluence mirror deletion in %s: listing returned 0 pages against a %d-page baseline",
					s.Key, len(priorPages),
				)
			}
			missing := 0
			for id := range priorPages {
				if _, exists := seen[id]; !exists {
					missing++
				}
			}
			if missing >= 20 && missing*100 >= len(priorPages)*80 {
				return nil, fmt.Errorf(
					"refusing Confluence mirror deletion in %s: %d/%d pages disappeared",
					s.Key, missing, len(priorPages),
				)
			}
			for id := range priorPages {
				if _, exists := seen[id]; exists {
					continue
				}
				if err := emitDeleted(ctx, h, &next, resourceID, id); err != nil {
					return nil, err
				}
			}
		}
	}
	next.FullSync = false
	next.FullSyncBaseline = nil
	return next.syncCursor(), nil
}

// reconcileUnlisted handles the pages a cut-short listing did not return. The
// listing proves nothing about them, so each one is looked up on its own and
// deleted only when Confluence confirms it is gone from the space. Pages that
// are still there, could not be checked, or exceed the per-run probe budget
// stay tracked with their prior version and are checked again next run.
func reconcileUnlisted(
	ctx context.Context,
	c *client,
	h datasource.StreamHandler,
	s space,
	resourceID string,
	priorPages map[string]string,
	seen map[string]struct{},
	next *cursor,
) error {
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
	deleted := types.FetchedItem{
		ExternalID: id, IsDeleted: true, SourceResourceID: resourceID,
		Metadata: map[string]string{"channel": types.ChannelConfluence},
	}
	if err := h.Emit(ctx, deleted); err != nil {
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
