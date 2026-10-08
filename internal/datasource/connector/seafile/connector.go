package seafile

import (
	"context"
	"fmt"
	"net/http"
	"path"
	"sort"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
)

var (
	_ datasource.Connector              = (*Connector)(nil)
	_ datasource.StreamingConnector     = (*Connector)(nil)
	_ datasource.FullStreamingConnector = (*Connector)(nil)
)

// Connector is stateless: every data source carries its own server URL and
// API token in its encrypted credentials.
type Connector struct{}

// NewConnector creates the Seafile connector registered in the container.
func NewConnector() *Connector { return &Connector{} }

// Type returns the connector type key stored on data sources.
func (*Connector) Type() string { return types.ConnectorTypeSeafile }

// Validate checks the selection locally, pings Seafile with the token and,
// when resources are selected, confirms they belong to one existing,
// unencrypted library; it never lists directories.
func (*Connector) Validate(ctx context.Context, ds *types.DataSourceConfig) error {
	cfg, err := parseConfig(ds)
	if err != nil {
		return err
	}
	var repoID string
	if len(ds.ResourceIDs) > 0 {
		if repoID, _, err = collapseRoots(ds.ResourceIDs); err != nil {
			return err
		}
	}
	client := newClient(cfg)
	if err := client.ping(ctx); err != nil {
		return err
	}
	if repoID == "" {
		return nil
	}
	repos, err := client.listRepos(ctx)
	if err != nil {
		return err
	}
	_, err = selectedRepo(repos, repoID)
	return err
}

func selectedRepo(repos []repoInfo, repoID string) (repoInfo, error) {
	for _, repo := range repos {
		if repo.ID != repoID {
			continue
		}
		if repo.Encrypted {
			return repoInfo{}, fmt.Errorf("%w: encrypted Seafile libraries are not supported",
				datasource.ErrInvalidConfig)
		}
		return repo, nil
	}
	return repoInfo{}, fmt.Errorf("%w: Seafile library %s", datasource.ErrResourceNotFound, repoID)
}

// ListResources returns the unencrypted libraries for an empty parent and one
// directory level otherwise. Files with unsupported extensions are omitted so
// the picker only offers what a sync would import. Entries the sync would
// reject are skipped here rather than failing the whole level: the picker
// stays browsable and the sync reports the strict failure on its own.
func (*Connector) ListResources(
	ctx context.Context, ds *types.DataSourceConfig, parentID string,
) ([]types.Resource, error) {
	cfg, err := parseConfig(ds)
	if err != nil {
		return nil, err
	}
	client := newClient(cfg)
	var out []types.Resource
	if parentID == "" {
		out, err = listLibraries(ctx, client)
	} else {
		out, err = listDirectory(ctx, client, parentID)
	}
	if err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ExternalID < out[j].ExternalID
	})
	return out, nil
}

func listLibraries(ctx context.Context, client *client) ([]types.Resource, error) {
	repos, err := client.listRepos(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]types.Resource, 0, len(repos))
	for _, repo := range repos {
		if repo.Encrypted {
			continue
		}
		out = append(out, types.Resource{
			ExternalID:  encodeResourceID(repo.ID, "/"),
			Name:        repo.Name,
			Type:        "library",
			Description: repo.Type,
			HasChildren: true,
		})
	}
	return out, nil
}

func listDirectory(ctx context.Context, client *client, parentID string) ([]types.Resource, error) {
	repoID, dir, err := parseResourceID(parentID)
	if err != nil {
		return nil, err
	}
	entries, err := client.listDir(ctx, repoID, dir)
	if err != nil {
		return nil, mapDirError(err, dir)
	}
	out := make([]types.Resource, 0, len(entries))
	for _, e := range entries {
		if !validEntryName(e.Name) {
			continue
		}
		var kind string
		switch e.Type {
		case "dir":
			kind = "directory"
		case "file":
			if !utils.IsSupportedImportExtension(path.Ext(e.Name)) {
				continue
			}
			kind = "file"
		default:
			continue
		}
		out = append(out, types.Resource{
			ExternalID:  encodeResourceID(repoID, path.Join(dir, e.Name)),
			Name:        e.Name,
			Type:        kind,
			ParentID:    parentID,
			HasChildren: e.Type == "dir",
			ModifiedAt:  time.Unix(e.Mtime, 0).UTC(),
		})
	}
	return out, nil
}

// mapDirError classifies a directory listing failure: 404 means the resource
// is gone, 403 is a permission scoping problem rather than a bad token. The
// 403 message carries the reason code so the sync log names the cause the
// same way error items do.
func mapDirError(err error, p string) error {
	switch statusOf(err) {
	case http.StatusNotFound:
		return fmt.Errorf("%w: Seafile path %s", datasource.ErrResourceNotFound, p)
	case http.StatusForbidden:
		return fmt.Errorf("%w: %s: Seafile path %s", datasource.ErrFetchFailed, reasonPermissionDenied, p)
	default:
		return err
	}
}

// ResolveResourceAncestors derives ancestors from the IDs alone; no request
// is made and unparsable IDs contribute nothing.
func (*Connector) ResolveResourceAncestors(
	_ context.Context, _ *types.DataSourceConfig, ids []string,
) ([]string, error) {
	seen := make(map[string]bool)
	out := make([]string, 0)
	for _, id := range ids {
		for _, ancestor := range ancestorIDs(id) {
			if !seen[ancestor] {
				seen[ancestor] = true
				out = append(out, ancestor)
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

// collector adapts the streaming engine to the collecting Connector methods.
type collector struct{ items []types.FetchedItem }

func (h *collector) Emit(_ context.Context, item types.FetchedItem) error {
	h.items = append(h.items, item)
	return nil
}

func (*collector) Checkpoint(context.Context, *types.SyncCursor) error { return nil }

// FetchAll collects a fresh sync of the given resources without a cursor.
func (c *Connector) FetchAll(
	ctx context.Context, ds *types.DataSourceConfig, resourceIDs []string,
) ([]types.FetchedItem, error) {
	if ds == nil {
		return nil, datasource.ErrInvalidConfig
	}
	dsCopy := *ds
	dsCopy.ResourceIDs = resourceIDs
	items, _, err := c.FetchIncremental(ctx, &dsCopy, nil)
	return items, err
}

// FetchIncremental collects one streaming run into memory.
func (c *Connector) FetchIncremental(
	ctx context.Context, ds *types.DataSourceConfig, old *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	h := &collector{}
	next, err := c.FetchStream(ctx, ds, old, h)
	return h.items, next, err
}

// FetchStream is the production sync path: it scans the selected scope,
// emits changed files as they are downloaded and checkpoints the cursor so
// an interrupted run resumes instead of restarting.
func (c *Connector) FetchStream(
	ctx context.Context, ds *types.DataSourceConfig, old *types.SyncCursor, h datasource.StreamHandler,
) (*types.SyncCursor, error) {
	return c.runSync(ctx, ds, old, h, false)
}

// FetchFullStream re-downloads every file while keeping the old cursor as a
// deletion baseline until the run completes.
func (c *Connector) FetchFullStream(
	ctx context.Context, ds *types.DataSourceConfig, old *types.SyncCursor, h datasource.StreamHandler,
) (*types.SyncCursor, error) {
	return c.runSync(ctx, ds, old, h, true)
}
