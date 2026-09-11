package outline

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
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
