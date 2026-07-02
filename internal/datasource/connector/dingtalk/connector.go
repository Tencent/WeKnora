package dingtalk

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	nodeTypeFile           = "FILE"
	nodeTypeFolder         = "FOLDER"
	nodeCategoryOnlineDoc  = "ALIDOC"
	resourceTypeWikiSpace  = "wiki_space"
	resourceTypeFolder     = "folder"
	resourceTypeDocument   = "document"
	markdownContentType    = "text/markdown"
	markdownFileNameSuffix = ".md"
)

// Compile-time proof that *Connector satisfies the datasource.Connector interface.
var _ datasource.Connector = (*Connector)(nil)

// Connector implements datasource.Connector for DingTalk.
type Connector struct{}

// NewConnector creates a new DingTalk connector.
func NewConnector() *Connector { return &Connector{} }

// Type returns the connector type identifier.
func (c *Connector) Type() string { return types.ConnectorTypeDingTalk }

// Validate verifies the given credentials by listing accessible workspaces.
func (c *Connector) Validate(ctx context.Context, config *types.DataSourceConfig) error {
	cfg, err := parseConfig(config)
	if err != nil {
		return err
	}
	if err := newClient(cfg).Ping(ctx); err != nil {
		return fmt.Errorf("dingtalk connection failed: %w", err)
	}
	return nil
}

// ListResources returns DingTalk wiki spaces or the direct child nodes under parentID.
// The picker is loaded lazily: root requests return workspaces, while non-root
// requests return only the next level under the selected workspace/node.
func (c *Connector) ListResources(
	ctx context.Context,
	config *types.DataSourceConfig,
	parentID string,
) ([]types.Resource, error) {
	cfg, err := parseConfig(config)
	if err != nil {
		return nil, err
	}
	cli := newClient(cfg)

	if parentID == "" {
		spaces, err := cli.ListWorkspaces(ctx)
		if err != nil {
			return nil, fmt.Errorf("list dingtalk workspaces: %w", err)
		}
		resources := make([]types.Resource, 0, len(spaces))
		for _, space := range spaces {
			resources = append(resources, workspaceToResource(space))
		}
		sortResourcesByName(resources)
		return resources, nil
	}

	workspaceID, nodeID := parseResourceID(parentID)
	if workspaceID == "" || nodeID == "" {
		return nil, fmt.Errorf("%w: invalid dingtalk resource id %q", datasource.ErrInvalidConfig, parentID)
	}
	nodes, err := cli.ListNodes(ctx, nodeID)
	if err != nil {
		return nil, fmt.Errorf("list dingtalk nodes under %s: %w", parentID, err)
	}
	resources := make([]types.Resource, 0, len(nodes))
	for _, n := range nodes {
		if n.WorkspaceID == "" {
			n.WorkspaceID = workspaceID
		}
		resources = append(resources, nodeToResource(parentID, n))
	}
	sortResourcesByName(resources)
	return resources, nil
}

// ResolveResourceAncestors has nothing to do for DingTalk selections currently.
func (c *Connector) ResolveResourceAncestors(
	ctx context.Context, config *types.DataSourceConfig, resourceIDs []string,
) ([]string, error) {
	return []string{}, nil
}

// FetchAll performs a full sync of all selected DingTalk resources.
func (c *Connector) FetchAll(ctx context.Context, config *types.DataSourceConfig, resourceIDs []string) ([]types.FetchedItem, error) {
	items, _, err := c.walk(ctx, config, resourceIDs, nil, false)
	return items, err
}

// FetchIncremental returns items changed or deleted since the prior cursor.
func (c *Connector) FetchIncremental(
	ctx context.Context,
	config *types.DataSourceConfig,
	cursor *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	resourceIDs := config.ResourceIDs
	if len(resourceIDs) == 0 {
		return nil, nil, fmt.Errorf("no resource IDs (workspace or node IDs) configured")
	}

	var prev *dingtalkCursor
	if cursor != nil && cursor.ConnectorCursor != nil {
		var p dingtalkCursor
		b, _ := json.Marshal(cursor.ConnectorCursor)
		_ = json.Unmarshal(b, &p)
		prev = &p
	}

	items, newCursor, err := c.walk(ctx, config, resourceIDs, prev, true)
	if err != nil {
		return nil, nil, err
	}

	cursorMap := make(map[string]interface{})
	b, _ := json.Marshal(newCursor)
	_ = json.Unmarshal(b, &cursorMap)

	return items, &types.SyncCursor{
		LastSyncTime:    newCursor.LastSyncTime,
		ConnectorCursor: cursorMap,
	}, nil
}

// walk is the shared implementation for FetchAll and FetchIncremental.
// If incremental is false, prev is ignored and no cursor is returned.
func (c *Connector) walk(
	ctx context.Context,
	config *types.DataSourceConfig,
	resourceIDs []string,
	prev *dingtalkCursor,
	incremental bool,
) ([]types.FetchedItem, *dingtalkCursor, error) {
	if len(resourceIDs) == 0 {
		return nil, nil, fmt.Errorf("no resource IDs (workspace or node IDs) configured")
	}
	cfg, err := parseConfig(config)
	if err != nil {
		return nil, nil, err
	}
	cli := newClient(cfg)

	newCursor := &dingtalkCursor{
		LastSyncTime:     time.Now(),
		ResourceNodeTime: make(map[string]map[string]string),
	}
	var out []types.FetchedItem

	for _, resourceID := range resourceIDs {
		workspaceID, nodeID := parseResourceID(resourceID)
		if workspaceID == "" || nodeID == "" {
			return nil, nil, fmt.Errorf("%w: invalid dingtalk resource id %q", datasource.ErrInvalidConfig, resourceID)
		}
		nodes, err := collectNodes(ctx, cli, workspaceID, nodeID)
		if err != nil {
			return nil, nil, fmt.Errorf("load dingtalk resource %s: %w", resourceID, err)
		}

		currentNodes := make(map[string]bool)
		newCursor.ResourceNodeTime[resourceID] = make(map[string]string)
		for _, n := range nodes {
			if n.NodeID == "" {
				continue
			}
			currentNodes[n.NodeID] = true
			newCursor.ResourceNodeTime[resourceID][n.NodeID] = n.ModifiedTime

			if incremental && prev != nil && prev.ResourceNodeTime != nil {
				if prevTimes, ok := prev.ResourceNodeTime[resourceID]; ok && prevTimes[n.NodeID] == n.ModifiedTime {
					continue
				}
			}

			item, err := c.fetchNodeContent(ctx, cli, workspaceID, resourceID, n)
			if err != nil {
				out = append(out, types.FetchedItem{
					ExternalID:       n.NodeID,
					Title:            resourceTitle(n.Name, n.NodeID),
					SourceResourceID: resourceID,
					Metadata: map[string]string{
						"error":        err.Error(),
						"channel":      types.ChannelDingtalk,
						"node_id":      n.NodeID,
						"workspace_id": workspaceID,
					},
				})
				continue
			}
			if item != nil {
				out = append(out, *item)
			}
		}

		if incremental && prev != nil && prev.ResourceNodeTime != nil {
			if prevTimes, ok := prev.ResourceNodeTime[resourceID]; ok {
				for prevNodeID := range prevTimes {
					if !currentNodes[prevNodeID] {
						out = append(out, types.FetchedItem{
							ExternalID:       prevNodeID,
							IsDeleted:        true,
							SourceResourceID: resourceID,
						})
					}
				}
			}
		}
	}

	if !incremental {
		return out, nil, nil
	}
	return out, newCursor, nil
}

// collectNodes returns the selected node and all descendants that may contain content.
func collectNodes(ctx context.Context, cli *client, workspaceID, nodeID string) ([]node, error) {
	root, err := cli.GetNode(ctx, nodeID)
	if err != nil {
		if _, blockErr := cli.QueryDocBlocks(ctx, nodeID); blockErr == nil {
			logger.Warnf(ctx, "[DingTalk] get node %s failed, treating it as a document after blocks query succeeded: %v", nodeID, err)
			return []node{{
				NodeID:      nodeID,
				WorkspaceID: workspaceID,
				Name:        nodeID,
				Type:        nodeTypeFile,
				Category:    nodeCategoryOnlineDoc,
			}}, nil
		} else {
			return nil, fmt.Errorf("get dingtalk node %s: %w (document blocks fallback failed: %v)", nodeID, err, blockErr)
		}
	}
	if root.WorkspaceID == "" {
		root.WorkspaceID = workspaceID
	}
	all := []node{root}
	if isFolderNode(root) {
		children, err := collectChildNodes(ctx, cli, workspaceID, root.NodeID)
		if err != nil {
			return nil, err
		}
		all = append(all, children...)
	}
	return all, nil
}

// collectChildNodes recursively walks child nodes under a folder.
func collectChildNodes(ctx context.Context, cli *client, workspaceID, parentNodeID string) ([]node, error) {
	children, err := cli.ListNodes(ctx, parentNodeID)
	if err != nil {
		return nil, err
	}
	all := make([]node, 0, len(children))
	for _, child := range children {
		if child.WorkspaceID == "" {
			child.WorkspaceID = workspaceID
		}
		all = append(all, child)
		if isFolderNode(child) {
			descendants, err := collectChildNodes(ctx, cli, workspaceID, child.NodeID)
			if err != nil {
				return nil, err
			}
			all = append(all, descendants...)
		}
	}
	return all, nil
}

// fetchNodeContent converts a supported online document into a fetched Markdown item.
func (c *Connector) fetchNodeContent(
	ctx context.Context,
	cli *client,
	workspaceID string,
	resourceID string,
	n node,
) (*types.FetchedItem, error) {
	if !isSupportedDocNode(n) {
		return nil, nil
	}
	blocks, err := cli.QueryDocBlocks(ctx, n.NodeID)
	if err != nil {
		return nil, fmt.Errorf("query doc blocks %s: %w", n.NodeID, err)
	}
	title := resourceTitle(n.Name, n.NodeID)
	content := renderBlocksMarkdown(title, blocks)
	return &types.FetchedItem{
		ExternalID:       n.NodeID,
		Title:            title,
		Content:          []byte(content),
		ContentType:      markdownContentType,
		FileName:         sanitizeFileName(title) + markdownFileNameSuffix,
		URL:              n.URL,
		UpdatedAt:        parseDingTalkTime(n.ModifiedTime),
		SourceResourceID: resourceID,
		Metadata: map[string]string{
			"channel":      types.ChannelDingtalk,
			"node_id":      n.NodeID,
			"workspace_id": workspaceID,
			"category":     n.Category,
			"extension":    n.Extension,
			"word_count":   strconv.FormatInt(n.Stats.WordCount, 10),
		},
	}, nil
}

func workspaceToResource(space workspace) types.Resource {
	name := resourceTitle(space.Name, space.WorkspaceID)
	return types.Resource{
		ExternalID:  makeResourceID(space.WorkspaceID, space.RootNodeID),
		Name:        name,
		Type:        resourceTypeWikiSpace,
		Description: space.Description,
		URL:         space.URL,
		ModifiedAt:  parseDingTalkTime(space.ModifiedTime),
		HasChildren: true,
		Metadata: map[string]interface{}{
			"workspace_id":    space.WorkspaceID,
			"root_node_id":    space.RootNodeID,
			"workspace_type":  space.Type,
			"permission_role": space.PermissionRole,
			"corp_id":         space.CorpID,
			"team_id":         space.TeamID,
		},
	}
}

func nodeToResource(parentID string, n node) types.Resource {
	workspaceID := n.WorkspaceID
	if workspaceID == "" {
		workspaceID, _ = parseResourceID(parentID)
	}
	resourceType := resourceTypeDocument
	if isFolderNode(n) {
		resourceType = resourceTypeFolder
	}
	return types.Resource{
		ExternalID:  makeResourceID(workspaceID, n.NodeID),
		Name:        resourceTitle(n.Name, n.NodeID),
		Type:        resourceType,
		URL:         n.URL,
		ModifiedAt:  parseDingTalkTime(n.ModifiedTime),
		ParentID:    parentID,
		HasChildren: isFolderNode(n),
		Metadata: map[string]interface{}{
			"node_id":         n.NodeID,
			"workspace_id":    workspaceID,
			"category":        n.Category,
			"extension":       n.Extension,
			"size":            n.Size,
			"word_count":      n.Stats.WordCount,
			"permission_role": n.PermissionRole,
		},
	}
}

func isSupportedDocNode(n node) bool {
	return n.Type == nodeTypeFile && n.Category == nodeCategoryOnlineDoc
}

func isFolderNode(n node) bool {
	return n.Type == nodeTypeFolder || n.HasChildren
}

func sortResourcesByName(resources []types.Resource) {
	sort.Slice(resources, func(i, j int) bool {
		return resources[i].Name < resources[j].Name
	})
}

func resourceTitle(name, fallback string) string {
	if name != "" {
		return name
	}
	if fallback != "" {
		return fallback
	}
	return "untitled"
}
