package nextcloud

import (
	"context"
	"fmt"
	"net/http"
	"path"
	"sort"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
)

var (
	_ datasource.Connector              = (*Connector)(nil)
	_ datasource.FullStreamingConnector = (*Connector)(nil)
)

// Connector keeps no credentials or sync state between data sources.
type Connector struct{}

// NewConnector creates a Nextcloud WebDAV connector.
func NewConnector() *Connector { return &Connector{} }

// Type returns the registered connector type.
func (*Connector) Type() string { return types.ConnectorTypeNextcloud }

// Validate verifies account access with Depth 0, without enumerating files.
func (*Connector) Validate(ctx context.Context, ds *types.DataSourceConfig) error {
	cfg, err := parseConfig(ds)
	if err != nil {
		return err
	}
	if len(ds.ResourceIDs) > 0 {
		if _, err := collapseRoots(ds.ResourceIDs); err != nil {
			return err
		}
	}
	c := newClient(cfg)
	defer c.http.CloseIdleConnections()
	_, err = c.propfind(ctx, "/", 0)
	if statusOf(err) == http.StatusForbidden {
		return fmt.Errorf("%w: Nextcloud account access denied", datasource.ErrInvalidCredentials)
	}
	return err
}

// ListResources exposes the account root, then lazily lists one directory
// level. The picker lists all supported formats independently of sync filters,
// so editing filters does not require saving them before browsing resources.
func (*Connector) ListResources(
	ctx context.Context, ds *types.DataSourceConfig, parentID string,
) ([]types.Resource, error) {
	cfg, err := parseConfig(ds)
	if err != nil {
		return nil, err
	}
	c := newClient(cfg)
	defer c.http.CloseIdleConnections()
	p, depth := "/", 0
	if parentID != "" {
		p, err = normalizePath(parentID)
		if err != nil {
			return nil, err
		}
		depth = 1
	}
	entries, err := c.propfind(ctx, p, depth)
	if err != nil {
		return nil, mapListingError(err)
	}
	out := make([]types.Resource, 0, len(entries))
	for _, e := range entries {
		if parentID != "" && e.Path == p || !e.IsDir && !utils.IsSupportedImportExtension(path.Ext(e.Path)) {
			continue
		}
		kind, name := "file", path.Base(e.Path)
		if e.IsDir {
			kind = "directory"
		}
		if e.Path == "/" {
			name = "Nextcloud"
		}
		out = append(out, types.Resource{
			ExternalID: e.Path, Name: name, Type: kind,
			ParentID: parentID, HasChildren: e.IsDir, ModifiedAt: e.ModifiedAt,
		})
	}
	return out, nil
}

// ResolveResourceAncestors derives the lazy picker's expansion path locally.
func (*Connector) ResolveResourceAncestors(
	_ context.Context, _ *types.DataSourceConfig, ids []string,
) ([]string, error) {
	seen := map[string]bool{}
	for _, id := range ids {
		p, err := normalizePath(id)
		if err != nil {
			continue
		}
		for p != "/" {
			p = path.Dir(p)
			seen[p] = true
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}

type collector struct{ items []types.FetchedItem }

func (h *collector) Emit(_ context.Context, item types.FetchedItem) error {
	h.items = append(h.items, item)
	return nil
}

func (*collector) Checkpoint(context.Context, *types.SyncCursor) error { return nil }

// FetchAll collects a fresh sync of the selected resources.
func (c *Connector) FetchAll(
	ctx context.Context, ds *types.DataSourceConfig, ids []string,
) ([]types.FetchedItem, error) {
	if ds == nil {
		return nil, datasource.ErrInvalidConfig
	}
	configCopy := *ds
	configCopy.ResourceIDs = ids
	items, _, err := c.FetchIncremental(ctx, &configCopy, nil)
	return items, err
}

// FetchIncremental collects changed files and deletions using the old cursor.
func (c *Connector) FetchIncremental(
	ctx context.Context, ds *types.DataSourceConfig, old *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	h := &collector{}
	next, err := c.FetchStream(ctx, ds, old, h)
	return h.items, next, err
}

// FetchStream downloads one file at a time and checkpoints resumable progress.
func (c *Connector) FetchStream(
	ctx context.Context, ds *types.DataSourceConfig, old *types.SyncCursor, h datasource.StreamHandler,
) (*types.SyncCursor, error) {
	return c.runSync(ctx, ds, old, h, false)
}

// FetchFullStream re-fetches all files while retaining the deletion baseline.
func (c *Connector) FetchFullStream(
	ctx context.Context, ds *types.DataSourceConfig, old *types.SyncCursor, h datasource.StreamHandler,
) (*types.SyncCursor, error) {
	return c.runSync(ctx, ds, old, h, true)
}
