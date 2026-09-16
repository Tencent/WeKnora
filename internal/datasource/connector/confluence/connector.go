// Package confluence imports Confluence Server/Data Center and Cloud spaces as Markdown.
package confluence

import (
	"context"
	"fmt"
	"strings"

	htmltomd "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

var _ datasource.StreamingConnector = (*Connector)(nil)
var _ datasource.FullStreamingConnector = (*Connector)(nil)

type Connector struct {
	newClient func(config) (*client, error)
}

func NewConnector() *Connector  { return &Connector{newClient: newClient} }
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
func (c *Connector) Validate(ctx context.Context, ds *types.DataSourceConfig) error {
	client, _, err := c.configured(ds)
	if err != nil {
		return err
	}
	return client.ping(ctx)
}
func (c *Connector) ListResources(ctx context.Context, ds *types.DataSourceConfig, parentID string) ([]types.Resource, error) {
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
		resourceURL, err := client.resolveEndpoint(s.Links.WebUI)
		if err != nil {
			return nil, fmt.Errorf("resolve Confluence space URL: %w", err)
		}
		out = append(out, types.Resource{ExternalID: s.ID, Name: s.Name, Type: "space", URL: resourceURL, HasChildren: false, Metadata: map[string]interface{}{"space_key": s.Key}})
	}
	return out, nil
}
func (*Connector) ResolveResourceAncestors(context.Context, *types.DataSourceConfig, []string) ([]string, error) {
	return nil, nil
}
func (c *Connector) FetchAll(ctx context.Context, ds *types.DataSourceConfig, resourceIDs []string) ([]types.FetchedItem, error) {
	copy := *ds
	copy.ResourceIDs = resourceIDs
	return c.collect(ctx, &copy, nil)
}
func (c *Connector) FetchIncremental(ctx context.Context, ds *types.DataSourceConfig, old *types.SyncCursor) ([]types.FetchedItem, *types.SyncCursor, error) {
	items, next, err := c.collectWithCursor(ctx, ds, old)
	return items, next, err
}

type collector struct{ items []types.FetchedItem }

func (h *collector) Emit(_ context.Context, item types.FetchedItem) error {
	h.items = append(h.items, item)
	return nil
}
func (*collector) Checkpoint(context.Context, *types.SyncCursor) error { return nil }
func (c *Connector) collect(ctx context.Context, ds *types.DataSourceConfig, old *types.SyncCursor) ([]types.FetchedItem, error) {
	items, _, err := c.collectWithCursor(ctx, ds, old)
	return items, err
}
func (c *Connector) collectWithCursor(ctx context.Context, ds *types.DataSourceConfig, old *types.SyncCursor) ([]types.FetchedItem, *types.SyncCursor, error) {
	h := &collector{}
	next, err := c.FetchStream(ctx, ds, old, h)
	return h.items, next, err
}

// FetchStream is the one sync engine. A version enters its cursor only after
// its Markdown has been fetched and Emit has succeeded, making checkpoints safe.
func (c *Connector) FetchStream(ctx context.Context, ds *types.DataSourceConfig, old *types.SyncCursor, h datasource.StreamHandler) (*types.SyncCursor, error) {
	return c.fetchStream(ctx, ds, old, h, false)
}

// FetchFullStream forces every page to be re-fetched while retaining old solely
// as a complete baseline for deletion reconciliation.
func (c *Connector) FetchFullStream(ctx context.Context, ds *types.DataSourceConfig, old *types.SyncCursor, h datasource.StreamHandler) (*types.SyncCursor, error) {
	return c.fetchStream(ctx, ds, old, h, true)
}

func (c *Connector) fetchStream(ctx context.Context, ds *types.DataSourceConfig, old *types.SyncCursor, h datasource.StreamHandler, forceFull bool) (*types.SyncCursor, error) {
	if ds == nil || len(ds.ResourceIDs) == 0 {
		return nil, fmt.Errorf("Confluence requires at least one selected space")
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
	previous, next := decodeCursor(old), decodeCursor(old).clone()
	for _, resourceID := range ds.ResourceIDs {
		s, found := byID[resourceID]
		if !found {
			return nil, fmt.Errorf("selected Confluence space %s is unavailable; refusing destructive reconciliation", resourceID)
		}
		pages, err := client.pages(ctx, s)
		if err != nil {
			return nil, fmt.Errorf("list pages in Confluence space %s: %w", s.Key, err)
		}
		priorPages, hadBaseline := previous.SpacePages[resourceID]
		if next.SpacePages[resourceID] == nil {
			next.SpacePages[resourceID] = map[string]string{}
		}
		seen := make(map[string]struct{}, len(pages))
		for _, summary := range pages {
			seen[summary.ID] = struct{}{}
			version := pageVersion(summary)
			if !forceFull && priorPages[summary.ID] == version {
				continue
			}
			full, err := client.body(ctx, summary.ID)
			if err != nil {
				return nil, fmt.Errorf("fetch Confluence page %s: %w", summary.ID, err)
			}
			item, err := markdownItem(client, resourceID, summary, full)
			if err != nil {
				return nil, fmt.Errorf("convert Confluence page %s: %w", summary.ID, err)
			}
			if err := h.Emit(ctx, item); err != nil {
				return nil, err
			}
			next.SpacePages[resourceID][summary.ID] = version
			if err := h.Checkpoint(ctx, next.syncCursor()); err != nil {
				return nil, err
			}
		}
		if hadBaseline {
			missing := 0
			for id := range priorPages {
				if _, exists := seen[id]; !exists {
					missing++
				}
			}
			if missing >= 20 && missing*100 >= len(priorPages)*80 {
				return nil, fmt.Errorf("refusing Confluence mirror deletion in %s: %d/%d pages disappeared", s.Key, missing, len(priorPages))
			}
			for id := range priorPages {
				if _, exists := seen[id]; exists {
					continue
				}
				if err := h.Emit(ctx, types.FetchedItem{ExternalID: id, IsDeleted: true, SourceResourceID: resourceID, Metadata: map[string]string{"channel": types.ChannelConfluence}}); err != nil {
					return nil, err
				}
				delete(next.SpacePages[resourceID], id)
				if err := h.Checkpoint(ctx, next.syncCursor()); err != nil {
					return nil, err
				}
			}
		}
	}
	return next.syncCursor(), nil
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
	pageURL, err := client.resolveEndpoint(summary.Links.WebUI)
	if err != nil {
		return types.FetchedItem{}, fmt.Errorf("resolve Confluence page URL: %w", err)
	}
	metadata := map[string]string{"channel": types.ChannelConfluence, "space_key": summary.Space.Key, "space_name": summary.Space.Name, "page_id": summary.ID}
	if creator := strings.TrimSpace(summary.Version.By.DisplayName); creator != "" {
		metadata["creator"] = creator
	}
	return types.FetchedItem{ExternalID: summary.ID, Title: summary.Title, Content: []byte(markdown), ContentType: "text/markdown", FileName: safeFilename(summary.Title) + ".md", URL: pageURL, UpdatedAt: pageUpdatedAt(summary), SourceResourceID: resourceID, Metadata: metadata}, nil
}
