package dingtalk

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// Compile-time proof that *Connector satisfies the datasource.Connector interface.
var _ datasource.Connector = (*Connector)(nil)

// Connector implements datasource.Connector for DingTalk documents / wiki.
type Connector struct{}

// NewConnector creates a new DingTalk connector.
func NewConnector() *Connector { return &Connector{} }

// Type returns the connector type identifier.
func (c *Connector) Type() string { return types.ConnectorTypeDingTalk }

// Validate verifies the given credentials by obtaining a token and listing workspaces.
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

// ResolveResourceAncestors returns the ancestors that must be expanded so a
// lazily-loaded picker can reveal pre-existing selections.
//
// For a workspace-only selection there are no ancestors. For a nested node
// selection ("workspaceId:nodeId") we walk parentNodeId up to the workspace
// root and return the intermediate composite IDs (plus the bare workspaceId
// so the root expands).
func (c *Connector) ResolveResourceAncestors(
	ctx context.Context, config *types.DataSourceConfig, resourceIDs []string,
) ([]string, error) {
	cfg, err := parseDingTalkConfig(config)
	if err != nil {
		return nil, err
	}
	cli := newClient(cfg)

	seen := make(map[string]struct{})
	var out []string
	add := func(id string) {
		if id == "" {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}

	// Cache workspace root node ids so we can stop the walk at the real root
	// (rootNodeId is not a selectable resource in the picker).
	rootCache := make(map[string]string)

	for _, rid := range resourceIDs {
		wsID, nodeID := parseResourceID(rid)
		if nodeID == "" {
			// Workspace itself — nothing to expand above it.
			continue
		}
		// Always expand the workspace root.
		add(wsID)

		rootNodeID := rootCache[wsID]
		if rootNodeID == "" {
			if ws, err := cli.GetWorkspace(ctx, wsID); err == nil {
				rootNodeID = ws.RootNodeID
				rootCache[wsID] = rootNodeID
			}
		}

		// Walk parents via GetNode until we hit the workspace root / empty parent.
		cur := nodeID
		for depth := 0; depth < 64 && cur != ""; depth++ {
			n, err := cli.GetNode(ctx, cur)
			if err != nil {
				logger.Warnf(ctx, "[DingTalk] resolve ancestors: get node %s: %v", cur, err)
				break
			}
			parent := n.ParentNodeID
			if parent == "" || parent == rootNodeID {
				break
			}
			// Intermediate folders must be expanded as composite resource IDs.
			add(encodeResourceID(wsID, parent))
			cur = parent
		}
	}
	return out, nil
}

// ListResources lists DingTalk wiki resources for selection, loading the tree
// lazily one level at a time (mirrors Feishu behaviour).
//
//   - parentID == ""                        → list all accessible workspaces
//   - parentID == workspaceID               → list top-level nodes of that workspace
//   - parentID == "workspaceID:nodeID"      → list direct children of that node
func (c *Connector) ListResources(
	ctx context.Context, config *types.DataSourceConfig, parentID string,
) ([]types.Resource, error) {
	cfg, err := parseDingTalkConfig(config)
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
		for _, s := range spaces {
			url := s.URL
			if url == "" && s.WorkspaceID != "" {
				url = "https://alidocs.dingtalk.com/i/spaces/" + s.WorkspaceID
			}
			resources = append(resources, types.Resource{
				ExternalID:  s.WorkspaceID,
				Name:        s.Name,
				Type:        "wiki_space",
				Description: s.Description,
				URL:         url,
				HasChildren: true,
				ModifiedAt:  parseDingTalkTime(s.ModifiedTime),
				Metadata: map[string]interface{}{
					"workspace_id": s.WorkspaceID,
					"root_node_id": s.RootNodeID,
				},
			})
		}
		sort.Slice(resources, func(i, j int) bool {
			return resources[i].ExternalID < resources[j].ExternalID
		})
		return resources, nil
	}

	wsID, nodeID := parseResourceID(parentID)
	parentNodeID := nodeID
	if parentNodeID == "" {
		// Expand a workspace: resolve its rootNodeId first.
		ws, err := cli.GetWorkspace(ctx, wsID)
		if err != nil {
			return nil, fmt.Errorf("get workspace %s: %w", wsID, err)
		}
		parentNodeID = ws.RootNodeID
		if parentNodeID == "" {
			return []types.Resource{}, nil
		}
	}

	nodes, err := cli.ListNodes(ctx, parentNodeID)
	if err != nil {
		return nil, fmt.Errorf("list dingtalk nodes under %s: %w", parentID, err)
	}

	resources := make([]types.Resource, 0, len(nodes))
	for _, n := range nodes {
		resources = append(resources, nodeToResource(wsID, parentID, n))
	}
	sort.Slice(resources, func(i, j int) bool {
		return resources[i].ExternalID < resources[j].ExternalID
	})
	return resources, nil
}

func nodeToResource(workspaceID string, n wikiNode) types.Resource {
	wsID := n.WorkspaceID
	if wsID == "" {
		wsID = workspaceID
	}
	extID := encodeResourceID(wsID, n.NodeID)
	rType := "file"
	hasChildren := isFolder(n)
	if hasChildren {
		rType = "doc_category"
	}
	url := n.URL
	if url == "" && n.NodeID != "" {
		url = "https://alidocs.dingtalk.com/i/nodes/" + n.NodeID
	}
	parentID := ""
	if n.ParentNodeID != "" {
		// Parent of top-level nodes is the workspace root — expose parent as
		// the bare workspace so the tree roots correctly under the space.
		// We cannot know rootNodeId here cheaply; leave ParentID as composite
		// of parent node. The UI sets parent via the expand request.
		parentID = encodeResourceID(wsID, n.ParentNodeID)
	}
	return types.Resource{
		ExternalID:  extID,
		Name:        n.Name,
		Type:        rType,
		URL:         url,
		ParentID:    parentID,
		HasChildren: hasChildren,
		ModifiedAt:  parseDingTalkTime(n.ModifiedTime),
		Metadata: map[string]interface{}{
			"workspace_id": wsID,
			"node_id":      n.NodeID,
			"category":     n.Category,
			"node_type":    n.Type,
		},
	}
}

// FetchAll performs a full sync of the specified resources.
func (c *Connector) FetchAll(ctx context.Context, config *types.DataSourceConfig, resourceIDs []string) ([]types.FetchedItem, error) {
	items, _, err := c.walk(ctx, config, resourceIDs, nil, false)
	return items, err
}

// FetchIncremental returns items changed (or deleted) since the prior cursor.
func (c *Connector) FetchIncremental(
	ctx context.Context,
	config *types.DataSourceConfig,
	cursor *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	resourceIDs := config.ResourceIDs
	if len(resourceIDs) == 0 {
		return nil, nil, fmt.Errorf("no resource IDs configured")
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

// walk is the shared implementation for FetchAll / FetchIncremental.
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
		LastSyncTime: time.Now(),
		NodeModTimes: make(map[string]map[string]string),
	}
	var out []types.FetchedItem

	// Cache workspace root node ids to avoid repeated GetWorkspace calls.
	wsRootCache := make(map[string]string)

	for _, rid := range resourceIDs {
		wsID, nodeID := parseResourceID(rid)
		if wsID == "" {
			return nil, nil, fmt.Errorf("invalid resource id %q", rid)
		}

		// Determine the walk root.
		var walkRoot string
		var includeRoot *wikiNode
		if nodeID == "" {
			root, err := resolveWorkspaceRoot(ctx, cli, wsID, wsRootCache)
			if err != nil {
				return nil, nil, fmt.Errorf("resolve workspace %s root: %w", wsID, err)
			}
			walkRoot = root
		} else {
			// Selected a specific node: include it (if file) + all descendants.
			n, err := cli.GetNode(ctx, nodeID)
			if err != nil {
				return nil, nil, fmt.Errorf("get node %s: %w", nodeID, err)
			}
			if n.WorkspaceID == "" {
				n.WorkspaceID = wsID
			}
			includeRoot = &n
			walkRoot = nodeID
		}

		nodes, err := cli.ListAllNodesRecursive(ctx, walkRoot)
		if err != nil {
			// Partial: keep what we have so far for this resource.
			logger.Warnf(ctx, "[DingTalk] recursive list under %s: %v", rid, err)
		}
		if includeRoot != nil {
			// Prepend the selected root node so files selected directly are synced.
			nodes = append([]wikiNode{*includeRoot}, nodes...)
		}

		currentDocs := make(map[string]bool)
		if newCursor.NodeModTimes[rid] == nil {
			newCursor.NodeModTimes[rid] = make(map[string]string)
		}

		// Deduplicate by node id (includeRoot may also appear in recursive list).
		seenNodes := make(map[string]struct{})
		var kept, skippedFolder int
		for _, n := range nodes {
			if n.NodeID == "" {
				continue
			}
			if _, ok := seenNodes[n.NodeID]; ok {
				continue
			}
			seenNodes[n.NodeID] = struct{}{}

			if !isSyncableFile(n) {
				skippedFolder++
				continue
			}
			kept++
			currentDocs[n.NodeID] = true
			mod := n.ModifiedTime
			newCursor.NodeModTimes[rid][n.NodeID] = mod

			// Incremental: skip unchanged.
			if incremental && prev != nil && prev.NodeModTimes != nil {
				if prevTimes, ok := prev.NodeModTimes[rid]; ok {
					if prevTimes[n.NodeID] == mod && mod != "" {
						continue
					}
				}
			}

			// Rate-limit between content fetches.
			if err := sleepCtx(ctx, 200*time.Millisecond); err != nil {
				return nil, nil, err
			}

			docKey := n.DocKey
			if docKey == "" {
				docKey = n.NodeID
			}
			blocks, err := cli.GetDocumentBlocks(ctx, docKey)
			if err != nil {
				out = append(out, types.FetchedItem{
					ExternalID:       n.NodeID,
					Title:            n.Name,
					SourceResourceID: rid,
					Metadata: map[string]string{
						"error":        err.Error(),
						"channel":      types.ChannelDingtalk,
						"node_id":      n.NodeID,
						"workspace_id": wsID,
						"category":     n.Category,
					},
				})
				continue
			}

			md := blocksToMarkdown(blocks)
			url := n.URL
			if url == "" {
				url = "https://alidocs.dingtalk.com/i/nodes/" + n.NodeID
			}
			title := n.Name
			if title == "" {
				title = "untitled"
			}

			out = append(out, types.FetchedItem{
				ExternalID:       n.NodeID,
				Title:            title,
				Content:          []byte(md),
				ContentType:      "text/markdown",
				FileName:         sanitizeFileName(title) + ".md",
				URL:              url,
				UpdatedAt:        parseDingTalkTime(mod),
				SourceResourceID: rid,
				Metadata: map[string]string{
					"node_id":      n.NodeID,
					"workspace_id": wsID,
					"category":     n.Category,
					"channel":      types.ChannelDingtalk,
				},
			})
		}

		logger.Infof(ctx, "[DingTalk] resource %s: nodes=%d kept_files=%d skipped_folder=%d",
			rid, len(nodes), kept, skippedFolder)

		// Deletion detection (incremental only).
		if incremental && prev != nil && prev.NodeModTimes != nil {
			if prevTimes, ok := prev.NodeModTimes[rid]; ok {
				for prevDocID := range prevTimes {
					if !currentDocs[prevDocID] {
						out = append(out, types.FetchedItem{
							ExternalID:       prevDocID,
							IsDeleted:        true,
							SourceResourceID: rid,
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

func resolveWorkspaceRoot(
	ctx context.Context, cli *client, wsID string, cache map[string]string,
) (string, error) {
	if root, ok := cache[wsID]; ok {
		return root, nil
	}
	ws, err := cli.GetWorkspace(ctx, wsID)
	if err != nil {
		return "", err
	}
	if ws.RootNodeID == "" {
		return "", fmt.Errorf("workspace %s has empty rootNodeId", wsID)
	}
	cache[wsID] = ws.RootNodeID
	return ws.RootNodeID, nil
}
