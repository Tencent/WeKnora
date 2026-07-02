package dingtalk

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
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
	cli := newClient(cfg)
	if _, err := cli.operatorUnionID(ctx); err != nil {
		return fmt.Errorf("dingtalk connection failed: %w", err)
	}
	if _, err := cli.ListWorkspaces(ctx); err != nil {
		return fmt.Errorf("dingtalk workspace permission check failed: %w", err)
	}
	return nil
}

func (c *Connector) ListResources(
	ctx context.Context,
	config *types.DataSourceConfig,
	parentID string,
) ([]types.Resource, error) {
	cfg, err := parseDingTalkConfig(config)
	if err != nil {
		return nil, err
	}
	cli := newClient(cfg)

	if parentID == "" {
		workspaces, err := cli.ListWorkspaces(ctx)
		if err != nil {
			return nil, err
		}
		resources := make([]types.Resource, 0, len(workspaces))
		for _, ws := range workspaces {
			rootNodeID := ws.RootNodeID
			if rootNodeID == "" {
				rootNodeID = ws.WorkspaceID
			}
			resources = append(resources, types.Resource{
				ExternalID:  makeResourceID(ws.WorkspaceID, rootNodeID),
				Name:        fallbackName(ws.Name, "Untitled workspace"),
				Type:        "workspace",
				Description: ws.Description,
				URL:         ws.URL,
				ModifiedAt:  parseDingTalkTime(ws.ModifiedTime),
				HasChildren: true,
				Metadata: map[string]interface{}{
					"workspace_id": ws.WorkspaceID,
					"root_node_id": rootNodeID,
				},
			})
		}
		sortResources(resources)
		return resources, nil
	}

	workspaceID, parentNodeID, err := parseResourceID(parentID)
	if err != nil {
		return nil, err
	}
	nodes, err := cli.ListNodes(ctx, parentNodeID)
	if err != nil {
		return nil, err
	}
	resources := make([]types.Resource, 0, len(nodes))
	for _, node := range nodes {
		resources = append(resources, nodeToResource(workspaceID, parentID, node))
	}
	sortResources(resources)
	return resources, nil
}

func (c *Connector) ResolveResourceAncestors(
	ctx context.Context,
	config *types.DataSourceConfig,
	resourceIDs []string,
) ([]string, error) {
	cfg, err := parseDingTalkConfig(config)
	if err != nil {
		return nil, err
	}
	cli := newClient(cfg)

	ancestors := map[string]struct{}{}
	for _, resourceID := range resourceIDs {
		workspaceID, nodeID, err := parseResourceID(resourceID)
		if err != nil {
			return nil, err
		}
		for depth := 0; depth < 64 && nodeID != ""; depth++ {
			node, err := cli.GetNode(ctx, nodeID)
			if err != nil {
				return nil, err
			}
			parentID := node.parentNodeID()
			if parentID == "" {
				break
			}
			ancestors[makeResourceID(workspaceID, parentID)] = struct{}{}
			nodeID = parentID
		}
	}

	out := make([]string, 0, len(ancestors))
	for id := range ancestors {
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}

func (c *Connector) FetchAll(
	ctx context.Context,
	config *types.DataSourceConfig,
	resourceIDs []string,
) ([]types.FetchedItem, error) {
	items, _, err := c.walk(ctx, config, resourceIDs, nil, false)
	return items, err
}

func (c *Connector) FetchIncremental(
	ctx context.Context,
	config *types.DataSourceConfig,
	cursor *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	resourceIDs := config.ResourceIDs
	if len(resourceIDs) == 0 {
		return nil, nil, fmt.Errorf("%w: no DingTalk resource IDs configured", datasource.ErrInvalidConfig)
	}

	var prev *dingtalkCursor
	if cursor != nil && cursor.ConnectorCursor != nil {
		var decoded dingtalkCursor
		b, _ := json.Marshal(cursor.ConnectorCursor)
		_ = json.Unmarshal(b, &decoded)
		prev = &decoded
	}

	items, next, err := c.walk(ctx, config, resourceIDs, prev, true)
	if err != nil {
		return nil, nil, err
	}

	cursorMap := make(map[string]interface{})
	b, _ := json.Marshal(next)
	_ = json.Unmarshal(b, &cursorMap)
	return items, &types.SyncCursor{
		LastSyncTime:    next.LastSyncTime,
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
	cfg, err := parseDingTalkConfig(config)
	if err != nil {
		return nil, nil, err
	}
	cli := newClient(cfg)

	next := &dingtalkCursor{
		LastSyncTime:      time.Now(),
		ResourceDocTimes:  make(map[string]map[string]string),
		ResourceDocHashes: make(map[string]map[string]string),
	}
	var out []types.FetchedItem
	seenDocs := make(map[string]bool)

	for _, selectedResourceID := range resourceIDs {
		workspaceID, nodeID, err := parseResourceID(selectedResourceID)
		if err != nil {
			return nil, nil, err
		}

		docs, err := collectDocuments(ctx, cli, nodeID, map[string]bool{})
		if err != nil {
			return nil, nil, fmt.Errorf("collect dingtalk documents for %s: %w", selectedResourceID, err)
		}

		currentDocs := make(map[string]bool)
		next.ResourceDocTimes[selectedResourceID] = make(map[string]string)
		next.ResourceDocHashes[selectedResourceID] = make(map[string]string)

		for _, doc := range docs {
			docResourceID := makeResourceID(workspaceID, doc.NodeID)
			modified := doc.modifiedTime()
			currentDocs[docResourceID] = true
			next.ResourceDocTimes[selectedResourceID][docResourceID] = modified

			if seenDocs[docResourceID] {
				continue
			}
			seenDocs[docResourceID] = true

			blocks, err := cli.QueryDocBlocks(ctx, doc.docKey())
			if err != nil {
				out = append(out, types.FetchedItem{
					ExternalID:       docResourceID,
					Title:            doc.displayName(),
					SourceResourceID: selectedResourceID,
					Metadata: map[string]string{
						"channel":      types.ChannelDingtalk,
						"workspace_id": workspaceID,
						"node_id":      doc.NodeID,
						"doc_key":      doc.docKey(),
						"error":        err.Error(),
					},
				})
				continue
			}

			content, hash, err := renderDocumentContent(blocks)
			if err != nil {
				out = append(out, types.FetchedItem{
					ExternalID:       docResourceID,
					Title:            doc.displayName(),
					SourceResourceID: selectedResourceID,
					Metadata: map[string]string{
						"channel":      types.ChannelDingtalk,
						"workspace_id": workspaceID,
						"node_id":      doc.NodeID,
						"doc_key":      doc.docKey(),
						"error":        err.Error(),
					},
				})
				continue
			}
			next.ResourceDocHashes[selectedResourceID][docResourceID] = hash

			if incremental && prev != nil && prev.ResourceDocTimes != nil && prev.ResourceDocHashes != nil {
				prevTime := prev.ResourceDocTimes[selectedResourceID][docResourceID]
				prevHash := prev.ResourceDocHashes[selectedResourceID][docResourceID]
				if prevTime == modified && prevHash != "" && prevHash == hash {
					continue
				}
			}

			out = append(out, types.FetchedItem{
				ExternalID:       docResourceID,
				Title:            doc.displayName(),
				Content:          content,
				ContentType:      "text/markdown",
				FileName:         markdownFileName(doc.displayName()),
				URL:              doc.URL,
				UpdatedAt:        parseDingTalkTime(modified),
				SourceResourceID: selectedResourceID,
				Metadata: map[string]string{
					"channel":      types.ChannelDingtalk,
					"workspace_id": workspaceID,
					"node_id":      doc.NodeID,
					"doc_key":      doc.docKey(),
					"category":     doc.Category,
					"content_hash": hash,
				},
			})
		}

		if incremental && prev != nil && prev.ResourceDocTimes != nil {
			if prevTimes, ok := prev.ResourceDocTimes[selectedResourceID]; ok {
				for prevDocID := range prevTimes {
					if !currentDocs[prevDocID] {
						out = append(out, types.FetchedItem{
							ExternalID:       prevDocID,
							IsDeleted:        true,
							SourceResourceID: selectedResourceID,
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
	return out, next, nil
}

func renderDocumentContent(blocks []docBlock) ([]byte, string, error) {
	content := []byte(renderBlocksMarkdown(blocks))
	if strings.TrimSpace(string(content)) == "" {
		return nil, "", fmt.Errorf("dingtalk document content is empty after markdown rendering")
	}
	return content, contentHash(content), nil
}

func contentHash(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func collectDocuments(ctx context.Context, cli *client, nodeID string, visited map[string]bool) ([]wikiNode, error) {
	if visited[nodeID] {
		return nil, nil
	}
	visited[nodeID] = true

	node, err := cli.GetNode(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	if !node.isFolder() {
		if node.isSupportedDocument() {
			return []wikiNode{node}, nil
		}
		return nil, nil
	}

	children, err := cli.ListNodes(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	var docs []wikiNode
	for _, child := range children {
		if child.isFolder() {
			nested, err := collectDocuments(ctx, cli, child.NodeID, visited)
			if err != nil {
				return nil, err
			}
			docs = append(docs, nested...)
			continue
		}
		if !child.isSupportedDocument() {
			continue
		}
		docs = append(docs, child)
	}
	return docs, nil
}

func nodeToResource(workspaceID, parentID string, node wikiNode) types.Resource {
	resourceType := "document"
	if node.isFolder() {
		resourceType = "folder"
	}
	return types.Resource{
		ExternalID:  makeResourceID(workspaceID, node.NodeID),
		Name:        node.displayName(),
		Type:        resourceType,
		URL:         node.URL,
		ModifiedAt:  parseDingTalkTime(node.modifiedTime()),
		ParentID:    parentID,
		HasChildren: node.isFolder(),
		Metadata: map[string]interface{}{
			"workspace_id": workspaceID,
			"node_id":      node.NodeID,
			"category":     node.Category,
			"doc_key":      node.docKey(),
		},
	}
}

func sortResources(resources []types.Resource) {
	sort.Slice(resources, func(i, j int) bool {
		if resources[i].Type != resources[j].Type {
			return resources[i].Type < resources[j].Type
		}
		return resources[i].ExternalID < resources[j].ExternalID
	})
}

func fallbackName(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
