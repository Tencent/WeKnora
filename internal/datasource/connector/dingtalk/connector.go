package dingtalk

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// Compile-time proof that *Connector satisfies the datasource.Connector interface.
var _ datasource.Connector = (*Connector)(nil)

// Connector implements datasource.Connector for DingTalk.
type Connector struct{}

// NewConnector creates a new DingTalk connector.
func NewConnector() *Connector { return &Connector{} }

// Type returns the connector type identifier.
func (c *Connector) Type() string { return types.ConnectorTypeDingTalk }

// Validate verifies credentials.
func (c *Connector) Validate(ctx context.Context, config *types.DataSourceConfig) error {
	cfg, err := parseDingTalkConfig(config)
	if err != nil {
		return err
	}
	cli := newClient(cfg)
	if err := cli.Ping(ctx); err != nil {
		return fmt.Errorf("dingtalk connection failed: %w", err)
	}
	return nil
}

// ResolveResourceAncestors has nothing to do for DingTalk.
func (c *Connector) ResolveResourceAncestors(
	ctx context.Context, config *types.DataSourceConfig, resourceIDs []string,
) ([]string, error) {
	return []string{}, nil
}

// ListResources returns all workspaces.
func (c *Connector) ListResources(
	ctx context.Context, config *types.DataSourceConfig, parentID string,
) ([]types.Resource, error) {
	if parentID != "" {
		return []types.Resource{}, nil
	}

	cfg, err := parseDingTalkConfig(config)
	if err != nil {
		return nil, err
	}
	cli := newClient(cfg)

	workspaces, err := cli.ListWorkspaces(ctx)
	if err != nil {
		return nil, fmt.Errorf("list workspaces: %w", err)
	}

	out := make([]types.Resource, 0, len(workspaces))
	for _, w := range workspaces {
		out = append(out, types.Resource{
			ExternalID:  w.WorkspaceID,
			Name:        w.Name,
			Type:        "space",
			URL:         w.URL,
			Description: w.Description,
			ModifiedAt:  w.UpdateTime.Time(),
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].ExternalID < out[j].ExternalID })
	return out, nil
}

type dingtalkCursor struct {
	LastSyncTime       time.Time                    `json:"last_sync_time"`
	WorkspaceNodeTimes map[string]map[string]string `json:"workspace_node_times"`
}

func (c *Connector) FetchAll(ctx context.Context, config *types.DataSourceConfig, resourceIDs []string) ([]types.FetchedItem, error) {
	items, _, err := c.walk(ctx, config, resourceIDs, nil, false)
	return items, err
}

func (c *Connector) FetchIncremental(
	ctx context.Context, config *types.DataSourceConfig, cursor *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	var prev *dingtalkCursor
	if cursor != nil && len(cursor.ConnectorCursor) > 0 {
		data, err := json.Marshal(cursor.ConnectorCursor)
		if err == nil {
			var p dingtalkCursor
			if json.Unmarshal(data, &p) == nil {
				prev = &p
			}
		}
	}

	items, next, err := c.walk(ctx, config, config.ResourceIDs, prev, true)
	if err != nil {
		return nil, nil, err
	}

	var connCursor map[string]interface{}
	if next != nil {
		data, _ := json.Marshal(next)
		_ = json.Unmarshal(data, &connCursor)
	}

	return items, &types.SyncCursor{
		LastSyncTime:    time.Now(),
		ConnectorCursor: connCursor,
	}, nil
}

func (c *Connector) walkNodes(ctx context.Context, cli *client, workspaceID string, parentNodeID string) ([]Node, error) {
	var allNodes []Node
	nodes, err := cli.ListNodes(ctx, workspaceID, parentNodeID)
	if err != nil {
		return nil, err
	}

	for _, node := range nodes {
		allNodes = append(allNodes, node)
		if strings.ToLower(node.Type) == "folder" {
			subNodes, err := c.walkNodes(ctx, cli, workspaceID, node.NodeID)
			if err != nil {
				return nil, err
			}
			allNodes = append(allNodes, subNodes...)
		}
	}
	return allNodes, nil
}

func (c *Connector) walk(
	ctx context.Context,
	config *types.DataSourceConfig,
	resourceIDs []string,
	prev *dingtalkCursor,
	incremental bool,
) ([]types.FetchedItem, *dingtalkCursor, error) {
	cfg, err := parseDingTalkConfig(config)
	if err != nil {
		return nil, nil, err
	}
	cli := newClient(cfg)

	newCursor := &dingtalkCursor{
		LastSyncTime:       time.Now(),
		WorkspaceNodeTimes: make(map[string]map[string]string),
	}
	var out []types.FetchedItem

	for _, workspaceID := range resourceIDs {
		newCursor.WorkspaceNodeTimes[workspaceID] = make(map[string]string)
		currentNodes := make(map[string]bool)

		w, err := cli.GetWorkspace(ctx, workspaceID)
		if err != nil {
			return nil, nil, fmt.Errorf("get workspace %s details: %w", workspaceID, err)
		}

		nodes, err := c.walkNodes(ctx, cli, workspaceID, w.RootNodeID)
		if err != nil {
			return nil, nil, fmt.Errorf("list nodes for workspace %s: %w", workspaceID, err)
		}

		for _, node := range nodes {
			nodeType := strings.ToLower(node.Type)
			if nodeType != "doc" && nodeType != "file" {
				continue
			}

			// Skip DingTalk-native formats that cannot be converted to indexable text.
			if shouldSkip(node.Extension) {
				logger.Infof(ctx, "[DingTalk] skipping node %s (%s.%s): unsupported format",
					node.NodeID, node.Name, node.Extension)
				continue
			}

			updatedTimeStr := node.UpdateTime.Format(time.RFC3339)
			currentNodes[node.NodeID] = true
			newCursor.WorkspaceNodeTimes[workspaceID][node.NodeID] = updatedTimeStr

			if incremental && prev != nil && prev.WorkspaceNodeTimes != nil {
				if prevTimes, ok := prev.WorkspaceNodeTimes[workspaceID]; ok {
					if prevTimes[node.NodeID] == updatedTimeStr {
						continue
					}
				}
			}

			// DingTalk-native online docs use get_document_content (returns Markdown).
			// Uploaded files use download_file (returns raw bytes via signed URL).
			var (
				content  []byte
				fileName string
			)
			if isOnlineDoc(node.Extension) {
				content, err = cli.GetDocumentContent(ctx, node.NodeID)
				fileName = stripExtAndAddMD(node.Name, node.Extension)
			} else {
				content, err = cli.DownloadFileMCP(ctx, node.NodeID)
				fileName = buildFileName(node.Name, node.Extension)
			}
			if err != nil {
				out = append(out, types.FetchedItem{
					ExternalID:       node.NodeID,
					Title:            node.Name,
					SourceResourceID: workspaceID,
					Metadata: map[string]string{
						"error":   err.Error(),
						"channel": types.ConnectorTypeDingTalk,
						"node_id": node.NodeID,
					},
				})
				continue
			}

			out = append(out, types.FetchedItem{
				ExternalID:       node.NodeID,
				Title:            node.Name,
				Content:          content,
				FileName:         fileName,
				URL:              node.URL,
				UpdatedAt:        node.UpdateTime.Time(),
				SourceResourceID: workspaceID,
				Metadata: map[string]string{
					"node_id":      node.NodeID,
					"workspace_id": workspaceID,
					"channel":      types.ConnectorTypeDingTalk,
				},
			})
		}

		if incremental && prev != nil && prev.WorkspaceNodeTimes != nil {
			if prevTimes, ok := prev.WorkspaceNodeTimes[workspaceID]; ok {
				for prevNodeID := range prevTimes {
					if !currentNodes[prevNodeID] {
						out = append(out, types.FetchedItem{
							ExternalID:       prevNodeID,
							IsDeleted:        true,
							SourceResourceID: workspaceID,
						})
					}
				}
			}
		}
	}

	return out, newCursor, nil
}

// dingTalkTextDocExtensions are DingTalk-native formats that get_document_content
// can convert to Markdown. Only adoc is supported; the other native formats
// (axls/amind/able) require different MCP tools that don't produce plain text.
var dingTalkTextDocExtensions = map[string]bool{
	"adoc": true,
}

// dingTalkSkipExtensions are DingTalk-native formats that cannot be meaningfully
// indexed as text. They are skipped during sync rather than imported as errors.
var dingTalkSkipExtensions = map[string]bool{
	"axls":  true, // 钉钉表格 — needs get_spreadsheet_range
	"amind": true, // 钉钉脑图 — no text-export MCP tool
	"able":  true, // 钉钉多维表 — needs AI-table MCP tool
}

func isOnlineDoc(extension string) bool {
	return dingTalkTextDocExtensions[strings.ToLower(extension)]
}

func shouldSkip(extension string) bool {
	return dingTalkSkipExtensions[strings.ToLower(extension)]
}

// stripExtAndAddMD removes the DingTalk-native extension (e.g. ".adoc") from
// the node name if present, then appends ".md". This avoids double extensions
// like "文档.adoc.md" when DingTalk includes the extension in the node name.
func stripExtAndAddMD(name, extension string) string {
	if extension != "" {
		suffix := "." + strings.ToLower(extension)
		if strings.HasSuffix(strings.ToLower(name), suffix) {
			name = name[:len(name)-len(suffix)]
		}
	}
	return name + ".md"
}

func buildFileName(name, extension string) string {
	if extension == "" {
		return name
	}
	if strings.HasSuffix(strings.ToLower(name), "."+strings.ToLower(extension)) {
		return name
	}
	return name + "." + extension
}
