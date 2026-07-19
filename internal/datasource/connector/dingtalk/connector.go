package dingtalk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// Connector implements the datasource.Connector interface for DingTalk.
type Connector struct{}

// NewConnector creates a new DingTalk connector.
func NewConnector() *Connector {
	return &Connector{}
}

// Compile-time interface check.
var _ datasource.Connector = (*Connector)(nil)

// Type returns the connector type identifier.
func (c *Connector) Type() string {
	return types.ConnectorTypeDingTalk
}

const dingtalkNodeResourceSeparator = ":"

// Validate verifies that the DingTalk configuration is valid by testing connectivity.
func (c *Connector) Validate(ctx context.Context, config *types.DataSourceConfig) error {
	dingtalkConfig, err := parseDingtalkConfig(config)
	if err != nil {
		return err
	}

	client := NewClient(dingtalkConfig)
	if err := client.Ping(ctx); err != nil {
		return fmt.Errorf("dingtalk connection failed: %w", err)
	}

	return nil
}

// ListResources lists DingTalk knowledge base resources for selection, loading the tree
// lazily one level at a time to avoid traversing the entire wiki up front.
//
//   - parentID == ""                  → list all accessible knowledge bases (workspaces).
//   - parentID == workspaceId         → list the root nodes of that workspace.
//   - parentID == "workspaceId:nodeId" → list the direct children of that node.
func (c *Connector) ListResources(
	ctx context.Context, config *types.DataSourceConfig, parentID string,
) ([]types.Resource, error) {
	dingtalkConfig, err := parseDingtalkConfig(config)
	if err != nil {
		return nil, err
	}

	client := NewClient(dingtalkConfig)

	if parentID == "" {
		// Root level: list all knowledge bases
		workspaces, err := client.ListWorkspaces(ctx)
		if err != nil {
			return nil, fmt.Errorf("list dingtalk workspaces: %w", err)
		}

		resources := make([]types.Resource, 0, len(workspaces))
		for _, ws := range workspaces {
			resources = append(resources, types.Resource{
				ExternalID:  ws.WorkspaceID,
				Name:        ws.Name,
				Type:        "wiki_workspace",
				Description: ws.Description,
				URL:         ws.URL,
				HasChildren: true,
				ModifiedAt:  parseDingtalkTime(ws.ModifiedTime),
				Metadata: map[string]interface{}{
					"workspace_id": ws.WorkspaceID,
					"root_node_id": ws.RootNodeID,
					"type":         ws.Type,
				},
			})
		}
		return resources, nil
	}

	// Lazy load: list only the direct children of the given workspace / node.
	workspaceID, nodeID := parseDingtalkResourceID(parentID)

	// Determine the parent node ID for the API call
	var parentNodeID string
	if nodeID == "" {
		// We're at the workspace level — need to get the root node ID
		// Fetch workspace info to get rootNodeId
		ws, err := client.GetWorkspaceRootNodeID(ctx, workspaceID)
		if err != nil {
			return nil, fmt.Errorf("get workspace root node for %s: %w", workspaceID, err)
		}
		parentNodeID = ws
	} else {
		parentNodeID = nodeID
	}

	nodes, err := client.ListNodes(ctx, parentNodeID)
	if err != nil {
		return nil, fmt.Errorf("list dingtalk nodes under %s: %w", parentID, err)
	}

	resources := make([]types.Resource, 0, len(nodes))
	for _, n := range nodes {
		resources = append(resources, nodeToResource(workspaceID, n))
	}
	return resources, nil
}

// ResolveResourceAncestors returns the resource IDs of every parent that has to
// be expanded so the lazily-loaded picker can reveal each given selection. For a
// selected node "workspaceId:nodeId" that is its workspace plus every intermediate
// node up the tree; the walk uses GetNode (parentId) and is O(depth)
// per selection, so it never re-traverses the whole wiki.
func (c *Connector) ResolveResourceAncestors(
	ctx context.Context, config *types.DataSourceConfig, resourceIDs []string,
) ([]string, error) {
	dingtalkConfig, err := parseDingtalkConfig(config)
	if err != nil {
		return nil, err
	}
	client := NewClient(dingtalkConfig)

	seen := make(map[string]bool)
	ancestors := make([]string, 0)
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			ancestors = append(ancestors, id)
		}
	}

	for _, rid := range resourceIDs {
		workspaceID, nodeID := parseDingtalkResourceID(rid)
		if workspaceID == "" || nodeID == "" {
			// A workspace-level selection is already a top-level node in the picker;
			// there is nothing above it to reveal.
			continue
		}
		// The workspace's direct children must be loaded to reveal the top-level node.
		add(workspaceID)

		// Walk up from the selection to the top, loading each intermediate
		// parent so the path down to the selection becomes visible.
		current := nodeID
		for current != "" {
			node, err := client.GetNode(ctx, current)
			if err != nil {
				// Best-effort: a broken path just stays collapsed, the rest of
				// the selections are still revealed.
				logger.Warnf(ctx, "[DingTalk] resolve ancestors: get node %s: %v", current, err)
				break
			}
			if node.ParentID == "" {
				break
			}
			add(makeDingtalkNodeResourceID(workspaceID, node.ParentID))
			current = node.ParentID
		}
	}

	return ancestors, nil
}

// FetchAll performs a full sync of all documents from the specified knowledge base workspaces.
func (c *Connector) FetchAll(ctx context.Context, config *types.DataSourceConfig, resourceIDs []string) ([]types.FetchedItem, error) {
	dingtalkConfig, err := parseDingtalkConfig(config)
	if err != nil {
		return nil, err
	}

	client := NewClient(dingtalkConfig)

	var allItems []types.FetchedItem

	for _, resourceID := range resourceIDs {
		workspaceID, nodeID := parseDingtalkResourceID(resourceID)

		// Determine the root node to start traversal from
		var rootNodeID string
		if nodeID == "" {
			// Workspace level — get root node ID
			rootID, err := client.GetWorkspaceRootNodeID(ctx, workspaceID)
			if err != nil {
				return nil, fmt.Errorf("get root node for workspace %s: %w", workspaceID, err)
			}
			rootNodeID = rootID
		} else {
			rootNodeID = nodeID
		}

		// List all nodes recursively
		nodes, err := client.ListAllNodesRecursive(ctx, rootNodeID)
		if err != nil {
			var partialErr *partialNodeListError
			if !errors.As(err, &partialErr) {
				return nil, fmt.Errorf("list nodes for resource %s: %w", resourceID, err)
			}
			allItems = appendNodeListFailureItems(allItems, workspaceID, resourceID, partialErr.Failures)
		}

		// Fetch content for each document node
		for _, n := range nodes {
			item, err := c.fetchNodeContent(ctx, client, n, workspaceID, resourceID)
			if err != nil {
				// Log error but continue with other nodes
				allItems = append(allItems, types.FetchedItem{
					ExternalID:       n.NodeID,
					Title:            n.Name,
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

// FetchIncremental performs an incremental sync by comparing node modified times
// against the previously recorded state.
func (c *Connector) FetchIncremental(ctx context.Context, config *types.DataSourceConfig, cursor *types.SyncCursor) ([]types.FetchedItem, *types.SyncCursor, error) {
	dingtalkConfig, err := parseDingtalkConfig(config)
	if err != nil {
		return nil, nil, err
	}

	client := NewClient(dingtalkConfig)

	// Parse the previous cursor state
	var prevCursor dingtalkCursor
	if cursor != nil && cursor.ConnectorCursor != nil {
		cursorBytes, marshalErr := json.Marshal(cursor.ConnectorCursor)
		if marshalErr != nil {
			logger.Warnf(ctx, "[DingTalk] FetchIncremental: failed to marshal cursor, treating as first sync: %v", marshalErr)
		} else {
			if unmarshalErr := json.Unmarshal(cursorBytes, &prevCursor); unmarshalErr != nil {
				logger.Warnf(ctx, "[DingTalk] FetchIncremental: failed to unmarshal cursor, treating as first sync: %v", unmarshalErr)
			}
		}
	}

	// Build new cursor to track current state
	newCursor := dingtalkCursor{
		LastSyncTime: time.Now(),
		NodeTimes:    make(map[string]map[string]string),
	}

	var changedItems []types.FetchedItem

	// Get resource IDs from config
	resourceIDs := config.ResourceIDs
	if len(resourceIDs) == 0 {
		return nil, nil, fmt.Errorf("no resource IDs configured")
	}

	for _, resourceID := range resourceIDs {
		workspaceID, nodeID := parseDingtalkResourceID(resourceID)

		// Determine the root node to start traversal from
		var rootNodeID string
		if nodeID == "" {
			rootID, err := client.GetWorkspaceRootNodeID(ctx, workspaceID)
			if err != nil {
				return nil, nil, fmt.Errorf("get root node for workspace %s: %w", workspaceID, err)
			}
			rootNodeID = rootID
		} else {
			rootNodeID = nodeID
		}

		// List all nodes recursively
		nodes, err := client.ListAllNodesRecursive(ctx, rootNodeID)
		var partialErr *partialNodeListError
		if err != nil {
			if !errors.As(err, &partialErr) {
				return nil, nil, fmt.Errorf("list nodes for resource %s: %w", resourceID, err)
			}
			changedItems = appendNodeListFailureItems(changedItems, workspaceID, resourceID, partialErr.Failures)
		}

		// Preserve previous cursor entries for nodes that failed to list,
		// so they won't be mistakenly marked as deleted on the next sync.
		newCursor.NodeTimes[resourceID] = make(map[string]string)
		if partialErr != nil && prevCursor.NodeTimes != nil {
			if prevTimes, ok := prevCursor.NodeTimes[resourceID]; ok {
				for prevNodeID, modTime := range prevTimes {
					newCursor.NodeTimes[resourceID][prevNodeID] = modTime
				}
			}
		}

		// Build a set of current node IDs for deletion detection
		currentNodes := make(map[string]bool)

		for _, n := range nodes {
			currentNodes[n.NodeID] = true
			modTime := n.ModifiedTime
			newCursor.NodeTimes[resourceID][n.NodeID] = modTime

			// Check if node has changed since last sync
			if prevCursor.NodeTimes != nil {
				if prevTimes, ok := prevCursor.NodeTimes[resourceID]; ok {
					if prevModTime, exists := prevTimes[n.NodeID]; exists {
						if prevModTime == modTime {
							// Node unchanged, skip
							continue
						}
					}
				}
			}

			// Node is new or changed — fetch its content
			item, err := c.fetchNodeContent(ctx, client, n, workspaceID, resourceID)
			if err != nil {
				// Record failed items
				changedItems = append(changedItems, types.FetchedItem{
					ExternalID:       n.NodeID,
					Title:            n.Name,
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

		// Detect deleted nodes
		if partialErr == nil && prevCursor.NodeTimes != nil {
			if prevTimes, ok := prevCursor.NodeTimes[resourceID]; ok {
				for deletedNodeID := range prevTimes {
					if !currentNodes[deletedNodeID] {
						// Node was deleted
						changedItems = append(changedItems, types.FetchedItem{
							ExternalID:       deletedNodeID,
							IsDeleted:        true,
							SourceResourceID: resourceID,
						})
					}
				}
			}
		}
	}

	// Build next sync cursor
	nextCursorMap := make(map[string]interface{})
	cursorBytes, marshalErr := json.Marshal(newCursor)
	if marshalErr != nil {
		logger.Warnf(ctx, "[DingTalk] FetchIncremental: failed to marshal new cursor: %v", marshalErr)
	} else if unmarshalErr := json.Unmarshal(cursorBytes, &nextCursorMap); unmarshalErr != nil {
		logger.Warnf(ctx, "[DingTalk] FetchIncremental: failed to unmarshal new cursor: %v", unmarshalErr)
	}

	nextSyncCursor := &types.SyncCursor{
		LastSyncTime:    time.Now(),
		ConnectorCursor: nextCursorMap,
	}

	return changedItems, nextSyncCursor, nil
}

// fetchNodeContent fetches the content of a single node and converts it to FetchedItem.
// Dispatches to different retrieval strategies based on node type and category:
//   - ALIDOC (钉钉文档) → blocks API → Markdown conversion
//   - FOLDER → skip (no content)
//   - DOCUMENT / IMAGE / VIDEO / others → skip (v1: no download API available)
func (c *Connector) fetchNodeContent(ctx context.Context, client *Client, n node, workspaceID string, resourceID string) (*types.FetchedItem, error) {
	// Skip folders
	if n.Type == "FOLDER" {
		return nil, nil
	}

	// Only process ALIDOC (DingTalk documents) in v1
	if n.Category != "ALIDOC" {
		logger.Infof(ctx, "[DingTalk] skipping node %s: unsupported category=%s type=%s", n.NodeID, n.Category, n.Type)
		return nil, nil
	}

	// Fetch document blocks
	blocks, err := client.GetDocumentBlocks(ctx, n.NodeID)
	if err != nil {
		return nil, fmt.Errorf("get blocks for %s (%s): %w", n.Name, n.NodeID, err)
	}

	// Convert blocks to Markdown
	markdown := blocksToMarkdown(blocks)

	editTime := parseDingtalkTime(n.ModifiedTime)

	meta := map[string]string{
		"node_id":      n.NodeID,
		"workspace_id": workspaceID,
		"category":     n.Category,
		"extension":    n.Extension,
		"creator_id":   n.CreatorID,
		"modifier_id":  n.ModifierID,
		"channel":      types.ChannelDingtalk,
	}

	fileName := sanitizeFileName(n.Name) + ".md"

	return &types.FetchedItem{
		ExternalID:       n.NodeID,
		Title:            n.Name,
		Content:          []byte(markdown),
		ContentType:      "text/markdown; charset=utf-8",
		FileName:         fileName,
		URL:              n.URL,
		UpdatedAt:        editTime,
		SourceResourceID: resourceID,
		Metadata:         meta,
	}, nil
}

// --- Helper functions ---

// makeDingtalkNodeResourceID creates a compound resource ID from workspace and node IDs.
func makeDingtalkNodeResourceID(workspaceID, nodeID string) string {
	return workspaceID + dingtalkNodeResourceSeparator + nodeID
}

// parseDingtalkResourceID splits a compound resource ID into workspace and node IDs.
func parseDingtalkResourceID(resourceID string) (workspaceID string, nodeID string) {
	workspaceID, nodeID, _ = strings.Cut(resourceID, dingtalkNodeResourceSeparator)
	return workspaceID, nodeID
}

// nodeToResource converts a DingTalk node to a Resource for the picker UI.
func nodeToResource(workspaceID string, n node) types.Resource {
	parentID := workspaceID
	if n.ParentID != "" {
		parentID = makeDingtalkNodeResourceID(workspaceID, n.ParentID)
	}

	name := n.Name
	if name == "" {
		name = n.NodeID
	}

	return types.Resource{
		ExternalID:  makeDingtalkNodeResourceID(workspaceID, n.NodeID),
		Name:        name,
		Type:        "wiki_node",
		URL:         n.URL,
		ParentID:    parentID,
		HasChildren: n.HasChildren,
		ModifiedAt:  parseDingtalkTime(n.ModifiedTime),
		Metadata: map[string]interface{}{
			"workspace_id": workspaceID,
			"node_id":      n.NodeID,
			"category":     n.Category,
			"type":         n.Type,
		},
	}
}

// appendNodeListFailureItems appends error items for partial node listing failures.
func appendNodeListFailureItems(items []types.FetchedItem, workspaceID string, resourceID string, failures []nodeListFailure) []types.FetchedItem {
	for _, failure := range failures {
		n := failure.Node
		title := n.Name
		if title == "" {
			title = n.NodeID
		}
		items = append(items, types.FetchedItem{
			ExternalID:       n.NodeID,
			Title:            title,
			SourceResourceID: resourceID,
			Metadata: map[string]string{
				"error":         failure.Err.Error(),
				"channel":       types.ChannelDingtalk,
				"node_id":       n.NodeID,
				"workspace_id":  workspaceID,
				"failure_stage": "list_children",
			},
		})
	}
	return items
}

// blocksToMarkdown converts DingTalk document blocks to Markdown string.
func blocksToMarkdown(blocks []block) string {
	var sb strings.Builder

	for i, b := range blocks {
		if i > 0 {
			sb.WriteString("\n")
		}

		switch b.BlockType {
		case "heading":
			if b.Heading != nil {
				level := b.Heading.Level.Int()
				if level < 1 {
					level = 1
				}
				if level > 6 {
					level = 6
				}
				sb.WriteString(strings.Repeat("#", level) + " " + b.Heading.Text)
			}
		case "paragraph":
			if b.Paragraph != nil {
				sb.WriteString(b.Paragraph.Text)
			}
		case "codeBlock":
			if b.CodeBlock != nil {
				lang := b.CodeBlock.Language
				sb.WriteString("```" + lang + "\n")
				sb.WriteString(b.CodeBlock.Text)
				if !strings.HasSuffix(b.CodeBlock.Text, "\n") {
					sb.WriteString("\n")
				}
				sb.WriteString("```")
			}
		case "table":
			if b.Table != nil && len(b.Table.Cells) > 0 {
				writeTableMarkdown(&sb, b.Table)
			}
		case "list":
			if b.List != nil {
				for idx, item := range b.List.Items {
					if b.List.Style == "ordered" {
						sb.WriteString(fmt.Sprintf("%d. %s", idx+1, item))
					} else {
						sb.WriteString("- " + item)
					}
					if idx < len(b.List.Items)-1 {
						sb.WriteString("\n")
					}
				}
			}
		case "quote":
			if b.Quote != nil {
				// Prefix each line with "> "
				lines := strings.Split(b.Quote.Text, "\n")
				for _, line := range lines {
					sb.WriteString("> " + line + "\n")
				}
			}
		case "divider":
			sb.WriteString("---")
		case "image":
			if b.Image != nil {
				alt := b.Image.AltText
				if alt == "" {
					alt = "image"
				}
				sb.WriteString(fmt.Sprintf("![%s](%s)", alt, b.Image.URL))
			}
		default:
			// Unknown block type — try to extract any text content
			text := extractBlockText(b)
			if text != "" {
				sb.WriteString(text)
			}
		}
	}

	return sb.String()
}

// writeTableMarkdown writes a table in Markdown format.
// DingTalk returns cells as String[][] (not [][]struct), so each cell is a plain string.
// Data rows are normalized to match header column count to produce valid Markdown.
func writeTableMarkdown(sb *strings.Builder, t *blockTable) {
	if len(t.Cells) == 0 {
		return
	}

	// Header row
	headerRow := t.Cells[0]
	colCount := len(headerRow)
	if colCount == 0 {
		return
	}
	for i, cell := range headerRow {
		if i > 0 {
			sb.WriteString(" | ")
		}
		sb.WriteString(strings.ReplaceAll(cell, "|", "\\|"))
	}
	sb.WriteString("\n")

	// Separator row
	for i := 0; i < colCount; i++ {
		if i > 0 {
			sb.WriteString(" | ")
		}
		sb.WriteString("---")
	}
	sb.WriteString("\n")

	// Data rows — normalize each row to match header column count
	for rowIdx := 1; rowIdx < len(t.Cells); rowIdx++ {
		row := t.Cells[rowIdx]
		for i := 0; i < colCount; i++ {
			if i > 0 {
				sb.WriteString(" | ")
			}
			if i < len(row) {
				sb.WriteString(strings.ReplaceAll(row[i], "|", "\\|"))
			}
			// else: missing cell — leave empty
		}
		sb.WriteString("\n")
	}
}

// extractBlockText tries to extract plain text from an unknown block type.
func extractBlockText(b block) string {
	if b.Paragraph != nil {
		return b.Paragraph.Text
	}
	if b.Heading != nil {
		return b.Heading.Text
	}
	return ""
}
