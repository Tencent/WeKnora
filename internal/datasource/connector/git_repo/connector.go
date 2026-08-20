package git_repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

var _ datasource.StreamingConnector = (*Connector)(nil)

// Connector syncs documents from git repositories (e.g. a VuePress blog). It
// clones each configured repo into a persistent worktree, tracks the branch tip
// commit as its incremental cursor, and emits per-file items. Relative image
// references in markdown are inlined as data URIs before ingestion so the
// standard image pipeline stores and serves them previewably.
type Connector struct {
	client *client
}

// NewConnector creates a stateless git_repo connector; per-data-source
// credentials and identity are injected at each call via configured().
func NewConnector() *Connector { return &Connector{} }

// Type returns the git_repo connector type identifier.
func (c *Connector) Type() string { return types.ConnectorTypeGitRepo }

func (c *Connector) configured(ds *types.DataSourceConfig) (*Connector, error) {
	if ds == nil {
		return nil, datasource.ErrInvalidConfig
	}
	token, _ := ds.Credentials["access_token"].(string)
	return &Connector{client: newClient(ds.ID, ds.TenantID, token)}, nil
}

// Validate checks that every configured repo is reachable (via a remote
// ref listing), after URL-level SSRF validation has passed in parseConfig.
func (c *Connector) Validate(ctx context.Context, ds *types.DataSourceConfig) error {
	conn, err := c.configured(ds)
	if err != nil {
		return err
	}
	// The stateless validate-credentials call carries only credentials (no
	// settings), so there is nothing repo-specific to check yet — mirroring the
	// GitLab connector's projects check. Full validation (each repo reachable)
	// runs at save/sync time via validateDataSourceConfig → Validate.
	if _, ok := ds.Settings["repos"]; !ok {
		return nil
	}
	cfg, err := parseConfig(ds)
	if err != nil {
		return err
	}
	for _, sel := range cfg.Repos {
		if _, _, err := conn.client.lsRemoteRefs(ctx, sel.RepoURL, sel.Branch); err != nil {
			return fmt.Errorf("git_repo: validate %s: %w", sel.RepoURL, err)
		}
	}
	return nil
}

// ListResources returns nil — git_repo is settings-driven (the repo list is
// edited in the datasource form) rather than resource-tree driven.
func (c *Connector) ListResources(context.Context, *types.DataSourceConfig, string) ([]types.Resource, error) {
	return nil, nil
}

// ResolveResourceAncestors is a no-op: git_repo has no resource tree.
func (c *Connector) ResolveResourceAncestors(context.Context, *types.DataSourceConfig, []string) ([]string, error) {
	return []string{}, nil
}

// FetchAll and FetchIncremental delegate to FetchStream through a buffering
// handler so the base-interface methods behave identically to the streaming
// production path.
func (c *Connector) FetchAll(ctx context.Context, ds *types.DataSourceConfig, _ []string) ([]types.FetchedItem, error) {
	var items []types.FetchedItem
	_, err := c.FetchStream(ctx, ds, nil, &bufferingHandler{emit: func(item types.FetchedItem) error {
		items = append(items, item)
		return nil
	}})
	return items, err
}

// FetchIncremental delegates to the streaming FetchStream path, buffering
// items for the base-interface (non-streaming) callers.
func (c *Connector) FetchIncremental(
	ctx context.Context, ds *types.DataSourceConfig, cursor *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	var items []types.FetchedItem
	next, err := c.FetchStream(ctx, ds, cursor, &bufferingHandler{emit: func(item types.FetchedItem) error {
		items = append(items, item)
		return nil
	}})
	return items, next, err
}

type repoPosition struct {
	Commit string `json:"commit"`
	Branch string `json:"branch"`
}

type cursor struct {
	Repos map[string]repoPosition `json:"repos"`
}

func gitRepoCursor(value cursor) *types.SyncCursor {
	raw, _ := json.Marshal(value)
	return &types.SyncCursor{
		LastSyncTime:    time.Now().UTC(),
		ConnectorCursor: map[string]interface{}{"repos": value.Repos, "raw": string(raw)},
	}
}

// FetchStream walks each configured repo from the previous cursor, emitting
// changed items (full stream on first sync, diff-based stream afterwards) and
// checkpointing after each repo so a timed-out sync resumes instead of
// restarting.
func (c *Connector) FetchStream(
	ctx context.Context, ds *types.DataSourceConfig, old *types.SyncCursor, h datasource.StreamHandler,
) (*types.SyncCursor, error) {
	var err error
	if c, err = c.configured(ds); err != nil {
		return nil, err
	}
	cfg, err := parseConfig(ds)
	if err != nil {
		return nil, err
	}

	prev := cursor{Repos: map[string]repoPosition{}}
	if old != nil {
		b, _ := json.Marshal(old.ConnectorCursor)
		_ = json.Unmarshal(b, &prev)
		if prev.Repos == nil {
			prev.Repos = map[string]repoPosition{}
		}
	}
	next := cursor{Repos: make(map[string]repoPosition, len(cfg.Repos))}
	for url, pos := range prev.Repos {
		next.Repos[url] = pos
	}

	for _, sel := range cfg.Repos {
		url := sel.RepoURL

		// Resolve the branch BEFORE touching disk so the clone directory is
		// stable across syncs (the config may leave branch empty and follow the
		// remote default). Empty config → previous cursor branch → ls-remote.
		resolvedBranch := sel.Branch
		if resolvedBranch == "" {
			if p, ok := prev.Repos[url]; ok && p.Branch != "" {
				resolvedBranch = p.Branch
			} else {
				_, rb, err := c.client.lsRemoteRefs(ctx, url, "")
				if err != nil {
					return nil, err
				}
				resolvedBranch = rb
			}
		}
		dir := c.client.cloneDirFor(url, resolvedBranch)

		// Two overlapping syncs of the same data source (cron + manual) would race
		// on the shared clone dir; serialize per data source.
		mu := repoDirMutex(ds.ID)
		mu.Lock()
		repo, headSHA, resolvedBranch, err := c.client.ensureCheckedOut(ctx, dir, url, resolvedBranch)
		mu.Unlock()
		if err != nil {
			return nil, err
		}

		previous := prev.Repos[url]
		switch {
		case previous.Commit == "":
			err = c.streamFiles(ctx, url, resolvedBranch, sel.Paths, h)
		case previous.Commit != headSHA:
			changes, diffErr := diffNameStatus(repo, previous.Commit, headSHA)
			if errors.Is(diffErr, errHistoryRewritten) {
				// Cursor commit no longer exists (force-push / history rewrite):
				// re-enumerate the configured scope to preserve updates.
				err = c.streamFiles(ctx, url, resolvedBranch, sel.Paths, h)
			} else if diffErr != nil {
				return nil, diffErr
			} else {
				err = c.streamChanges(ctx, url, resolvedBranch, sel.Paths, changes, h)
			}
		}
		if err != nil {
			return nil, err
		}

		next.Repos[url] = repoPosition{Commit: headSHA, Branch: resolvedBranch}
		checkpoint := gitRepoCursor(next)
		if err := h.Checkpoint(ctx, checkpoint); err != nil {
			return nil, err
		}
	}
	return gitRepoCursor(next), nil
}

// streamFiles emits every supported in-scope file of the worktree.
func (c *Connector) streamFiles(
	ctx context.Context, url, branch string, roots []string, h datasource.StreamHandler,
) error {
	dir := c.client.cloneDirFor(url, branch)
	return walkFiles(dir, roots, func(rel string) error {
		item, err := c.item(ctx, url, branch, rel)
		if err != nil {
			return err
		}
		return h.Emit(ctx, item)
	})
}

// streamChanges emits deletions for removed/renamed files and items for
// added/modified files, mirroring the GitLab connector's diff streaming.
func (c *Connector) streamChanges(
	ctx context.Context, url, branch string, roots []string, changes []nameStatusChange, h datasource.StreamHandler,
) error {
	for _, ch := range changes {
		if ch.Deleted {
			if inScope(ch.OldPath, roots) && isSupportedFile(ch.OldPath) {
				if err := h.Emit(ctx, c.deleted(url, branch, ch.OldPath)); err != nil {
					return err
				}
			}
			continue
		}
		if ch.Renamed && inScope(ch.OldPath, roots) && isSupportedFile(ch.OldPath) {
			if err := h.Emit(ctx, c.deleted(url, branch, ch.OldPath)); err != nil {
				return err
			}
		}
		if inScope(ch.NewPath, roots) && isSupportedFile(ch.NewPath) {
			item, err := c.item(ctx, url, branch, ch.NewPath)
			if err != nil {
				return err
			}
			if err := h.Emit(ctx, item); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *Connector) item(_ context.Context, url, branch, file string) (types.FetchedItem, error) {
	dir := c.client.cloneDirFor(url, branch)
	data, err := readFile(dir, file)
	if err != nil {
		return types.FetchedItem{}, fmt.Errorf("git_repo: read %s/%s: %w", url, file, err)
	}
	if isMarkdownish(file) || isHTMLish(file) {
		data, _ = InlineRelativeImages(dir, file, data)
	}
	id := fmt.Sprintf("gitrepo:%s:%s:%s", url, branch, file)
	repoName := repoDisplayName(url)
	return types.FetchedItem{
		ExternalID:       id,
		Title:            repoName + "/" + file,
		FileName:         knowledgeRelativePath(repoName, branch, file),
		Content:          data,
		ContentType:      "text/plain",
		UpdatedAt:        time.Now().UTC(),
		SourceResourceID: url,
		Metadata: map[string]string{
			"channel":         types.ConnectorTypeGitRepo,
			"source_type":     "git_repo",
			"git_repo_url":    url,
			"git_repo_branch": branch,
			"git_repo_path":   file,
		},
	}, nil
}

// deleted builds the IsDeleted item for a removed repository file. The sync
// service performs real KB deletion for IsDeleted items when the data source
// has SyncDeletions enabled, scoped to this data source via ExternalID.
func (c *Connector) deleted(url, branch, file string) types.FetchedItem {
	return types.FetchedItem{
		ExternalID: fmt.Sprintf("gitrepo:%s:%s:%s", url, branch, file),
		IsDeleted:  true,
		Metadata: map[string]string{
			"channel":         types.ConnectorTypeGitRepo,
			"git_repo_url":    url,
			"git_repo_branch": branch,
			"git_repo_path":   file,
		},
	}
}

// repoDisplayName derives a stable, human-friendly repo name from its URL.
func repoDisplayName(url string) string {
	return strings.TrimSuffix(path.Base(url), ".git")
}

// knowledgeRelativePath maps a repository file to the KB folder convention:
// <repo name>-<branch>/<repository-relative file path>.
func knowledgeRelativePath(repoName, branch, file string) string {
	root := strings.TrimSpace(repoName) + "-" + strings.ReplaceAll(strings.TrimSpace(branch), "/", "-")
	return path.Join(root, file)
}

// repoDirMutexes serializes clone-dir access per data source. The connector is
// a registry singleton shared across all data sources; keying by ds.ID keeps
// concurrent syncs of different data sources parallel.
var repoDirMutexes = struct {
	sync.Mutex
	m map[string]*sync.Mutex
}{m: map[string]*sync.Mutex{}}

func repoDirMutex(dsID string) *sync.Mutex {
	repoDirMutexes.Lock()
	defer repoDirMutexes.Unlock()
	if mu, ok := repoDirMutexes.m[dsID]; ok {
		return mu
	}
	mu := &sync.Mutex{}
	repoDirMutexes.m[dsID] = mu
	return mu
}

// bufferingHandler adapts the streaming emit/checkpoint interface to plain
// slice collection for the FetchAll/FetchIncremental compatibility methods.
type bufferingHandler struct {
	emit func(item types.FetchedItem) error
}

func (h *bufferingHandler) Emit(_ context.Context, item types.FetchedItem) error {
	return h.emit(item)
}

func (h *bufferingHandler) Checkpoint(context.Context, *types.SyncCursor) error {
	return nil
}
