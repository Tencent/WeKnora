package gitlab

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

var _ datasource.Connector = (*Connector)(nil)

type Connector struct {
	client        *client
	canonicalBase string
}

// NewConnector creates a stateless connector. Each data source provides its
// own GitLab URL and access token in its encrypted credentials.
func NewConnector() *Connector    { return &Connector{} }
func (c *Connector) Type() string { return types.ConnectorTypeGitLab }

func (c *Connector) configured(ds *types.DataSourceConfig) (*Connector, error) {
	if c.client != nil {
		return c, nil
	}
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
	var err error
	if c, err = c.configured(ds); err != nil {
		return err
	}
	// Connection testing deliberately validates only the GitLab URL and token.
	return c.client.ping(ctx)
}

func (c *Connector) directoryExists(ctx context.Context, projectID, ref, dir string) (bool, error) {
	entries, err := c.client.tree(ctx, projectID, ref, dir)
	if err != nil {
		return false, err
	}
	// Git does not store empty directories. A successful non-empty listing of
	// the target path is therefore a reliable directory existence check and
	// avoids deployments that treat path="." as an invalid root path.
	return len(entries) > 0, nil
}

func hasActiveMember(members []member, username string) bool {
	for _, m := range members {
		if m.Username == username && m.State == "active" {
			return true
		}
	}
	return false
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
}

func (c *Connector) FetchIncremental(ctx context.Context, ds *types.DataSourceConfig, old *types.SyncCursor) ([]types.FetchedItem, *types.SyncCursor, error) {
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
	next := cursor{Projects: map[string]string{}}
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
		} else if previous != head {
			diff, err := c.client.compare(ctx, s.ProjectID, previous, head)
			if err != nil || diff.CompareTimeout {
				// A compare may be unavailable after history rewrites or truncated by
				// the server. Re-enumerate the configured scope to preserve updates.
				files, listErr := c.files(ctx, s.ProjectID, ref, s.Paths)
				if listErr != nil {
					return nil, nil, fmt.Errorf("gitlab compare %s: %w", s.ProjectID, err)
				}
				for _, f := range files {
					item, itemErr := c.item(ctx, p, ref, f)
					if itemErr != nil {
						return nil, nil, itemErr
					}
					out = append(out, item)
				}
			} else {
				for _, d := range diff.Diffs {
					if d.DeletedFile {
						if c.inScope(d.OldPath, s.Paths) {
							out = append(out, c.deleted(p, ref, d.OldPath))
						}
						continue
					}
					if d.RenamedFile && c.inScope(d.OldPath, s.Paths) {
						out = append(out, c.deleted(p, ref, d.OldPath))
					}
					if c.inScope(d.NewPath, s.Paths) {
						item, err := c.item(ctx, p, ref, d.NewPath)
						if err != nil {
							return nil, nil, err
						}
						out = append(out, item)
					}
				}
			}
		}
		next.Projects[s.ProjectID] = head
	}
	raw, _ := json.Marshal(next)
	return out, &types.SyncCursor{LastSyncTime: time.Now().UTC(), ConnectorCursor: map[string]interface{}{"projects": next.Projects, "raw": string(raw)}}, nil
}
func (c *Connector) files(ctx context.Context, id, ref string, roots []string) ([]string, error) {
	if len(roots) == 0 {
		roots = []string{""}
	}
	seen := map[string]bool{}
	var out []string
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
			} else if e.Type == "blob" && !seen[e.Path] {
				seen[e.Path] = true
				out = append(out, e.Path)
			}
		}
		return nil
	}
	for _, r := range roots {
		if err := walk(r); err != nil {
			return nil, err
		}
	}
	return out, nil
}
func (c *Connector) item(ctx context.Context, p *project, ref, file string) (types.FetchedItem, error) {
	body, err := c.client.raw(ctx, fmt.Sprint(p.ID), ref, file)
	if err != nil {
		return types.FetchedItem{}, err
	}
	id := fmt.Sprintf("gitlab:%s:%d:%s:%s", c.canonicalBase, p.ID, ref, file)
	return types.FetchedItem{ExternalID: id, Title: p.PathWithNamespace + "/" + file, FileName: knowledgeRelativePath(p.Name, ref, file), Content: body, ContentType: "text/plain", UpdatedAt: time.Now().UTC(), SourceResourceID: fmt.Sprint(p.ID), Metadata: map[string]string{"channel": types.ConnectorTypeGitLab, "source_type": "gitlab", "gitlab_project_id": fmt.Sprint(p.ID), "gitlab_ref": ref, "gitlab_path": file, "gitlab_url": p.WebURL + "/-/blob/" + ref + "/" + file}}, nil
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
