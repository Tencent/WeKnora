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
	cfg, err := parseConfig(config)
	if err != nil {
		return err
	}
	cli := newClient(cfg)
	if err := cli.Ping(ctx); err != nil {
		return fmt.Errorf("dingtalk connection failed: %w", err)
	}
	workspaces, err := cli.ListWorkspaces(ctx)
	if err != nil {
		return fmt.Errorf("list dingtalk workspaces failed: %w", err)
	}
	for _, ws := range workspaces {
		rootNodeID := strings.TrimSpace(ws.RootNodeID)
		if rootNodeID == "" {
			continue
		}
		if _, err := cli.ListNodes(ctx, ws.normalizedID(), rootNodeID); err != nil {
			return fmt.Errorf("list dingtalk nodes failed: %w", err)
		}
		break
	}
	return nil
}

func (c *Connector) ResolveResourceAncestors(
	ctx context.Context, config *types.DataSourceConfig, resourceIDs []string,
) ([]string, error) {
	seen := make(map[string]struct{})
	var out []string
	for _, id := range resourceIDs {
		ref, err := parseResourceRef(id)
		if err != nil {
			return nil, err
		}
		if ref.Kind == "workspace" {
			continue
		}
		ancestor := workspaceExternalID(ref.WorkspaceID)
		if _, ok := seen[ancestor]; ok {
			continue
		}
		seen[ancestor] = struct{}{}
		out = append(out, ancestor)
	}
	return out, nil
}

func (c *Connector) ListResources(
	ctx context.Context, config *types.DataSourceConfig, parentID string,
) ([]types.Resource, error) {
	cfg, err := parseConfig(config)
	if err != nil {
		return nil, err
	}
	cli := newClient(cfg)

	if parentID == "" {
		workspaces, err := cli.ListWorkspaces(ctx)
		if err != nil {
			return nil, fmt.Errorf("list dingtalk workspaces: %w", err)
		}
		out := make([]types.Resource, 0, len(workspaces))
		for _, ws := range workspaces {
			workspaceID := ws.normalizedID()
			if workspaceID == "" {
				continue
			}
			out = append(out, types.Resource{
				ExternalID:  workspaceExternalIDWithRoot(workspaceID, ws.RootNodeID),
				Name:        ws.displayName(),
				Type:        "workspace",
				Description: ws.Description,
				URL:         ws.URL,
				ModifiedAt:  parseTime(ws.modifiedAtText()),
				HasChildren: true,
				Metadata: map[string]interface{}{
					"workspace_id": workspaceID,
					"root_node_id": ws.RootNodeID,
				},
			})
		}
		return out, nil
	}

	ref, err := parseResourceRef(parentID)
	if err != nil {
		return nil, err
	}
	if ref.Kind == "doc" {
		return []types.Resource{}, nil
	}

	parentNodeID := ref.NodeID
	if ref.Kind == "workspace" && parentNodeID == "" {
		ws, err := cli.GetWorkspace(ctx, ref.WorkspaceID)
		if err != nil {
			return nil, fmt.Errorf("get dingtalk workspace: %w", err)
		}
		parentNodeID = ws.RootNodeID
	}
	if ref.Kind == "folder" {
		parentNodeID = ref.NodeID
	}
	nodes, err := cli.ListNodes(ctx, ref.WorkspaceID, parentNodeID)
	if err != nil {
		return nil, fmt.Errorf("list dingtalk nodes: %w", err)
	}
	out := make([]types.Resource, 0, len(nodes))
	for _, n := range nodes {
		res, ok := nodeResource(ref.WorkspaceID, parentID, n)
		if ok {
			out = append(out, res)
		}
	}
	return out, nil
}

func nodeResource(workspaceID, parentExternalID string, n node) (types.Resource, bool) {
	nodeID := n.normalizedID()
	if nodeID == "" {
		return types.Resource{}, false
	}
	nodeType := n.normalizedType()
	var externalID string
	var hasChildren bool
	switch {
	case n.isFolder():
		nodeType = "folder"
		externalID = folderExternalID(workspaceID, nodeID)
		hasChildren = true
	case n.isDocument():
		nodeType = "document"
		externalID = docExternalID(workspaceID, nodeID)
	default:
		return types.Resource{}, false
	}
	return types.Resource{
		ExternalID:  externalID,
		Name:        n.displayName(),
		Type:        nodeType,
		URL:         n.URL,
		ModifiedAt:  parseTime(n.modifiedAtText()),
		ParentID:    parentExternalID,
		HasChildren: hasChildren || n.HasChildren,
		Metadata: map[string]interface{}{
			"workspace_id": workspaceID,
			"node_id":      nodeID,
		},
	}, true
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
	var prev *dingtalkCursor
	if cursor != nil && cursor.ConnectorCursor != nil {
		var decoded dingtalkCursor
		b, _ := json.Marshal(cursor.ConnectorCursor)
		_ = json.Unmarshal(b, &decoded)
		prev = &decoded
	}

	items, next, err := c.walk(ctx, config, config.ResourceIDs, prev, true)
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

type documentSummary struct {
	WorkspaceID      string
	DocID            string
	ExternalID       string
	Title            string
	URL              string
	UpdatedSignal    string
	UpdatedAt        time.Time
	SourceResourceID string
}

func (c *Connector) walk(
	ctx context.Context,
	config *types.DataSourceConfig,
	resourceIDs []string,
	prev *dingtalkCursor,
	incremental bool,
) ([]types.FetchedItem, *dingtalkCursor, error) {
	cfg, err := parseConfig(config)
	if err != nil {
		return nil, nil, err
	}
	if len(resourceIDs) == 0 {
		resourceIDs = config.ResourceIDs
	}
	if len(resourceIDs) == 0 {
		return nil, nil, fmt.Errorf("no resource IDs configured")
	}

	cli := newClient(cfg)
	docs, err := c.collectDocuments(ctx, cli, resourceIDs)
	if err != nil {
		return nil, nil, err
	}

	next := &dingtalkCursor{
		LastSyncTime: time.Now().UTC(),
		DocTimes:     make(map[string]string, len(docs)),
	}
	current := make(map[string]struct{}, len(docs))
	out := make([]types.FetchedItem, 0, len(docs))

	for _, doc := range docs {
		current[doc.ExternalID] = struct{}{}
		next.DocTimes[doc.ExternalID] = doc.UpdatedSignal
		if incremental && prev != nil && prev.DocTimes != nil && doc.UpdatedSignal != "" {
			if prev.DocTimes[doc.ExternalID] == doc.UpdatedSignal {
				continue
			}
		}

		detail, err := cli.GetDocument(ctx, doc.DocID)
		if err != nil {
			out = append(out, types.FetchedItem{
				ExternalID:       doc.ExternalID,
				Title:            doc.Title,
				SourceResourceID: doc.SourceResourceID,
				Metadata: map[string]string{
					"error":        err.Error(),
					"channel":      types.ChannelDingtalk,
					"doc_id":       doc.DocID,
					"workspace_id": doc.WorkspaceID,
				},
			})
			continue
		}

		title := detail.displayTitle(doc.Title)
		content := detail.markdown()
		if strings.TrimSpace(content) == "" {
			content = fallbackMarkdown(title, firstNonEmpty(detail.URL, doc.URL))
		}
		updatedText := firstNonEmpty(detail.updatedAtText(), doc.UpdatedSignal)
		cursorSignal := doc.UpdatedSignal
		if cursorSignal == "" {
			cursorSignal = updatedText
		}
		if cursorSignal != "" {
			next.DocTimes[doc.ExternalID] = cursorSignal
		}
		updatedAt := parseTime(updatedText)
		if updatedAt.IsZero() {
			updatedAt = doc.UpdatedAt
		}

		out = append(out, types.FetchedItem{
			ExternalID:       doc.ExternalID,
			Title:            title,
			Content:          []byte(content),
			ContentType:      "text/markdown",
			FileName:         sanitizeFileName(title) + ".md",
			URL:              firstNonEmpty(detail.URL, doc.URL),
			UpdatedAt:        updatedAt,
			SourceResourceID: doc.SourceResourceID,
			Metadata: map[string]string{
				"channel":      types.ChannelDingtalk,
				"doc_id":       doc.DocID,
				"workspace_id": doc.WorkspaceID,
			},
		})
	}

	if incremental && prev != nil && prev.DocTimes != nil {
		for prevID := range prev.DocTimes {
			if _, ok := current[prevID]; !ok {
				out = append(out, types.FetchedItem{
					ExternalID: prevID,
					IsDeleted:  true,
					Metadata: map[string]string{
						"channel": types.ChannelDingtalk,
					},
				})
			}
		}
	}

	return out, next, nil
}

func (c *Connector) collectDocuments(
	ctx context.Context,
	cli *client,
	resourceIDs []string,
) ([]documentSummary, error) {
	seen := make(map[string]struct{})
	var out []documentSummary
	for _, resourceID := range resourceIDs {
		docs, err := c.collectResourceDocuments(ctx, cli, resourceID)
		if err != nil {
			return nil, err
		}
		for _, doc := range docs {
			if _, ok := seen[doc.ExternalID]; ok {
				continue
			}
			seen[doc.ExternalID] = struct{}{}
			out = append(out, doc)
		}
	}
	return out, nil
}

func (c *Connector) collectResourceDocuments(
	ctx context.Context,
	cli *client,
	resourceID string,
) ([]documentSummary, error) {
	ref, err := parseResourceRef(resourceID)
	if err != nil {
		return nil, err
	}
	switch ref.Kind {
	case "workspace":
		rootNodeID := ref.NodeID
		if rootNodeID == "" {
			ws, err := cli.GetWorkspace(ctx, ref.WorkspaceID)
			if err != nil {
				return nil, fmt.Errorf("get workspace %s: %w", ref.WorkspaceID, err)
			}
			rootNodeID = ws.RootNodeID
		}
		return c.collectDocumentsUnder(ctx, cli, ref.WorkspaceID, rootNodeID, resourceID, make(map[string]struct{}))
	case "folder":
		return c.collectDocumentsUnder(ctx, cli, ref.WorkspaceID, ref.NodeID, resourceID, make(map[string]struct{}))
	case "doc":
		return []documentSummary{{
			WorkspaceID:      ref.WorkspaceID,
			DocID:            ref.NodeID,
			ExternalID:       docExternalID(ref.WorkspaceID, ref.NodeID),
			SourceResourceID: resourceID,
		}}, nil
	default:
		return nil, fmt.Errorf("unsupported dingtalk resource kind %q", ref.Kind)
	}
}

func fallbackMarkdown(title, docURL string) string {
	if strings.TrimSpace(docURL) == "" {
		return "# " + title + "\n"
	}
	return "# " + title + "\n\nSource: " + docURL + "\n"
}

func (c *Connector) collectDocumentsUnder(
	ctx context.Context,
	cli *client,
	workspaceID, parentNodeID, sourceResourceID string,
	visitedFolders map[string]struct{},
) ([]documentSummary, error) {
	if parentNodeID != "" {
		key := workspaceID + ":" + parentNodeID
		if _, ok := visitedFolders[key]; ok {
			return nil, nil
		}
		visitedFolders[key] = struct{}{}
	}

	nodes, err := cli.ListNodes(ctx, workspaceID, parentNodeID)
	if err != nil {
		return nil, fmt.Errorf("list nodes workspace=%s parent=%s: %w", workspaceID, parentNodeID, err)
	}

	var out []documentSummary
	for _, n := range nodes {
		nodeID := n.normalizedID()
		if nodeID == "" {
			continue
		}
		if n.isFolder() {
			children, err := c.collectDocumentsUnder(ctx, cli, workspaceID, nodeID, sourceResourceID, visitedFolders)
			if err != nil {
				return nil, err
			}
			out = append(out, children...)
			continue
		}
		if !n.isDocument() {
			continue
		}
		updatedText := n.modifiedAtText()
		out = append(out, documentSummary{
			WorkspaceID:      workspaceID,
			DocID:            nodeID,
			ExternalID:       docExternalID(workspaceID, nodeID),
			Title:            n.displayName(),
			URL:              n.URL,
			UpdatedSignal:    updatedText,
			UpdatedAt:        parseTime(updatedText),
			SourceResourceID: sourceResourceID,
		})
	}
	return out, nil
}
