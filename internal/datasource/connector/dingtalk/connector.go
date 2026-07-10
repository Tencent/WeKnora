package dingtalk

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

var _ datasource.Connector = (*Connector)(nil)

type Connector struct{}

func NewConnector() *Connector { return &Connector{} }

func (c *Connector) Type() string { return types.ConnectorTypeDingTalk }

func (c *Connector) Validate(ctx context.Context, config *types.DataSourceConfig) error {
	cfg, err := parseDingTalkConfig(config)
	if err != nil {
		return err
	}
	if err := newClient(cfg).Ping(ctx); err != nil {
		return fmt.Errorf("dingtalk connection failed: %w", err)
	}
	return nil
}

func (c *Connector) ListResources(
	ctx context.Context, config *types.DataSourceConfig, parentID string,
) ([]types.Resource, error) {
	cfg, err := parseDingTalkConfig(config)
	if err != nil {
		return nil, err
	}
	cli := newClient(cfg)

	if parentID == "" {
		spaces, err := cli.ListSpaces(ctx)
		if err != nil {
			return nil, fmt.Errorf("list dingtalk doc spaces: %w", err)
		}
		resources := make([]types.Resource, 0, len(spaces))
		for _, space := range spaces {
			resources = append(resources, types.Resource{
				ExternalID:  space.SpaceID,
				Name:        space.Name,
				Type:        "doc_space",
				Description: space.Description,
				URL:         space.URL,
				ModifiedAt:  parseDingTalkTime(space.ModifiedAt),
				HasChildren: true,
				Metadata: map[string]interface{}{
					"space_id": space.SpaceID,
				},
			})
		}
		return resources, nil
	}

	spaceID, nodeID := parseResourceID(parentID)
	nodes, err := cli.ListNodes(ctx, spaceID, nodeID)
	if err != nil {
		return nil, fmt.Errorf("list dingtalk doc nodes under %s: %w", parentID, err)
	}
	resources := make([]types.Resource, 0, len(nodes))
	for _, node := range nodes {
		resources = append(resources, nodeToResource(spaceID, parentID, node))
	}
	return resources, nil
}

func (c *Connector) ResolveResourceAncestors(
	ctx context.Context, config *types.DataSourceConfig, resourceIDs []string,
) ([]string, error) {
	cfg, err := parseDingTalkConfig(config)
	if err != nil {
		return nil, err
	}
	cli := newClient(cfg)

	seen := make(map[string]bool)
	ancestors := make([]string, 0)
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			ancestors = append(ancestors, id)
		}
	}

	for _, resourceID := range resourceIDs {
		spaceID, nodeID := parseResourceID(resourceID)
		if spaceID == "" || nodeID == "" {
			continue
		}
		add(spaceID)
		path, err := ancestorPathForNode(ctx, cli, spaceID, nodeID)
		if err != nil {
			return nil, err
		}
		for _, id := range path {
			add(id)
		}
	}
	return ancestors, nil
}

func (c *Connector) FetchAll(
	ctx context.Context, config *types.DataSourceConfig, resourceIDs []string,
) ([]types.FetchedItem, error) {
	items, _, err := c.walk(ctx, config, resourceIDs, nil, false)
	return items, err
}

func (c *Connector) FetchIncremental(
	ctx context.Context, config *types.DataSourceConfig, cursor *types.SyncCursor,
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
	cursorMap := map[string]interface{}{}
	b, _ := json.Marshal(newCursor)
	_ = json.Unmarshal(b, &cursorMap)
	return items, &types.SyncCursor{
		LastSyncTime:    newCursor.LastSyncTime,
		ConnectorCursor: cursorMap,
	}, nil
}

func (c *Connector) walk(
	ctx context.Context,
	config *types.DataSourceConfig,
	resourceIDs []string,
	prev *dingtalkCursor,
	incremental bool,
) ([]types.FetchedItem, *dingtalkCursor, error) {
	if len(resourceIDs) == 0 {
		return nil, nil, fmt.Errorf("no resource IDs configured")
	}
	cfg, err := parseDingTalkConfig(config)
	if err != nil {
		return nil, nil, err
	}
	cli := newClient(cfg)
	newCursor := &dingtalkCursor{
		LastSyncTime:   time.Now(),
		SpaceNodeTimes: make(map[string]map[string]string),
	}
	var out []types.FetchedItem

	for _, resourceID := range resourceIDs {
		spaceID, nodeID := parseResourceID(resourceID)
		if spaceID == "" {
			return nil, nil, fmt.Errorf("invalid dingtalk resource id %q", resourceID)
		}
		nodes, err := syncNodesForResource(ctx, cli, spaceID, nodeID)
		if err != nil {
			return nil, nil, fmt.Errorf("list nodes for resource %s: %w", resourceID, err)
		}

		currentDocs := map[string]bool{}
		newCursor.SpaceNodeTimes[resourceID] = map[string]string{}
		for _, node := range nodes {
			if !isDocumentNode(node) {
				continue
			}
			docID := node.docID()
			if docID == "" {
				continue
			}
			updatedAt := node.updated()
			currentDocs[docID] = true
			newCursor.SpaceNodeTimes[resourceID][docID] = updatedAt

			if incremental && prev != nil && prev.SpaceNodeTimes != nil {
				if prevTimes, ok := prev.SpaceNodeTimes[resourceID]; ok && prevTimes[docID] == updatedAt {
					continue
				}
			}

			detail, err := cli.GetDocumentContent(ctx, docID)
			if err != nil {
				out = append(out, types.FetchedItem{
					ExternalID:       docID,
					Title:            node.displayName(),
					SourceResourceID: resourceID,
					Metadata: map[string]string{
						"channel": types.ChannelDingtalk,
						"error":   err.Error(),
					},
				})
				continue
			}
			out = append(out, fetchedItemFromNode(resourceID, spaceID, node, detail))
		}

		if incremental && prev != nil && prev.SpaceNodeTimes != nil {
			if prevTimes, ok := prev.SpaceNodeTimes[resourceID]; ok {
				for prevDocID := range prevTimes {
					if !currentDocs[prevDocID] {
						out = append(out, types.FetchedItem{
							ExternalID:       prevDocID,
							IsDeleted:        true,
							SourceResourceID: resourceID,
							Metadata: map[string]string{
								"channel": types.ChannelDingtalk,
							},
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

func syncNodesForResource(ctx context.Context, cli *client, spaceID, nodeID string) ([]docNode, error) {
	if nodeID == "" {
		return listNodesRecursive(ctx, cli, spaceID, "")
	}

	node, found, err := findNodeByID(ctx, cli, spaceID, nodeID)
	if err != nil {
		return nil, err
	}
	if found {
		if !isFolderNode(node) && isDocumentNode(node) {
			return []docNode{node}, nil
		}
		return listNodesRecursive(ctx, cli, spaceID, node.id())
	}

	nodes, err := listNodesRecursive(ctx, cli, spaceID, nodeID)
	if err != nil {
		return nil, err
	}
	if len(nodes) > 0 {
		return nodes, nil
	}
	return []docNode{{NodeID: nodeID, DocumentID: nodeID, Type: "document"}}, nil
}

func listNodesRecursive(ctx context.Context, cli *client, spaceID, nodeID string) ([]docNode, error) {
	nodes, err := cli.ListNodes(ctx, spaceID, nodeID)
	if err != nil {
		return nil, err
	}
	out := make([]docNode, 0, len(nodes))
	for _, node := range nodes {
		out = append(out, node)
		if isFolderNode(node) {
			children, err := listNodesRecursive(ctx, cli, spaceID, node.id())
			if err != nil {
				return out, err
			}
			out = append(out, children...)
		}
	}
	return out, nil
}

func findNodeByID(ctx context.Context, cli *client, spaceID, wanted string) (docNode, bool, error) {
	nodes, err := cli.ListNodes(ctx, spaceID, "")
	if err != nil {
		return docNode{}, false, err
	}
	return findNodeInTree(ctx, cli, spaceID, strings.TrimSpace(wanted), nodes)
}

func findNodeInTree(ctx context.Context, cli *client, spaceID, wanted string, nodes []docNode) (docNode, bool, error) {
	for _, node := range nodes {
		if node.id() == wanted || node.docID() == wanted {
			return node, true, nil
		}
		if !isFolderNode(node) {
			continue
		}
		children, err := cli.ListNodes(ctx, spaceID, node.id())
		if err != nil {
			return docNode{}, false, err
		}
		found, ok, err := findNodeInTree(ctx, cli, spaceID, wanted, children)
		if err != nil || ok {
			return found, ok, err
		}
	}
	return docNode{}, false, nil
}

func ancestorPathForNode(ctx context.Context, cli *client, spaceID, wanted string) ([]string, error) {
	nodes, err := cli.ListNodes(ctx, spaceID, "")
	if err != nil {
		return nil, err
	}
	path, _, err := ancestorPathInTree(ctx, cli, spaceID, strings.TrimSpace(wanted), nil, nodes)
	return path, err
}

func ancestorPathInTree(
	ctx context.Context,
	cli *client,
	spaceID string,
	wanted string,
	path []string,
	nodes []docNode,
) ([]string, bool, error) {
	for _, node := range nodes {
		if node.id() == wanted || node.docID() == wanted {
			return path, true, nil
		}
		if !isFolderNode(node) {
			continue
		}
		children, err := cli.ListNodes(ctx, spaceID, node.id())
		if err != nil {
			return nil, false, err
		}
		nextPath := append(append([]string{}, path...), makeResourceID(spaceID, node.id()))
		foundPath, ok, err := ancestorPathInTree(ctx, cli, spaceID, wanted, nextPath, children)
		if err != nil || ok {
			return foundPath, ok, err
		}
	}
	return nil, false, nil
}

func nodeToResource(spaceID, parentID string, node docNode) types.Resource {
	nodeID := node.id()
	resourceType := "document"
	hasChildren := false
	if isFolderNode(node) {
		resourceType = "folder"
		hasChildren = true
	}
	return types.Resource{
		ExternalID:  makeResourceID(spaceID, nodeID),
		Name:        node.displayName(),
		Type:        resourceType,
		URL:         node.URL,
		ModifiedAt:  parseDingTalkTime(node.updated()),
		ParentID:    parentID,
		HasChildren: hasChildren || node.HasChildren,
		Metadata: map[string]interface{}{
			"space_id": spaceID,
			"node_id":  nodeID,
		},
	}
}

func fetchedItemFromNode(resourceID, spaceID string, node docNode, detail contentResponse) types.FetchedItem {
	title := firstNonEmpty(detail.Title, node.displayName(), "Untitled")
	updatedAt := firstNonEmpty(detail.UpdatedAt, node.updated())
	url := firstNonEmpty(detail.URL, node.URL)
	docID := firstNonEmpty(detail.DocumentID, node.docID())
	return types.FetchedItem{
		ExternalID:       docID,
		Title:            title,
		Content:          []byte(detail.body()),
		ContentType:      "text/markdown",
		FileName:         sanitizeFileName(title) + ".md",
		URL:              url,
		UpdatedAt:        parseDingTalkTime(updatedAt),
		SourceResourceID: resourceID,
		Metadata: map[string]string{
			"channel":     types.ChannelDingtalk,
			"space_id":    spaceID,
			"document_id": docID,
			"node_id":     node.id(),
			"format":      detail.Format,
		},
	}
}

func parseResourceID(resourceID string) (string, string) {
	parts := strings.SplitN(resourceID, ":", 2)
	if len(parts) == 1 {
		return strings.TrimSpace(parts[0]), ""
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
}

func makeResourceID(spaceID, nodeID string) string {
	if nodeID == "" {
		return spaceID
	}
	return spaceID + ":" + nodeID
}

func isFolderNode(node docNode) bool {
	t := strings.ToLower(strings.TrimSpace(node.Type))
	return node.HasChildren || t == "folder" || t == "directory" || t == "catalog"
}

func isDocumentNode(node docNode) bool {
	t := strings.ToLower(strings.TrimSpace(node.Type))
	return t == "" || t == "document" || t == "doc" || t == "docx" || t == "markdown"
}
