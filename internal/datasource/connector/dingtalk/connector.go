package dingtalk

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

// maxDepth bounds tree traversal so a cyclic or pathologically deep wiki cannot
// hang a sync worker.
const maxDepth = 32

const (
	nodeResourcePrefix       = "dingtalk-node:"
	maxOpaqueResourceIDBytes = 16 * 1024
	maxResourceComponentSize = 1024
)

type nodeResourceRef struct {
	WorkspaceID string   `json:"w"`
	Path        []string `json:"p"`
}

var errSelectedResourceMissing = errors.New("selected DingTalk resource no longer exists")

// Connector implements datasource.Connector for DingTalk wiki (知识库).
type Connector struct {
	clientFactory func(*config) (*client, error)
}

var _ datasource.CursorAwareFullSyncConnector = (*Connector)(nil)

// NewConnector creates a DingTalk connector.
func NewConnector() *Connector {
	return &Connector{clientFactory: newClient}
}

func (c *Connector) makeClient(cfg *config) (*client, error) {
	if c != nil && c.clientFactory != nil {
		return c.clientFactory(cfg)
	}
	return newClient(cfg)
}

// Type returns the connector type identifier.
func (c *Connector) Type() string {
	return types.ConnectorTypeDingTalk
}

// Validate verifies credentials by listing workspaces.
func (c *Connector) Validate(ctx context.Context, config *types.DataSourceConfig) error {
	cfg, err := parseConfig(config)
	if err != nil {
		return err
	}
	cli, err := c.makeClient(cfg)
	if err != nil {
		return err
	}
	if _, err := cli.listWorkspaces(ctx, ""); err != nil {
		return err
	}
	return nil
}

// ListResources lists workspaces (parentID == "") or the children of a node.
func (c *Connector) ListResources(
	ctx context.Context, config *types.DataSourceConfig, parentID string,
) ([]types.Resource, error) {
	cfg, err := parseConfig(config)
	if err != nil {
		return nil, err
	}
	cli, err := c.makeClient(cfg)
	if err != nil {
		return nil, err
	}

	if parentID == "" {
		spaces, err := cli.listAllWorkspaces(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]types.Resource, 0, len(spaces))
		for _, ws := range spaces {
			out = append(out, types.Resource{
				ExternalID:  ws.WorkspaceID,
				Name:        ws.Name,
				Type:        "space",
				URL:         safeSourceURL(ws.URL),
				HasChildren: true,
				Metadata: map[string]interface{}{
					"root_node_id": ws.RootNodeID,
					"channel":      types.ConnectorTypeDingTalk,
				},
			})
		}
		return out, nil
	}

	parentNodeID, workspaceID, err := c.resolveContainer(ctx, cli, parentID)
	if err != nil {
		return nil, err
	}
	parentPath := []string(nil)
	if ref, encoded, parseErr := parseNodeResourceID(parentID); parseErr != nil {
		return nil, parseErr
	} else if encoded {
		parentPath = ref.Path
	}

	children, err := cli.listAllChildren(ctx, workspaceID, parentNodeID)
	if err != nil {
		return nil, err
	}
	out := make([]types.Resource, 0, len(children))
	for i := range children {
		n := &children[i]
		if n.WorkspaceID != "" && n.WorkspaceID != workspaceID {
			return nil, fmt.Errorf(
				"%w: DingTalk returned a node from a different workspace",
				datasource.ErrInvalidConfig,
			)
		}
		if n.WorkspaceID == "" {
			n.WorkspaceID = workspaceID
		}
		kind := "unsupported"
		if n.isSupportedDocument() {
			kind = "document"
		} else if n.canDescend() {
			kind = "folder"
		}
		path := append(append([]string(nil), parentPath...), n.NodeID)
		out = append(out, types.Resource{
			ExternalID:  makeNodeResourceID(workspaceID, path),
			Name:        n.Name,
			Type:        kind,
			URL:         safeSourceURL(n.URL),
			ModifiedAt:  n.lastModified(),
			ParentID:    parentID,
			HasChildren: n.canDescend(),
			Metadata: map[string]interface{}{
				"workspace_id": workspaceID,
				"node_id":      n.NodeID,
				"channel":      types.ConnectorTypeDingTalk,
			},
		})
	}
	return out, nil
}

// resolveContainer maps a picker resource ID to (nodeID, workspaceID). A workspace
// ID resolves to that workspace's root node; anything else is treated as a node.
func (c *Connector) resolveContainer(
	ctx context.Context, cli *client, resourceID string,
) (nodeID string, workspaceID string, err error) {
	nodeID, workspaceID, _, err = c.resolveSelection(ctx, cli, resourceID)
	return nodeID, workspaceID, err
}

// resolveSelection returns the selected node when resourceID addresses a node.
// Workspace selections return a nil selected node and resolve to their root.
func (c *Connector) resolveSelection(
	ctx context.Context, cli *client, resourceID string,
) (nodeID string, workspaceID string, selected *node, err error) {
	ref, encoded, parseErr := parseNodeResourceID(resourceID)
	if parseErr != nil {
		return "", "", nil, parseErr
	}
	if encoded {
		nodeID := ref.Path[len(ref.Path)-1]
		detail, err := cli.getNodeDetail(ctx, nodeID)
		if err != nil {
			if isNotFoundAPIError(err) {
				missing, fallbackDetail, verifyErr := resolveEncodedPath(ctx, cli, ref)
				if verifyErr != nil {
					return "", "", nil, verifyErr
				}
				if missing {
					return "", ref.WorkspaceID, nil, errSelectedResourceMissing
				}
				detail = fallbackDetail
			} else {
				return "", "", nil, err
			}
		}
		if detail == nil {
			return "", "", nil, fmt.Errorf(
				"%w: DingTalk resource path resolved without node detail",
				datasource.ErrInvalidConfig,
			)
		}
		if detail.NodeID != nodeID || detail.WorkspaceID != ref.WorkspaceID {
			return "", "", nil, fmt.Errorf(
				"%w: DingTalk resource does not belong to its encoded workspace",
				datasource.ErrInvalidConfig,
			)
		}
		return nodeID, ref.WorkspaceID, detail, nil
	}

	spaces, err := cli.listAllWorkspaces(ctx)
	if err != nil {
		return "", "", nil, err
	}
	for _, ws := range spaces {
		if ws.WorkspaceID == resourceID {
			return ws.RootNodeID, ws.WorkspaceID, nil, nil
		}
	}
	detail, err := cli.getNodeDetail(ctx, resourceID)
	if err != nil {
		if isNotFoundAPIError(err) {
			// listAllWorkspaces completed successfully above. A raw legacy
			// workspace/node selection that is absent from both that listing and
			// the detail endpoint is verified missing, not merely unreachable.
			// Let the normal two-complete-empty-snapshot policy reconcile its
			// previous documents instead of carrying them forever.
			return "", "", nil, errSelectedResourceMissing
		}
		return "", "", nil, err
	}
	return detail.NodeID, detail.WorkspaceID, detail, nil
}

// resolveEncodedPath verifies an unavailable node against complete parent
// listings. A successful listing that omits one path component is trustworthy
// absence evidence; API failures remain incomplete observations and therefore
// cannot drive deletion detection.
func resolveEncodedPath(
	ctx context.Context,
	cli *client,
	ref nodeResourceRef,
) (missing bool, selected *node, err error) {
	workspaces, err := cli.listAllWorkspaces(ctx)
	if err != nil {
		return false, nil, err
	}
	rootNodeID := ""
	for _, ws := range workspaces {
		if ws.WorkspaceID == ref.WorkspaceID {
			rootNodeID = ws.RootNodeID
			break
		}
	}
	if rootNodeID == "" {
		return true, nil, nil
	}

	parentNodeID := rootNodeID
	for _, expectedNodeID := range ref.Path {
		children, err := cli.listAllChildren(ctx, ref.WorkspaceID, parentNodeID)
		if err != nil {
			return false, nil, err
		}
		var found *node
		for i := range children {
			if children[i].NodeID != expectedNodeID {
				continue
			}
			if children[i].WorkspaceID != "" &&
				children[i].WorkspaceID != ref.WorkspaceID {
				return false, nil, fmt.Errorf(
					"%w: DingTalk resource path crossed workspaces",
					datasource.ErrInvalidConfig,
				)
			}
			children[i].WorkspaceID = ref.WorkspaceID
			found = &children[i]
			break
		}
		if found == nil {
			return true, nil, nil
		}
		selected = found
		parentNodeID = expectedNodeID
	}
	return false, selected, nil
}

// ResolveResourceAncestors reconstructs picker ancestors from the opaque,
// versioned resource IDs emitted by ListResources. DingTalk node details expose
// no parent pointer, so retaining the lazy-load path in the ID avoids a full
// workspace traversal every time an existing data source is edited.
func (c *Connector) ResolveResourceAncestors(
	ctx context.Context, config *types.DataSourceConfig, resourceIDs []string,
) ([]string, error) {
	seen := make(map[string]bool)
	ancestors := make([]string, 0)
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			ancestors = append(ancestors, id)
		}
	}

	for _, resourceID := range resourceIDs {
		ref, encoded, err := parseNodeResourceID(resourceID)
		if err != nil {
			return nil, err
		}
		if !encoded {
			// Workspace IDs are already top-level. Legacy raw node IDs do not
			// contain enough information to reconstruct their parent chain.
			continue
		}
		add(ref.WorkspaceID)
		for depth := 1; depth < len(ref.Path); depth++ {
			add(makeNodeResourceID(ref.WorkspaceID, ref.Path[:depth]))
		}
	}
	return ancestors, nil
}

func makeNodeResourceID(workspaceID string, path []string) string {
	ref := nodeResourceRef{WorkspaceID: workspaceID, Path: path}
	data, _ := json.Marshal(ref)
	return nodeResourcePrefix + base64.RawURLEncoding.EncodeToString(data)
}

func parseNodeResourceID(resourceID string) (nodeResourceRef, bool, error) {
	if !strings.HasPrefix(resourceID, nodeResourcePrefix) {
		return nodeResourceRef{}, false, nil
	}
	invalid := func() (nodeResourceRef, bool, error) {
		return nodeResourceRef{}, true, fmt.Errorf(
			"%w: malformed DingTalk resource identifier",
			datasource.ErrInvalidConfig,
		)
	}
	if len(resourceID) <= len(nodeResourcePrefix) ||
		len(resourceID) > maxOpaqueResourceIDBytes {
		return invalid()
	}
	data, err := base64.RawURLEncoding.DecodeString(resourceID[len(nodeResourcePrefix):])
	if err != nil {
		return invalid()
	}
	var ref nodeResourceRef
	if err := json.Unmarshal(data, &ref); err != nil ||
		ref.WorkspaceID == "" ||
		len(ref.Path) == 0 ||
		len(ref.Path) > maxDepth+1 ||
		!validResourceComponent(ref.WorkspaceID) {
		return invalid()
	}
	seen := make(map[string]struct{}, len(ref.Path))
	for _, nodeID := range ref.Path {
		if !validResourceComponent(nodeID) {
			return invalid()
		}
		if _, duplicate := seen[nodeID]; duplicate {
			return invalid()
		}
		seen[nodeID] = struct{}{}
	}
	return ref, true, nil
}

func validResourceComponent(value string) bool {
	if value == "" || len(value) > maxResourceComponentSize {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// FetchAll performs a full sync of the selected resources.
func (c *Connector) FetchAll(
	ctx context.Context, config *types.DataSourceConfig, resourceIDs []string,
) ([]types.FetchedItem, error) {
	if len(resourceIDs) == 0 {
		return nil, fmt.Errorf("%w: no resource IDs configured", datasource.ErrInvalidConfig)
	}
	items, _, err := c.walk(ctx, config, resourceIDs, nil, false, false)
	return items, err
}

// FetchAllWithCursor re-fetches the complete current snapshot while using the
// prior snapshot solely for deletion reconciliation. This preserves full-sync
// semantics without leaving removed resources or old credential identities
// behind indefinitely.
func (c *Connector) FetchAllWithCursor(
	ctx context.Context,
	config *types.DataSourceConfig,
	resourceIDs []string,
	cursor *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	if len(resourceIDs) == 0 {
		return nil, nil, fmt.Errorf("%w: no resource IDs configured", datasource.ErrInvalidConfig)
	}
	items, next, err := c.walk(
		ctx,
		config,
		resourceIDs,
		decodeSyncState(cursor),
		true,
		false,
	)
	return items, encodeSyncState(next), err
}

// FetchIncremental syncs only what changed since the cursor was written.
func (c *Connector) FetchIncremental(
	ctx context.Context, config *types.DataSourceConfig, cursor *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	resourceIDs := config.ResourceIDs
	if len(resourceIDs) == 0 {
		return nil, nil, fmt.Errorf("%w: no resource IDs configured", datasource.ErrInvalidConfig)
	}

	items, next, err := c.walk(
		ctx,
		config,
		resourceIDs,
		decodeSyncState(cursor),
		true,
		true,
	)
	return items, encodeSyncState(next), err
}

func decodeSyncState(cursor *types.SyncCursor) *syncState {
	if cursor == nil || cursor.ConnectorCursor == nil {
		return nil
	}
	var state syncState
	data, err := json.Marshal(cursor.ConnectorCursor)
	if err != nil || json.Unmarshal(data, &state) != nil {
		return nil
	}
	return &state
}

func encodeSyncState(state *syncState) *types.SyncCursor {
	if state == nil {
		return nil
	}
	cursorMap := make(map[string]interface{})
	data, err := json.Marshal(state)
	if err != nil || json.Unmarshal(data, &cursorMap) != nil {
		return nil
	}
	return &types.SyncCursor{
		LastSyncTime:    state.LastSyncTime,
		ConnectorCursor: cursorMap,
	}
}
