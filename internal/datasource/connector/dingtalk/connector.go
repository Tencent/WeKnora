package dingtalk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/types"
)

// Connector implements the datasource.Connector interface for DingTalk.
type Connector struct{}

// NewConnector creates a new DingTalk connector.
func NewConnector() *Connector {
	return &Connector{}
}

// Type returns the connector type identifier.
func (c *Connector) Type() string {
	return types.ConnectorTypeDingTalk
}

const nodeResourceSeparator = ":"

// Validate verifies that the DingTalk configuration is valid by testing
// connectivity, operator resolution, and the wiki read scope.
func (c *Connector) Validate(ctx context.Context, config *types.DataSourceConfig) error {
	dingConfig, err := parseDingTalkConfig(config)
	if err != nil {
		return err
	}

	client := NewClient(dingConfig)
	if err := client.Ping(ctx); err != nil {
		return fmt.Errorf("dingtalk connection failed: %w", err)
	}
	// Listing workspaces exercises both operator resolution (qyapi_get_member
	// unless operator_id is configured) and the Wiki.Workspace.Read scope, so
	// scope problems surface at configuration time instead of first sync.
	if _, err := client.ListWorkspaces(ctx); err != nil {
		return fmt.Errorf("dingtalk workspace access failed: %w", err)
	}
	return nil
}

// ListResources lists DingTalk knowledge base resources for selection,
// loading the tree lazily one level at a time.
//
//   - parentID == ""                     → list all accessible knowledge bases.
//   - parentID == workspaceID            → list the top-level nodes of that base.
//   - parentID == "workspaceID:nodeID"   → list the direct children of that node.
func (c *Connector) ListResources(
	ctx context.Context, config *types.DataSourceConfig, parentID string,
) ([]types.Resource, error) {
	dingConfig, err := parseDingTalkConfig(config)
	if err != nil {
		return nil, err
	}

	client := NewClient(dingConfig)

	if parentID == "" {
		workspaces, err := client.ListWorkspaces(ctx)
		if err != nil {
			return nil, fmt.Errorf("list dingtalk workspaces: %w", err)
		}

		resources := make([]types.Resource, 0, len(workspaces))
		for _, ws := range workspaces {
			resources = append(resources, types.Resource{
				ExternalID:  ws.WorkspaceID,
				Name:        ws.Name,
				Type:        "workspace",
				Description: ws.Description,
				URL:         ws.URL,
				HasChildren: true,
				Metadata: map[string]interface{}{
					"workspace_id": ws.WorkspaceID,
					"root_node_id": ws.RootNodeID,
				},
			})
		}
		return resources, nil
	}

	// Lazy load: list only the direct children of the given workspace / node.
	workspaceID, nodeID := parseNodeResourceID(parentID)
	if nodeID == "" {
		ws, err := client.GetWorkspace(ctx, workspaceID)
		if err != nil {
			return nil, fmt.Errorf("get dingtalk workspace %s: %w", workspaceID, err)
		}
		nodeID = ws.RootNodeID
	}

	nodes, err := client.ListNodes(ctx, nodeID)
	if err != nil {
		return nil, fmt.Errorf("list dingtalk nodes under %s: %w", parentID, err)
	}

	resources := make([]types.Resource, 0, len(nodes))
	for _, node := range nodes {
		resources = append(resources, nodeToResource(workspaceID, parentID, node))
	}
	return resources, nil
}

// ResolveResourceAncestors returns an empty set: the DingTalk wiki node API
// does not expose a parent pointer, so a lazily-loaded picker cannot reveal
// pre-existing deep selections without re-walking the tree. Selections still
// sync correctly; they just start collapsed in the picker.
func (c *Connector) ResolveResourceAncestors(
	ctx context.Context, config *types.DataSourceConfig, resourceIDs []string,
) ([]string, error) {
	return []string{}, nil
}

// FetchAll performs a full sync of all documents from the specified resources.
func (c *Connector) FetchAll(ctx context.Context, config *types.DataSourceConfig, resourceIDs []string) ([]types.FetchedItem, error) {
	dingConfig, err := parseDingTalkConfig(config)
	if err != nil {
		return nil, err
	}

	client := NewClient(dingConfig)

	var allItems []types.FetchedItem
	for _, resourceID := range resourceIDs {
		nodes, err := c.listResourceNodes(ctx, client, resourceID)
		if err != nil {
			var partialErr *partialNodeListError
			if !errors.As(err, &partialErr) {
				return nil, fmt.Errorf("list nodes for resource %s: %w", resourceID, err)
			}
			allItems = appendNodeListFailureItems(allItems, resourceID, partialErr.Failures)
		}

		for _, node := range nodes {
			item, err := c.fetchNodeContent(ctx, client, node, resourceID)
			if err != nil {
				// Record the failure but continue with other nodes.
				allItems = append(allItems, types.FetchedItem{
					ExternalID:       node.NodeID,
					Title:            node.Name,
					SourceResourceID: resourceID,
					Metadata: map[string]string{
						"error": err.Error(),
					},
				})
				continue
			}
			if item != nil {
				allItems = append(allItems, *item)
			}
		}
	}

	return allItems, nil
}

// FetchIncremental performs an incremental sync by comparing node modified
// timestamps against the previously recorded state.
func (c *Connector) FetchIncremental(ctx context.Context, config *types.DataSourceConfig, cursor *types.SyncCursor) ([]types.FetchedItem, *types.SyncCursor, error) {
	dingConfig, err := parseDingTalkConfig(config)
	if err != nil {
		return nil, nil, err
	}

	client := NewClient(dingConfig)

	var prevCursor dingtalkCursor
	if cursor != nil && cursor.ConnectorCursor != nil {
		cursorBytes, _ := json.Marshal(cursor.ConnectorCursor)
		_ = json.Unmarshal(cursorBytes, &prevCursor)
	}

	newCursor := dingtalkCursor{
		LastSyncTime:      time.Now(),
		ResourceNodeTimes: make(map[string]map[string]string),
	}

	resourceIDs := config.ResourceIDs
	if len(resourceIDs) == 0 {
		return nil, nil, fmt.Errorf("no resource IDs (workspace IDs or node IDs) configured")
	}

	var changedItems []types.FetchedItem
	for _, resourceID := range resourceIDs {
		nodes, err := c.listResourceNodes(ctx, client, resourceID)
		var partialErr *partialNodeListError
		if err != nil {
			if !errors.As(err, &partialErr) {
				return nil, nil, fmt.Errorf("list nodes for resource %s: %w", resourceID, err)
			}
			changedItems = appendNodeListFailureItems(changedItems, resourceID, partialErr.Failures)
		}

		newCursor.ResourceNodeTimes[resourceID] = make(map[string]string)
		// On a partial listing, carry over the previous state so nodes hidden
		// by the failure are neither re-fetched nor reported as deleted.
		if partialErr != nil && prevCursor.ResourceNodeTimes != nil {
			for nodeID, ts := range prevCursor.ResourceNodeTimes[resourceID] {
				newCursor.ResourceNodeTimes[resourceID][nodeID] = ts
			}
		}

		currentNodes := make(map[string]bool)
		for _, node := range nodes {
			currentNodes[node.NodeID] = true
			tsStr := strconv.FormatInt(node.ModifiedTimestamp, 10)
			newCursor.ResourceNodeTimes[resourceID][node.NodeID] = tsStr

			if prevTimes, ok := prevCursor.ResourceNodeTimes[resourceID]; ok {
				if prevTS, exists := prevTimes[node.NodeID]; exists && prevTS == tsStr {
					continue // unchanged
				}
			}

			item, err := c.fetchNodeContent(ctx, client, node, resourceID)
			if err != nil {
				changedItems = append(changedItems, types.FetchedItem{
					ExternalID:       node.NodeID,
					Title:            node.Name,
					SourceResourceID: resourceID,
					Metadata: map[string]string{
						"error": err.Error(),
					},
				})
				continue
			}
			if item != nil {
				changedItems = append(changedItems, *item)
			}
		}

		// Detect deletions only when the listing was complete.
		if partialErr == nil && prevCursor.ResourceNodeTimes != nil {
			for nodeID := range prevCursor.ResourceNodeTimes[resourceID] {
				if !currentNodes[nodeID] {
					changedItems = append(changedItems, types.FetchedItem{
						ExternalID:       nodeID,
						IsDeleted:        true,
						SourceResourceID: resourceID,
					})
				}
			}
		}
	}

	nextCursorMap := make(map[string]interface{})
	cursorBytes, _ := json.Marshal(newCursor)
	_ = json.Unmarshal(cursorBytes, &nextCursorMap)

	return changedItems, &types.SyncCursor{
		LastSyncTime:    time.Now(),
		ConnectorCursor: nextCursorMap,
	}, nil
}

// listResourceNodes lists every node covered by a selected resource: the whole
// knowledge base when a workspace is selected, or the node itself plus all of
// its descendants when a node is selected.
func (c *Connector) listResourceNodes(ctx context.Context, client *Client, resourceID string) ([]wikiNode, error) {
	workspaceID, nodeID := parseNodeResourceID(resourceID)
	if nodeID == "" {
		ws, err := client.GetWorkspace(ctx, workspaceID)
		if err != nil {
			return nil, err
		}
		return client.ListNodesRecursive(ctx, ws.RootNodeID)
	}

	root, err := client.GetNode(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	if !root.HasChildren {
		return []wikiNode{root}, nil
	}
	descendants, err := client.ListNodesRecursive(ctx, nodeID)
	return append([]wikiNode{root}, descendants...), err
}

// fetchNodeContent fetches the content of a single wiki node and converts it
// to a FetchedItem.
//
// Only native DingTalk text documents (type=FILE, category=ALIDOC,
// extension=adoc) can be exported to markdown by the export API. Folders are
// skipped silently; other file categories (uploaded files, spreadsheets, AI
// tables) have no markdown export path and are skipped as well.
func (c *Connector) fetchNodeContent(ctx context.Context, client *Client, node wikiNode, resourceID string) (*types.FetchedItem, error) {
	if !isExportableNode(node) {
		return nil, nil
	}

	data, err := client.ExportMarkdown(ctx, node.NodeID)
	if err != nil {
		return nil, fmt.Errorf("export %s (%s): %w", node.Name, node.NodeID, err)
	}

	title := strings.TrimSuffix(node.Name, "."+extensionAdoc)
	workspaceID, _ := parseNodeResourceID(resourceID)
	if workspaceID == "" {
		workspaceID = node.WorkspaceID
	}

	return &types.FetchedItem{
		ExternalID:       node.NodeID,
		Title:            title,
		Content:          data,
		ContentType:      "text/markdown",
		FileName:         sanitizeFileName(title) + ".md",
		URL:              node.URL,
		UpdatedAt:        time.UnixMilli(node.ModifiedTimestamp),
		SourceResourceID: resourceID,
		Metadata: map[string]string{
			"node_id":      node.NodeID,
			"workspace_id": workspaceID,
			"category":     node.Category,
			"extension":    node.Extension,
			"creator":      node.CreatorID,
			"modifier":     node.ModifierID,
			"channel":      types.ChannelDingtalk,
		},
	}, nil
}

func appendNodeListFailureItems(items []types.FetchedItem, resourceID string, failures []nodeListFailure) []types.FetchedItem {
	for _, failure := range failures {
		node := failure.Node
		title := node.Name
		if title == "" {
			title = node.NodeID
		}
		items = append(items, types.FetchedItem{
			ExternalID:       node.NodeID,
			Title:            title,
			SourceResourceID: resourceID,
			Metadata: map[string]string{
				"error":         failure.Err.Error(),
				"channel":       types.ChannelDingtalk,
				"node_id":       node.NodeID,
				"workspace_id":  node.WorkspaceID,
				"failure_stage": "list_children",
			},
		})
	}
	return items
}

// --- Helper functions ---

func makeNodeResourceID(workspaceID, nodeID string) string {
	return workspaceID + nodeResourceSeparator + nodeID
}

func parseNodeResourceID(resourceID string) (workspaceID string, nodeID string) {
	workspaceID, nodeID, _ = strings.Cut(resourceID, nodeResourceSeparator)
	return workspaceID, nodeID
}

func nodeToResource(workspaceID, parentID string, node wikiNode) types.Resource {
	name := node.Name
	if name == "" {
		name = node.NodeID
	}

	resourceType := "document"
	if node.Type == nodeTypeFolder {
		resourceType = "folder"
	}

	return types.Resource{
		ExternalID:  makeNodeResourceID(workspaceID, node.NodeID),
		Name:        name,
		Type:        resourceType,
		URL:         node.URL,
		ParentID:    parentID,
		HasChildren: node.HasChildren,
		ModifiedAt:  time.UnixMilli(node.ModifiedTimestamp),
		Metadata: map[string]interface{}{
			"workspace_id": workspaceID,
			"node_id":      node.NodeID,
			"category":     node.Category,
			"extension":    node.Extension,
		},
	}
}

// isExportableNode reports whether the export API can convert this node to markdown.
func isExportableNode(node wikiNode) bool {
	return node.Type == nodeTypeFile &&
		node.Category == categoryAlidoc &&
		node.Extension == extensionAdoc
}

// parseDingTalkConfig extracts and validates DingTalk-specific configuration.
func parseDingTalkConfig(config *types.DataSourceConfig) (*Config, error) {
	if config == nil {
		return nil, fmt.Errorf("config is nil")
	}

	credBytes, err := json.Marshal(config.Credentials)
	if err != nil {
		return nil, fmt.Errorf("marshal credentials: %w", err)
	}

	var dingConfig Config
	if err := json.Unmarshal(credBytes, &dingConfig); err != nil {
		return nil, fmt.Errorf("parse dingtalk credentials: %w", err)
	}

	if dingConfig.AppKey == "" || dingConfig.AppSecret == "" {
		return nil, fmt.Errorf("dingtalk app_key and app_secret are required")
	}

	return &dingConfig, nil
}

// sanitizeFileName removes characters that are invalid in filenames and
// truncates at a UTF-8 rune boundary.
func sanitizeFileName(name string) string {
	if name == "" {
		return "untitled"
	}
	replacer := strings.NewReplacer(
		"/", "_", "\\", "_", ":", "_", "*", "_",
		"?", "_", "\"", "_", "<", "_", ">", "_", "|", "_",
	)
	result := replacer.Replace(name)
	const maxBytes = 200
	if len(result) > maxBytes {
		result = result[:maxBytes]
		for len(result) > 0 {
			r, size := utf8.DecodeLastRuneInString(result)
			if r != utf8.RuneError || size != 1 {
				break
			}
			result = result[:len(result)-1]
		}
	}
	return result
}
