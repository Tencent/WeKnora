package dingtalk

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

var _ datasource.Connector = (*Connector)(nil)

type Connector struct{}

func NewConnector() *Connector    { return &Connector{} }
func (c *Connector) Type() string { return types.ConnectorTypeDingTalk }
func (c *Connector) api(ctx context.Context, config *types.DataSourceConfig) (*client, error) {
	cfg, err := parseConfig(config)
	if err != nil {
		return nil, err
	}
	cli := newClient(cfg)
	if err = cli.authenticate(ctx); err != nil {
		return nil, fmt.Errorf("dingtalk connection failed: %w", err)
	}
	return cli, nil
}
func (c *Connector) Validate(ctx context.Context, config *types.DataSourceConfig) error {
	cli, err := c.api(ctx, config)
	if err != nil {
		return err
	}
	_, err = cli.listWorkspaces(ctx)
	return err
}

func (c *Connector) ListResources(ctx context.Context, config *types.DataSourceConfig, parentID string) ([]types.Resource, error) {
	cli, err := c.api(ctx, config)
	if err != nil {
		return nil, err
	}
	if parentID == "" {
		ws, err := cli.listWorkspaces(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]types.Resource, 0, len(ws))
		for _, w := range ws {
			out = append(out, types.Resource{ExternalID: w.RootDentryUUID, Name: w.WorkspaceName, Type: "workspace", URL: w.URL, ModifiedAt: parseTime(w.CreateTime), HasChildren: true, Metadata: map[string]interface{}{"workspace_id": w.WorkspaceID}})
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
		return out, nil
	}
	nodes, err := cli.listNodes(ctx, parentID)
	if err != nil {
		return nil, err
	}
	out := make([]types.Resource, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, types.Resource{ExternalID: n.NodeID, Name: n.Name, Type: strings.ToLower(n.Type), URL: n.URL, ModifiedAt: parseTime(n.ModifiedTime), ParentID: parentID, HasChildren: n.HasChildren, Metadata: map[string]interface{}{"category": n.Category, "extension": n.Extension, "workspace_id": n.WorkspaceID}})
	}
	return out, nil
}

func (c *Connector) ResolveResourceAncestors(ctx context.Context, config *types.DataSourceConfig, resourceIDs []string) ([]string, error) {
	return []string{}, nil
}

func (c *Connector) walk(ctx context.Context, cli *client, root string) ([]node, error) {
	children, err := cli.listNodes(ctx, root)
	if err != nil {
		return nil, err
	}
	var out []node
	for _, n := range children {
		if n.HasChildren || strings.EqualFold(n.Type, "folder") {
			desc, err := c.walk(ctx, cli, n.NodeID)
			if err != nil {
				return nil, err
			}
			out = append(out, desc...)
			continue
		}
		out = append(out, n)
	}
	return out, nil
}
func safeName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		s = "untitled"
	}
	s = strings.NewReplacer("/", "_", "\\", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_").Replace(s)
	return filepath.Base(s)
}

func (c *Connector) FetchAll(ctx context.Context, config *types.DataSourceConfig, resourceIDs []string) ([]types.FetchedItem, error) {
	cli, err := c.api(ctx, config)
	if err != nil {
		return nil, err
	}
	return c.fetch(ctx, cli, resourceIDs, nil)
}
func (c *Connector) fetch(ctx context.Context, cli *client, roots []string, previous map[string]string) ([]types.FetchedItem, error) {
	var out []types.FetchedItem
	for _, root := range roots {
		nodes, err := c.walk(ctx, cli, root)
		if err != nil {
			return nil, fmt.Errorf("list nodes below %s: %w", root, err)
		}
		for _, n := range nodes {
			if previous != nil && previous[n.NodeID] == n.ModifiedTime {
				continue
			}
			content, err := cli.documentContent(ctx, n.NodeID)
			if err != nil {
				out = append(out, types.FetchedItem{ExternalID: n.NodeID, Title: n.Name, SourceResourceID: root, Metadata: map[string]string{"channel": types.ChannelDingtalk, "error": err.Error()}})
				continue
			}
			out = append(out, types.FetchedItem{ExternalID: n.NodeID, Title: n.Name, Content: content, ContentType: "text/markdown", FileName: safeName(n.Name) + ".md", URL: n.URL, UpdatedAt: parseTime(n.ModifiedTime), SourceResourceID: root, Metadata: map[string]string{"channel": types.ChannelDingtalk, "workspace_id": n.WorkspaceID, "category": n.Category, "extension": n.Extension}})
		}
	}
	return out, nil
}

func (c *Connector) FetchIncremental(ctx context.Context, config *types.DataSourceConfig, cursor *types.SyncCursor) ([]types.FetchedItem, *types.SyncCursor, error) {
	cli, err := c.api(ctx, config)
	if err != nil {
		return nil, nil, err
	}
	prev := map[string]string{}
	if cursor != nil && cursor.ConnectorCursor != nil {
		if raw, ok := cursor.ConnectorCursor["node_modified_times"]; ok {
			b, _ := json.Marshal(raw)
			_ = json.Unmarshal(b, &prev)
		}
	}
	items, err := c.fetch(ctx, cli, config.ResourceIDs, prev)
	if err != nil {
		return nil, nil, err
	}
	current := map[string]string{}
	for _, root := range config.ResourceIDs {
		nodes, e := c.walk(ctx, cli, root)
		if e != nil {
			return nil, nil, e
		}
		for _, n := range nodes {
			current[n.NodeID] = n.ModifiedTime
		}
	}
	for id := range prev {
		if _, ok := current[id]; !ok {
			items = append(items, types.FetchedItem{ExternalID: id, IsDeleted: true})
		}
	}
	return items, &types.SyncCursor{LastSyncTime: time.Now(), ConnectorCursor: map[string]interface{}{"node_modified_times": current}}, nil
}
