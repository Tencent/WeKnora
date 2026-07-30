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

	"github.com/Tencent/WeKnora/internal/logger"
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

// ResolveResourceAncestors returns, for each selected node, the resource IDs of
// every ancestor whose children the lazily-loaded picker must expand to reveal
// it. The DingTalk node API has no parent pointer, so ancestry is recovered by
// walking each referenced workspace from its root node and recording the path;
// the walk stops once every requested node is located. It is best-effort: a
// listing error along the way just leaves that branch collapsed.
func (c *Connector) ResolveResourceAncestors(
	ctx context.Context, config *types.DataSourceConfig, resourceIDs []string,
) ([]string, error) {
	dingConfig, err := parseDingTalkConfig(config)
	if err != nil {
		return nil, err
	}
	client := NewClient(dingConfig)

	// Group the target node IDs by workspace. Bare-workspace selections are
	// top-level in the picker and have nothing above them to reveal.
	targetsByWorkspace := make(map[string]map[string]bool)
	for _, rid := range resourceIDs {
		workspaceID, nodeID := parseNodeResourceID(rid)
		if workspaceID == "" || nodeID == "" {
			continue
		}
		if targetsByWorkspace[workspaceID] == nil {
			targetsByWorkspace[workspaceID] = make(map[string]bool)
		}
		targetsByWorkspace[workspaceID][nodeID] = true
	}

	seen := make(map[string]bool)
	ancestors := make([]string, 0)
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			ancestors = append(ancestors, id)
		}
	}

	for workspaceID, targets := range targetsByWorkspace {
		ws, err := client.GetWorkspace(ctx, workspaceID)
		if err != nil {
			logger.Warnf(ctx, "[DingTalk] resolve ancestors: get workspace %s: %v", workspaceID, err)
			continue
		}

		// BFS from the root's direct children (whose picker parent is the bare
		// workspace ID), carrying each node's ancestor chain down the tree.
		type queued struct {
			node  wikiNode
			chain []string
		}
		top, err := client.ListNodes(ctx, ws.RootNodeID)
		if err != nil {
			logger.Warnf(ctx, "[DingTalk] resolve ancestors: list root of %s: %v", workspaceID, err)
			continue
		}
		queue := make([]queued, 0, len(top))
		for _, n := range top {
			queue = append(queue, queued{node: n, chain: []string{workspaceID}})
		}

		visited := make(map[string]bool)
		remaining := len(targets)
		for len(queue) > 0 && remaining > 0 {
			cur := queue[0]
			queue = queue[1:]
			if visited[cur.node.NodeID] {
				continue
			}
			visited[cur.node.NodeID] = true

			if targets[cur.node.NodeID] {
				for _, a := range cur.chain {
					add(a)
				}
				remaining--
			}
			if cur.node.HasChildren {
				children, err := client.ListNodes(ctx, cur.node.NodeID)
				if err != nil {
					logger.Warnf(ctx, "[DingTalk] resolve ancestors: list children of %s: %v", cur.node.NodeID, err)
					continue
				}
				childChain := append(append([]string{}, cur.chain...), makeNodeResourceID(workspaceID, cur.node.NodeID))
				for _, ch := range children {
					queue = append(queue, queued{node: ch, chain: childChain})
				}
			}
		}
	}

	return ancestors, nil
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
						"error":   err.Error(),
						"channel": types.ChannelDingtalk,
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

		prevTimes := prevCursor.ResourceNodeTimes[resourceID] // may be nil; reads are safe
		currentNodes := make(map[string]bool)
		for _, node := range nodes {
			currentNodes[node.NodeID] = true
			tsStr := strconv.FormatInt(node.ModifiedTimestamp, 10)

			if prevTS, exists := prevTimes[node.NodeID]; exists && prevTS == tsStr {
				// Unchanged: keep the cursor entry as-is.
				newCursor.ResourceNodeTimes[resourceID][node.NodeID] = tsStr
				continue
			}

			item, err := c.fetchNodeContent(ctx, client, node, resourceID)
			if err != nil {
				// Do NOT advance the cursor for a node whose fetch failed;
				// otherwise the next run would treat it as unchanged and never
				// retry, silently dropping its content forever. Preserve the
				// previous value (if any) so deletion detection keeps its
				// baseline while the mismatch forces a retry next run.
				if prevTS, ok := prevTimes[node.NodeID]; ok {
					newCursor.ResourceNodeTimes[resourceID][node.NodeID] = prevTS
				}
				changedItems = append(changedItems, types.FetchedItem{
					ExternalID:       node.NodeID,
					Title:            node.Name,
					SourceResourceID: resourceID,
					Metadata: map[string]string{
						"error":   err.Error(),
						"channel": types.ChannelDingtalk,
					},
				})
				continue
			}
			// Success (item may be nil for non-exportable nodes): record the
			// new timestamp so subsequent syncs skip it until it changes again.
			newCursor.ResourceNodeTimes[resourceID][node.NodeID] = tsStr
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
	result := append([]wikiNode{root}, descendants...)
	if err != nil {
		var partialErr *partialNodeListError
		if !errors.As(err, &partialErr) {
			// The first-level children listing failed outright. Degrade to a
			// partial error (carrying the selected root) instead of a fatal one
			// so FetchAll/FetchIncremental still sync the root and continue with
			// other resources, rather than aborting the entire sync.
			return result, &partialNodeListError{Failures: []nodeListFailure{{Node: root, Err: err}}}
		}
	}
	return result, err
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
