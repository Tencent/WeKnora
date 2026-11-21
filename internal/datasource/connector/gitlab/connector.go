package gitlab

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

var _ datasource.StreamingConnector = (*Connector)(nil)

type Connector struct {
	client        *client
	canonicalBase string
}

// NewConnector creates a stateless connector. Each data source provides its
// own GitLab URL and access token in its encrypted credentials.
func NewConnector() *Connector    { return &Connector{} }
func (c *Connector) Type() string { return types.ConnectorTypeGitLab }

func (c *Connector) configured(ds *types.DataSourceConfig) (*Connector, error) {
	if ds == nil {
		return nil, datasource.ErrInvalidConfig
	}
	baseURL, _ := ds.Credentials["base_url"].(string)
	token, _ := ds.Credentials["access_token"].(string)
	client, err := newClient(baseURL, token)
	if err != nil {
		return nil, err
	}
	return &Connector{client: client, canonicalBase: client.baseURL}, nil
}

func (c *Connector) Validate(ctx context.Context, ds *types.DataSourceConfig) error {
	configured, err := c.configured(ds)
	if err != nil {
		return err
	}
	if err := configured.client.ping(ctx); err != nil {
		return err
	}
	if ds != nil {
		if _, ok := ds.Settings["projects"]; ok {
			if _, err := parseConfig(ds); err != nil {
				return err
			}
		}
	}
	return nil
}
func (c *Connector) ListResources(ctx context.Context, ds *types.DataSourceConfig, parent string) ([]types.Resource, error) {
	var err error
	if c, err = c.configured(ds); err != nil {
		return nil, err
	}
	if _, err := parseConfig(ds); err != nil {
		return nil, err
	}
	if parent == "" {
		ps, err := c.client.projects(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]types.Resource, 0, len(ps))
		for _, p := range ps {
			out = append(out, types.Resource{ExternalID: fmt.Sprint(p.ID), Name: p.PathWithNamespace, Type: "project", URL: p.WebURL, HasChildren: true})
		}
		return out, nil
	}
	id, dir := splitResourceID(parent)
	p, err := c.client.project(ctx, id)
	if err != nil {
		return nil, err
	}
	ref := p.DefaultBranch
	entries, err := c.client.tree(ctx, id, ref, dir)
	if err != nil {
		return nil, err
	}
	out := make([]types.Resource, 0, len(entries))
	for _, e := range entries {
		if e.Type == "tree" {
			out = append(out, types.Resource{ExternalID: id + ":" + e.Path, Name: e.Name, Type: "directory", ParentID: parent, HasChildren: true})
		}
	}
	return out, nil
}
func (c *Connector) ResolveResourceAncestors(context.Context, *types.DataSourceConfig, []string) ([]string, error) {
	return []string{}, nil
}
func (c *Connector) FetchAll(ctx context.Context, ds *types.DataSourceConfig, _ []string) ([]types.FetchedItem, error) {
	var err error
	if c, err = c.configured(ds); err != nil {
		return nil, err
	}
	cfg, err := parseConfig(ds)
	if err != nil {
		return nil, err
	}
	var out []types.FetchedItem
	for _, s := range cfg.Projects {
		p, err := c.client.project(ctx, s.ProjectID)
		if err != nil {
			return nil, err
		}
		ref := s.Ref
		if ref == "" {
			ref = p.DefaultBranch
		}
		files, err := c.files(ctx, s.ProjectID, ref, s.Paths)
		if err != nil {
			return nil, err
		}
		for _, file := range files {
			item, err := c.item(ctx, p, ref, file)
			if err != nil {
				return nil, err
			}
			out = append(out, item)
		}
	}
	return out, nil
}

type cursor struct {
	Projects map[string]string `json:"projects"`
	// Paths records the directory selection per project at the time the
	// cursor was written. It lets an incremental sync tell a scope
	// expansion apart from an unchanged selection when the project HEAD
	// has not moved: files under newly added paths must be imported even
	// though no commit changed. Absent for cursors written before this
	// field existed; those are treated as unknown scope.
	Paths map[string][]string `json:"paths"`
}

// addedPaths returns the roots of current that were not covered by the
// previously recorded selection. A current root is covered when it equals
// a recorded root or lies underneath one. When the previous selection is
// unknown (legacy cursor without recorded paths), the whole current
// selection is returned so the next sync re-imports it once; re-imported
// items upsert by ExternalID, they do not duplicate.
func addedPaths(prev []string, recorded bool, current []string) []string {
	if !recorded {
		return current
	}
	if len(prev) == 0 {
		// Whole repository was selected before: everything is covered.
		return nil
	}
	if len(current) == 0 {
		// Selection widened to the whole repository.
		return []string{""}
	}
	var added []string
	for _, p := range current {
		covered := false
		for _, r := range prev {
			if p == r || strings.HasPrefix(p, r+"/") {
				covered = true
				break
			}
		}
		if !covered {
			added = append(added, p)
		}
	}
	return added
}

// pathCoveredByRoots reports whether file falls under one of roots, where an
// empty root ("") represents the whole repository.
func pathCoveredByRoots(file string, roots []string) bool {
	for _, r := range roots {
		if r == "" || file == r || strings.HasPrefix(file, r+"/") {
			return true
		}
	}
	return false
}

// FetchIncremental returns changed files since the previous sync cursor and
// imports any newly selected directory roots for the configured projects.
func (c *Connector) FetchIncremental(
	ctx context.Context, ds *types.DataSourceConfig, old *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	var err error
	if c, err = c.configured(ds); err != nil {
		return nil, nil, err
	}
	cfg, err := parseConfig(ds)
	if err != nil {
		return nil, nil, err
	}
	prev := cursor{Projects: map[string]string{}}
	if old != nil {
		b, _ := json.Marshal(old.ConnectorCursor)
		_ = json.Unmarshal(b, &prev)
		if prev.Projects == nil {
			prev.Projects = map[string]string{}
		}
	}
	var out []types.FetchedItem
	next := cursor{
		Projects: map[string]string{},
		Paths:    map[string][]string{},
	}
	for _, s := range cfg.Projects {
		p, err := c.client.project(ctx, s.ProjectID)
		if err != nil {
			return nil, nil, err
		}
		ref := s.Ref
		if ref == "" {
			ref = p.DefaultBranch
		}
		head, err := c.client.commitSHA(ctx, s.ProjectID, ref)
		if err != nil {
			return nil, nil, err
		}
		previous := prev.Projects[s.ProjectID]
		if previous == "" {
			files, err := c.files(ctx, s.ProjectID, ref, s.Paths)
			if err != nil {
				return nil, nil, err
			}
			for _, f := range files {
				item, err := c.item(ctx, p, ref, f)
				if err != nil {
					return nil, nil, err
				}
				out = append(out, item)
			}
		} else {
			prevPaths, recorded := prev.Paths[s.ProjectID]
			added := addedPaths(prevPaths, recorded, s.Paths)
			if previous != head {
				diff, err := c.compareChanges(ctx, s.ProjectID, previous, head)
				if err != nil {
					return nil, nil, err
				}
				for _, d := range diff.Diffs {
					if d.DeletedFile {
						if c.inScope(d.OldPath, s.Paths) && isSupportedFile(d.OldPath) {
							out = append(out, c.deleted(p, ref, d.OldPath))
						}
						continue
					}
					if d.RenamedFile && c.inScope(d.OldPath, s.Paths) && isSupportedFile(d.OldPath) {
						out = append(out, c.deleted(p, ref, d.OldPath))
					}
					if c.inScope(d.NewPath, s.Paths) && isSupportedFile(d.NewPath) &&
						!pathCoveredByRoots(d.NewPath, added) {
						item, err := c.item(ctx, p, ref, d.NewPath)
						if err != nil {
							return nil, nil, err
						}
						out = append(out, item)
					}
				}
			}
			if len(added) > 0 {
				files, err := c.files(ctx, s.ProjectID, ref, added)
				if err != nil {
					return nil, nil, err
				}
				for _, f := range files {
					item, err := c.item(ctx, p, ref, f)
					if err != nil {
						return nil, nil, err
					}
					out = append(out, item)
				}
			}
		}
		next.Projects[s.ProjectID] = head
		next.Paths[s.ProjectID] = s.Paths
	}
	return out, gitLabCursor(next), nil
}

// FetchStream is the production sync path. It emits each supported repository
// file as soon as it is read, so large GitLab projects do not accumulate their
// contents in memory. The service prefers this method for StreamingConnector
// implementations; FetchAll and FetchIncremental remain compatibility methods
// required by the base Connector interface.
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
	prev := cursor{Projects: map[string]string{}}
	if old != nil {
		b, _ := json.Marshal(old.ConnectorCursor)
		_ = json.Unmarshal(b, &prev)
		if prev.Projects == nil {
			prev.Projects = map[string]string{}
		}
	}
	next := cursor{
		Projects: make(map[string]string, len(cfg.Projects)),
		Paths:    make(map[string][]string, len(cfg.Projects)),
	}
	for projectID, commit := range prev.Projects {
		next.Projects[projectID] = commit
	}
	for projectID, paths := range prev.Paths {
		next.Paths[projectID] = paths
	}

	for _, selection := range cfg.Projects {
		project, err := c.client.project(ctx, selection.ProjectID)
		if err != nil {
			return nil, err
		}
		ref := selection.Ref
		if ref == "" {
			ref = project.DefaultBranch
		}
		head, err := c.client.commitSHA(ctx, selection.ProjectID, ref)
		if err != nil {
			return nil, err
		}

		previous := prev.Projects[selection.ProjectID]
		if previous == "" {
			err = c.streamFiles(ctx, project, ref, selection.Paths, h)
		} else {
			prevPaths, recorded := prev.Paths[selection.ProjectID]
			added := addedPaths(prevPaths, recorded, selection.Paths)
			if previous != head {
				err = c.streamChanges(ctx, project, ref, previous, head, selection.Paths, added, h)
			}
			if err == nil && len(added) > 0 {
				err = c.streamFiles(ctx, project, ref, added, h)
			}
		}
		if err != nil {
			return nil, err
		}

		next.Projects[selection.ProjectID] = head
		next.Paths[selection.ProjectID] = selection.Paths
		checkpoint := gitLabCursor(next)
		if err := h.Checkpoint(ctx, checkpoint); err != nil {
			return nil, err
		}
	}
	return gitLabCursor(next), nil
}

func gitLabCursor(value cursor) *types.SyncCursor {
	raw, _ := json.Marshal(value)
	return &types.SyncCursor{
		LastSyncTime: time.Now().UTC(),
		ConnectorCursor: map[string]interface{}{
			"projects": value.Projects,
			"paths":    value.Paths,
			"raw":      string(raw),
		},
	}
}

func (c *Connector) streamChanges(
	ctx context.Context, project *project, ref, from, to string,
	roots, skipRoots []string, h datasource.StreamHandler,
) error {
	diff, err := c.compareChanges(ctx, fmt.Sprint(project.ID), from, to)
	if err != nil {
		return err
	}
	for _, change := range diff.Diffs {
		if change.DeletedFile {
			if c.inScope(change.OldPath, roots) && isSupportedFile(change.OldPath) {
				if err := h.Emit(ctx, c.deleted(project, ref, change.OldPath)); err != nil {
					return err
				}
			}
			continue
		}
		if change.RenamedFile && c.inScope(change.OldPath, roots) && isSupportedFile(change.OldPath) {
			if err := h.Emit(ctx, c.deleted(project, ref, change.OldPath)); err != nil {
				return err
			}
		}
		if c.inScope(change.NewPath, roots) && isSupportedFile(change.NewPath) &&
			!pathCoveredByRoots(change.NewPath, skipRoots) {
			item, err := c.item(ctx, project, ref, change.NewPath)
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

func (c *Connector) compareChanges(ctx context.Context, projectID, from, to string) (*comparison, error) {
	diff, err := c.client.compare(ctx, projectID, from, to)
	if err != nil {
		return nil, fmt.Errorf("gitlab compare project %s: %w", projectID, err)
	}
	if diff.CompareTimeout {
		return nil, fmt.Errorf("gitlab compare project %s timed out", projectID)
	}
	return diff, nil
}

func (c *Connector) streamFiles(
	ctx context.Context, project *project, ref string, roots []string, h datasource.StreamHandler,
) error {
	return c.walkFiles(ctx, fmt.Sprint(project.ID), ref, roots, func(file string) error {
		item, err := c.item(ctx, project, ref, file)
		if err != nil {
			return err
		}
		return h.Emit(ctx, item)
	})
}

func (c *Connector) files(ctx context.Context, id, ref string, roots []string) ([]string, error) {
	var out []string
	err := c.walkFiles(ctx, id, ref, roots, func(file string) error {
		out = append(out, file)
		return nil
	})
	return out, err
}

// walkFiles visits supported blobs while traversing the selected directories.
// It deliberately does not collect paths, keeping streaming sync memory bounded
// by the traversal depth plus the current file body.
func (c *Connector) walkFiles(ctx context.Context, id, ref string, roots []string, visit func(string) error) error {
	if len(roots) == 0 {
		roots = []string{""}
	}
	var walk func(string) error
	walk = func(dir string) error {
		entries, err := c.client.tree(ctx, id, ref, dir)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if e.Type == "tree" {
				if err := walk(e.Path); err != nil {
					return err
				}
			} else if e.Type == "blob" && isSupportedFile(e.Path) {
				if err := visit(e.Path); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for _, r := range roots {
		if err := walk(r); err != nil {
			return err
		}
	}
	return nil
}

func isSupportedFile(file string) bool {
	_, ok := gitLabSupportedFileExtensions[strings.ToLower(path.Ext(file))]
	return ok
}

// gitLabSupportedFileExtensions limits repository sync to formats the current
// knowledge import pipeline can process. This is intentionally connector-local:
// GitLab exposes arbitrary repository blobs, unlike document-centric sources.
var gitLabSupportedFileExtensions = map[string]struct{}{
	".pdf": {}, ".txt": {}, ".docx": {}, ".doc": {}, ".epub": {},
	".html": {}, ".htm": {}, ".mhtml": {}, ".md": {}, ".markdown": {}, ".mdx": {},
	".png": {}, ".jpg": {}, ".jpeg": {}, ".gif": {},
	".csv": {}, ".xlsx": {}, ".xls": {}, ".pptx": {}, ".ppt": {}, ".json": {},
	".mp3": {}, ".wav": {}, ".m4a": {}, ".flac": {}, ".ogg": {},
}

func (c *Connector) item(ctx context.Context, p *project, ref, file string) (types.FetchedItem, error) {
	body, err := c.client.raw(ctx, fmt.Sprint(p.ID), ref, file)
	if err != nil {
		return types.FetchedItem{}, err
	}
	id := fmt.Sprintf("gitlab:%s:%d:%s:%s", c.canonicalBase, p.ID, ref, file)
	title := p.PathWithNamespace + "/" + file
	// A file emptied by a commit still exists at this path, and ingestion reads
	// an item with no content and no URL as "nothing to sync" (see
	// datasource_service applyFetchedItem): the sync is acknowledged as
	// successful while the body indexed from an earlier commit stays fully
	// searchable. Nothing revisits it afterwards either — a full sync sees the
	// file present in the tree, and an incremental sync only walks the diffs of
	// commits newer than the recorded head, which the emptying commit already
	// moved past.
	//
	// Emitting the file's path as its content replaces the stale body instead,
	// which is what the Notion (b9be7d0d), Confluence and IMA (b71b31c8)
	// connectors already do for the same case. The check is on length alone, not
	// on TrimSpace: a blob that holds only whitespace still carries content the
	// repository has, and only a zero-byte blob has none.
	if len(body) == 0 {
		logger.Infof(ctx, "[GitLab] %s is empty, syncing its path only", title)
		body = []byte("# " + title + "\n")
	}
	// UpdatedAt is intentionally left unset: the file's last commit time is not
	// fetched here, and a fetch timestamp would be a fabricated source time.
	return types.FetchedItem{
		ExternalID:       id,
		Title:            title,
		FileName:         knowledgeRelativePath(p.Name, ref, file),
		Content:          body,
		ContentType:      "text/plain",
		SourceResourceID: fmt.Sprint(p.ID),
		Metadata: map[string]string{
			"channel":           types.ConnectorTypeGitLab,
			"source_type":       "gitlab",
			"gitlab_project_id": fmt.Sprint(p.ID),
			"gitlab_ref":        ref,
			"gitlab_path":       file,
			"gitlab_url":        p.WebURL + "/-/blob/" + ref + "/" + file,
		},
	}, nil
}

// knowledgeRelativePath maps a repository file to the KB folder convention:
// <GitLab project name>-<branch>/<repository-relative file path>.
func knowledgeRelativePath(projectName, ref, file string) string {
	root := strings.TrimSpace(projectName) + "-" + strings.ReplaceAll(strings.TrimSpace(ref), "/", "-")
	return path.Join(root, file)
}
func (c *Connector) deleted(p *project, ref, file string) types.FetchedItem {
	return types.FetchedItem{ExternalID: fmt.Sprintf("gitlab:%s:%d:%s:%s", c.canonicalBase, p.ID, ref, file), IsDeleted: true, Metadata: map[string]string{"channel": types.ConnectorTypeGitLab, "gitlab_path": file}}
}
func (c *Connector) inScope(file string, roots []string) bool {
	if len(roots) == 0 {
		return true
	}
	for _, r := range roots {
		if file == r || strings.HasPrefix(file, r+"/") {
			return true
		}
	}
	return false
}
func splitResourceID(value string) (string, string) {
	parts := strings.SplitN(value, ":", 2)
	if len(parts) == 1 {
		return value, ""
	}
	return parts[0], parts[1]
}
