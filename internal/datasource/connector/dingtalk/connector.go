package dingtalk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	cursorVersion     = 2
	maxTraversalNodes = 1_000_000
)

var (
	_ datasource.Connector          = (*Connector)(nil)
	_ datasource.FullSyncWithCursor = (*Connector)(nil)
)

type apiFactory func(*config) dingTalkAPI

// Connector imports native DingTalk documents through the Wiki and Blocks APIs.
type Connector struct {
	newAPI apiFactory
}

// NewConnector creates a DingTalk data source connector.
func NewConnector() *Connector {
	return &Connector{newAPI: func(cfg *config) dingTalkAPI { return newClient(cfg) }}
}

func (c *Connector) api(cfg *config) dingTalkAPI {
	if c != nil && c.newAPI != nil {
		return c.newAPI(cfg)
	}
	return newClient(cfg)
}

// Type returns the registered data source type.
func (c *Connector) Type() string {
	return types.ConnectorTypeDingTalk
}

// Validate checks the application credentials and operator access, including
// node listing and a sample document read when one is visible at the workspace root.
func (c *Connector) Validate(ctx context.Context, dataSourceConfig *types.DataSourceConfig) error {
	cfg, err := parseConfig(dataSourceConfig)
	if err != nil {
		return err
	}
	api := c.api(cfg)
	workspaces, err := api.listWorkspaces(ctx)
	if err != nil {
		return fmt.Errorf("validate DingTalk data source: %w", err)
	}
	for _, item := range workspaces {
		rootNodeID := strings.TrimSpace(item.RootNodeID)
		if rootNodeID == "" {
			continue
		}
		children, err := api.listNodes(ctx, rootNodeID)
		if err != nil {
			return fmt.Errorf("validate DingTalk data source: %w", err)
		}
		for _, child := range children {
			if !child.isDocument() {
				continue
			}
			if _, err := api.documentBlocks(ctx, child.ID); err != nil {
				return fmt.Errorf("validate DingTalk data source: %w", err)
			}
			return nil
		}
		return nil
	}
	return nil
}

// ListResources lazily lists selectable workspaces, folders and documents.
func (c *Connector) ListResources(
	ctx context.Context,
	dataSourceConfig *types.DataSourceConfig,
	parentID string,
) ([]types.Resource, error) {
	cfg, err := parseConfig(dataSourceConfig)
	if err != nil {
		return nil, err
	}
	api := c.api(cfg)
	if strings.TrimSpace(parentID) == "" {
		workspaces, err := api.listWorkspaces(ctx)
		if err != nil {
			return nil, err
		}
		resources := make([]types.Resource, 0, len(workspaces))
		for _, item := range workspaces {
			if strings.TrimSpace(item.ID) == "" {
				continue
			}
			resourceID, err := encodeResourceReference(resourceReference{WorkspaceID: item.ID})
			if err != nil {
				return nil, err
			}
			name := strings.TrimSpace(item.Name)
			if name == "" {
				name = item.ID
			}
			resources = append(resources, types.Resource{
				ExternalID:  resourceID,
				Name:        name,
				Type:        "wiki_space",
				Description: item.Description,
				URL:         item.URL,
				ModifiedAt:  parseDingTalkTime(item.ModifiedTime),
				HasChildren: strings.TrimSpace(item.RootNodeID) != "",
				Metadata: map[string]interface{}{
					"workspace_id": item.ID,
				},
			})
		}
		sortResources(resources)
		return resources, nil
	}

	parentRef, err := decodeResourceReference(parentID)
	if err != nil {
		return nil, err
	}
	parentNodeID := parentRef.NodeID
	if parentNodeID == "" {
		workspaces, err := api.listWorkspaces(ctx)
		if err != nil {
			return nil, err
		}
		item, exists := workspaceByID(workspaces, parentRef.WorkspaceID)
		if !exists {
			return nil, fmt.Errorf("%w: DingTalk workspace %q is unavailable",
				datasource.ErrResourceNotFound, parentRef.WorkspaceID)
		}
		parentNodeID = strings.TrimSpace(item.RootNodeID)
		if parentNodeID == "" {
			return []types.Resource{}, nil
		}
	} else {
		workspaces, err := api.listWorkspaces(ctx)
		if err != nil {
			return nil, err
		}
		scopes, failures, err := resolveSyncScopes(ctx, api, workspaces, []string{parentID})
		if err != nil {
			return nil, err
		}
		if failure := failures[parentID]; failure != nil {
			return nil, failure
		}
		if len(scopes) != 1 || scopes[0].Document != nil {
			return nil, fmt.Errorf("%w: DingTalk resource %q is not an expandable folder",
				datasource.ErrResourceNotFound, parentID)
		}
		parentNodeID = scopes[0].StartNodeID
	}

	children, err := api.listNodes(ctx, parentNodeID)
	if err != nil {
		return nil, err
	}
	resources := make([]types.Resource, 0, len(children))
	for _, child := range children {
		if !child.isFolder() && !child.isDocument() {
			continue
		}
		if child.WorkspaceID != "" && child.WorkspaceID != parentRef.WorkspaceID {
			return nil, fmt.Errorf("DingTalk node %q belongs to a different workspace", child.ID)
		}
		childRef := parentRef.child(child.ID)
		resourceID, err := encodeResourceReference(childRef)
		if err != nil {
			return nil, err
		}
		resourceType := "document"
		if child.isFolder() {
			resourceType = "folder"
		}
		resources = append(resources, types.Resource{
			ExternalID:  resourceID,
			Name:        child.title(),
			Type:        resourceType,
			URL:         child.URL,
			ModifiedAt:  child.modifiedAt(),
			ParentID:    parentID,
			HasChildren: child.isFolder(),
			Metadata: map[string]interface{}{
				"workspace_id": parentRef.WorkspaceID,
				"node_id":      child.ID,
				"category":     child.Category,
				"extension":    child.Extension,
			},
		})
	}
	sortResources(resources)
	return resources, nil
}

// ResolveResourceAncestors restores the paths embedded in saved selections.
func (c *Connector) ResolveResourceAncestors(
	ctx context.Context,
	dataSourceConfig *types.DataSourceConfig,
	resourceIDs []string,
) ([]string, error) {
	if _, err := parseConfig(dataSourceConfig); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	var ancestors []string
	for _, resourceID := range resourceIDs {
		ref, err := decodeResourceReference(resourceID)
		if err != nil {
			return nil, err
		}
		ids, err := resourceAncestorIDs(ref)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			if _, exists := seen[id]; exists {
				continue
			}
			seen[id] = struct{}{}
			ancestors = append(ancestors, id)
		}
	}
	return ancestors, nil
}

func sortResources(resources []types.Resource) {
	sort.SliceStable(resources, func(i, j int) bool {
		left, right := strings.ToLower(resources[i].Name), strings.ToLower(resources[j].Name)
		if left == right {
			return resources[i].ExternalID < resources[j].ExternalID
		}
		return left < right
	})
}

func workspaceByID(workspaces []workspace, workspaceID string) (workspace, bool) {
	for _, item := range workspaces {
		if item.ID == workspaceID {
			return item, true
		}
	}
	return workspace{}, false
}

// FetchAll reads every supported document in the selected scopes.
func (c *Connector) FetchAll(
	ctx context.Context,
	dataSourceConfig *types.DataSourceConfig,
	resourceIDs []string,
) ([]types.FetchedItem, error) {
	items, _, err := c.sync(ctx, dataSourceConfig, resourceIDs, nil, syncMode{})
	return items, err
}

// FetchAllFromCursor re-fetches every document and reconciles deletions against
// the previous cursor so a scheduled full sync still honours deletion_sync.
func (c *Connector) FetchAllFromCursor(
	ctx context.Context,
	dataSourceConfig *types.DataSourceConfig,
	resourceIDs []string,
	cursor *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	if dataSourceConfig == nil {
		return nil, nil, fmt.Errorf("%w: config is nil", datasource.ErrInvalidConfig)
	}
	previous, err := decodeCursor(cursor)
	if err != nil {
		return nil, nil, err
	}
	return c.syncAndEncodeCursor(
		ctx, dataSourceConfig, resourceIDs, previous,
		syncMode{reconcileDeletions: true},
	)
}

// FetchIncremental reads changed documents and reconciles complete selections.
func (c *Connector) FetchIncremental(
	ctx context.Context,
	dataSourceConfig *types.DataSourceConfig,
	cursor *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	if dataSourceConfig == nil {
		return nil, nil, fmt.Errorf("%w: config is nil", datasource.ErrInvalidConfig)
	}
	previous, err := decodeCursor(cursor)
	if err != nil {
		return nil, nil, err
	}
	return c.syncAndEncodeCursor(
		ctx, dataSourceConfig, dataSourceConfig.ResourceIDs, previous,
		syncMode{skipUnchanged: true, reconcileDeletions: true},
	)
}

func (c *Connector) syncAndEncodeCursor(
	ctx context.Context,
	dataSourceConfig *types.DataSourceConfig,
	resourceIDs []string,
	previous *cursorState,
	mode syncMode,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	items, next, syncErr := c.sync(ctx, dataSourceConfig, resourceIDs, previous, mode)
	if next == nil {
		return items, nil, syncErr
	}
	encoded, err := encodeCursor(next)
	if err != nil {
		return nil, nil, err
	}
	return items, &types.SyncCursor{
		LastSyncTime:    next.SyncedAt,
		ConnectorCursor: encoded,
	}, syncErr
}

type cursorState struct {
	Version    int                          `json:"version"`
	SyncedAt   time.Time                    `json:"synced_at"`
	Resources  map[string]map[string]string `json:"resources"`
	Workspaces map[string]map[string]string `json:"workspaces,omitempty"`
}

type syncMode struct {
	skipUnchanged      bool
	reconcileDeletions bool
}

type syncScope struct {
	ResourceID  string
	Reference   resourceReference
	StartNodeID string
	Document    *node
}

func (s syncScope) contains(candidate syncScope) bool {
	if s.Reference.WorkspaceID != candidate.Reference.WorkspaceID {
		return false
	}
	if s.Reference.NodeID == "" {
		return true
	}
	if s.Document != nil {
		return s.Reference.NodeID == candidate.Reference.NodeID
	}
	if s.Reference.NodeID == candidate.Reference.NodeID {
		return true
	}
	for _, ancestor := range candidate.Reference.Ancestors {
		if ancestor == s.Reference.NodeID {
			return true
		}
	}
	return false
}

func (c *Connector) sync(
	ctx context.Context,
	dataSourceConfig *types.DataSourceConfig,
	resourceIDs []string,
	previous *cursorState,
	mode syncMode,
) ([]types.FetchedItem, *cursorState, error) {
	cfg, err := parseConfig(dataSourceConfig)
	if err != nil {
		return nil, nil, err
	}
	selected := uniqueIDs(resourceIDs)
	if len(selected) == 0 {
		return nil, nil, errors.New("no DingTalk resources selected")
	}

	api := c.api(cfg)
	workspaces, err := api.listWorkspaces(ctx)
	if err != nil {
		return nil, nil, err
	}
	scopes, failures, err := resolveSyncScopes(ctx, api, workspaces, selected)
	if err != nil {
		return nil, nil, err
	}

	next := &cursorState{
		Version:   cursorVersion,
		SyncedAt:  time.Now().UTC(),
		Resources: make(map[string]map[string]string, len(scopes)),
	}
	var items []types.FetchedItem
	failedDocuments := 0
	complete := len(failures) == 0
	seenDocuments := make(map[string]struct{})
	type deletionCandidate struct {
		resourceID string
		documentID string
		revision   string
	}
	var deletions []deletionCandidate
	for _, resourceID := range selected {
		if failure := failures[resourceID]; failure != nil {
			if previous != nil {
				next.Resources[resourceID] = cloneRevisions(previous.Resources[resourceID])
			}
			items = append(items, failedResource(resourceID, failure))
		}
	}

	for _, scope := range scopes {
		oldRevisions := map[string]string{}
		if previous != nil {
			if stored := previous.Resources[scope.ResourceID]; stored != nil {
				oldRevisions = stored
			}
		}
		documents, err := scanScope(ctx, api, scope)
		if err != nil {
			if isContextError(err) {
				return nil, nil, err
			}
			// Never infer deletions from an incomplete tree. Other independent
			// selections may still complete, while this scope keeps its previous
			// cursor and is retried on the next run.
			complete = false
			next.Resources[scope.ResourceID] = cloneRevisions(oldRevisions)
			items = append(items, failedResource(scope.ResourceID, err))
			continue
		}
		newRevisions := make(map[string]string, len(documents))
		currentDocuments := make(map[string]struct{}, len(documents))

		for _, document := range documents {
			if document.ID == "" {
				continue
			}
			seenDocuments[document.ID] = struct{}{}
			currentDocuments[document.ID] = struct{}{}
			revision := document.revision()
			oldRevision, existed := oldRevisions[document.ID]
			if mode.skipUnchanged && revision != "" && existed && revision == oldRevision {
				newRevisions[document.ID] = revision
				continue
			}

			blocks, err := api.documentBlocks(ctx, document.ID)
			if err != nil {
				if isContextError(err) {
					return nil, nil, err
				}
				items = append(items, failedDocument(
					scope.ResourceID, scope.Reference.WorkspaceID, document, err,
				))
				failedDocuments++
				if existed {
					// Do not advance failed documents. The next incremental run
					// must retry them even if modifiedTime remains unchanged.
					newRevisions[document.ID] = oldRevision
				}
				continue
			}
			rendered := renderDocument(document.title(), blocks)
			items = append(items, fetchedDocument(
				scope.ResourceID, scope.Reference.WorkspaceID, document, rendered,
			))
			newRevisions[document.ID] = revision
		}

		if mode.reconcileDeletions {
			for documentID, revision := range oldRevisions {
				if _, exists := currentDocuments[documentID]; exists {
					continue
				}
				deletions = append(deletions, deletionCandidate{scope.ResourceID, documentID, revision})
			}
		}
		next.Resources[scope.ResourceID] = newRevisions
	}

	// Reconcile the union of all selections. Moving a document between two
	// selected folders must never generate both an upsert and a deletion.
	// An unavailable scope could contain a moved document, so defer deletions
	// and retain their revisions until every scope can be scanned again.
	sort.Slice(deletions, func(i, j int) bool { return deletions[i].documentID < deletions[j].documentID })
	deleted := make(map[string]struct{})
	for _, candidate := range deletions {
		if _, visible := seenDocuments[candidate.documentID]; visible {
			continue
		}
		if !complete {
			next.Resources[candidate.resourceID][candidate.documentID] = candidate.revision
			continue
		}
		if _, exists := deleted[candidate.documentID]; exists {
			continue
		}
		deleted[candidate.documentID] = struct{}{}
		items = append(items, types.FetchedItem{
			ExternalID: candidate.documentID, IsDeleted: true, SourceResourceID: candidate.resourceID,
		})
	}
	if !mode.skipUnchanged && !mode.reconcileDeletions {
		next = nil
	}
	if !complete || failedDocuments > 0 {
		// Failure items carry localized reason codes. Returning the same raw
		// diagnostics in Details would duplicate them as untranslated UI text.
		return items, next, &datasource.PartialFetchError{}
	}
	return items, next, nil
}

func resolveSyncScopes(
	ctx context.Context,
	api dingTalkAPI,
	workspaces []workspace,
	resourceIDs []string,
) ([]syncScope, map[string]error, error) {
	byID := make(map[string]workspace, len(workspaces))
	for _, item := range workspaces {
		byID[item.ID] = item
	}
	childrenCache := make(map[string][]node)
	listChildren := func(parentNodeID string) ([]node, error) {
		if cached, exists := childrenCache[parentNodeID]; exists {
			return cached, nil
		}
		children, err := api.listNodes(ctx, parentNodeID)
		if err != nil {
			return nil, err
		}
		childrenCache[parentNodeID] = children
		return children, nil
	}

	resolve := func(resourceID string) (syncScope, error) {
		ref, err := decodeResourceReference(resourceID)
		if err != nil {
			return syncScope{}, err
		}
		canonicalID, err := encodeResourceReference(ref)
		if err != nil {
			return syncScope{}, err
		}
		item, exists := byID[ref.WorkspaceID]
		if !exists {
			return syncScope{}, fmt.Errorf("%w: DingTalk workspace %q is unavailable",
				datasource.ErrResourceNotFound, ref.WorkspaceID)
		}
		rootNodeID := strings.TrimSpace(item.RootNodeID)
		if rootNodeID == "" {
			return syncScope{}, fmt.Errorf("DingTalk workspace %q has no root node", ref.WorkspaceID)
		}
		if ref.NodeID == "" {
			return syncScope{
				ResourceID: canonicalID, Reference: ref, StartNodeID: rootNodeID,
			}, nil
		}

		parentNodeID := rootNodeID
		for _, ancestorID := range ref.Ancestors {
			children, err := listChildren(parentNodeID)
			if err != nil {
				return syncScope{}, fmt.Errorf("resolve DingTalk resource path: %w", err)
			}
			ancestor, exists := childByID(children, ancestorID)
			if !exists || !ancestor.isFolder() {
				return syncScope{}, fmt.Errorf("%w: DingTalk ancestor %q is unavailable",
					datasource.ErrResourceNotFound, ancestorID)
			}
			if ancestor.WorkspaceID != "" && ancestor.WorkspaceID != ref.WorkspaceID {
				return syncScope{}, fmt.Errorf("DingTalk ancestor %q belongs to a different workspace", ancestorID)
			}
			parentNodeID = ancestor.ID
		}
		children, err := listChildren(parentNodeID)
		if err != nil {
			return syncScope{}, fmt.Errorf("resolve DingTalk resource: %w", err)
		}
		selectedNode, exists := childByID(children, ref.NodeID)
		if !exists {
			return syncScope{}, fmt.Errorf("%w: DingTalk node %q is unavailable",
				datasource.ErrResourceNotFound, ref.NodeID)
		}
		if selectedNode.WorkspaceID != "" && selectedNode.WorkspaceID != ref.WorkspaceID {
			return syncScope{}, fmt.Errorf("DingTalk node %q belongs to a different workspace", ref.NodeID)
		}
		switch {
		case selectedNode.isFolder():
			return syncScope{
				ResourceID: canonicalID, Reference: ref, StartNodeID: selectedNode.ID,
			}, nil
		case selectedNode.isDocument():
			document := selectedNode
			return syncScope{
				ResourceID: canonicalID, Reference: ref, Document: &document,
			}, nil
		default:
			return syncScope{}, fmt.Errorf("DingTalk node %q is not a supported online document or folder",
				ref.NodeID)
		}
	}
	var scopes []syncScope
	failures := make(map[string]error)
	for _, resourceID := range resourceIDs {
		scope, err := resolve(resourceID)
		if err != nil {
			if isContextError(err) {
				return nil, nil, err
			}
			failures[resourceID] = err
			continue
		}
		scopes = append(scopes, scope)
	}

	sort.SliceStable(scopes, func(i, j int) bool {
		leftDepth := len(scopes[i].Reference.Ancestors)
		rightDepth := len(scopes[j].Reference.Ancestors)
		if scopes[i].Reference.NodeID != "" {
			leftDepth++
		}
		if scopes[j].Reference.NodeID != "" {
			rightDepth++
		}
		if leftDepth == rightDepth {
			return scopes[i].ResourceID < scopes[j].ResourceID
		}
		return leftDepth < rightDepth
	})
	compacted := make([]syncScope, 0, len(scopes))
	for _, scope := range scopes {
		covered := false
		for _, parent := range compacted {
			if parent.contains(scope) {
				covered = true
				break
			}
		}
		if !covered {
			compacted = append(compacted, scope)
		}
	}
	return compacted, failures, nil
}

func childByID(children []node, nodeID string) (node, bool) {
	for _, child := range children {
		if child.ID == nodeID {
			return child, true
		}
	}
	return node{}, false
}

func scanScope(ctx context.Context, api dingTalkAPI, scope syncScope) ([]node, error) {
	if scope.Document != nil {
		return []node{*scope.Document}, nil
	}
	return scanWorkspace(ctx, api, scope.Reference.WorkspaceID, scope.StartNodeID)
}

func scanWorkspace(
	ctx context.Context,
	api dingTalkAPI,
	workspaceID string,
	rootNodeID string,
) ([]node, error) {
	queue := []string{rootNodeID}
	visitedParents := make(map[string]struct{})
	seenNodes := make(map[string]struct{})
	var documents []node

	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		parentID := queue[0]
		queue = queue[1:]
		if _, visited := visitedParents[parentID]; visited {
			continue
		}
		visitedParents[parentID] = struct{}{}

		children, err := api.listNodes(ctx, parentID)
		if err != nil {
			return nil, err
		}
		for _, child := range children {
			if child.ID == "" {
				continue
			}
			if child.WorkspaceID != "" && child.WorkspaceID != workspaceID {
				return nil, fmt.Errorf("DingTalk node %q belongs to a different workspace", child.ID)
			}
			if _, seen := seenNodes[child.ID]; seen {
				continue
			}
			seenNodes[child.ID] = struct{}{}
			if len(seenNodes) > maxTraversalNodes {
				return nil, fmt.Errorf("DingTalk workspace exceeds %d nodes", maxTraversalNodes)
			}
			if child.isDocument() {
				documents = append(documents, child)
			}
			if child.isFolder() || child.HasChildren {
				queue = append(queue, child.ID)
			}
		}
	}
	sort.SliceStable(documents, func(i, j int) bool {
		return documents[i].ID < documents[j].ID
	})
	return documents, nil
}

func cloneRevisions(revisions map[string]string) map[string]string {
	cloned := make(map[string]string, len(revisions))
	for documentID, revision := range revisions {
		cloned[documentID] = revision
	}
	return cloned
}

type renderResult struct {
	Markdown     string
	UnknownTypes []string
}

func fetchedDocument(
	sourceResourceID string,
	workspaceID string,
	document node,
	rendered renderResult,
) types.FetchedItem {
	metadata := map[string]string{
		"channel":      types.ChannelDingtalk,
		"workspace_id": workspaceID,
		"node_id":      document.ID,
		"category":     document.Category,
		"extension":    document.Extension,
	}
	if len(rendered.UnknownTypes) > 0 {
		metadata["unknown_block_types"] = strings.Join(rendered.UnknownTypes, ",")
	}
	documentURL := strings.TrimSpace(document.URL)
	if documentURL == "" {
		documentURL = "https://alidocs.dingtalk.com/i/nodes/" + url.PathEscape(document.ID)
	}
	return types.FetchedItem{
		ExternalID:       document.ID,
		Title:            document.title(),
		Content:          []byte(rendered.Markdown),
		ContentType:      "text/markdown",
		FileName:         sanitizeFilename(document.title()) + ".md",
		URL:              documentURL,
		UpdatedAt:        document.modifiedAt(),
		Metadata:         metadata,
		SourceResourceID: sourceResourceID,
	}
}

func failedDocument(
	sourceResourceID string,
	workspaceID string,
	document node,
	err error,
) types.FetchedItem {
	return types.FetchedItem{
		ExternalID:       document.ID,
		Title:            document.title(),
		SourceResourceID: sourceResourceID,
		Metadata: map[string]string{
			"channel":           types.ChannelDingtalk,
			"workspace_id":      workspaceID,
			"node_id":           document.ID,
			"error":             err.Error(),
			"error_reason_code": "dingtalk_document_failed",
			"error_reason":      "DingTalk document could not be read; retry on the next sync",
		},
	}
}

func failedResource(resourceID string, err error) types.FetchedItem {
	return types.FetchedItem{
		ExternalID:       "dingtalk-resource:" + resourceID,
		SourceResourceID: resourceID,
		Metadata: map[string]string{
			"channel":           types.ChannelDingtalk,
			"error":             err.Error(),
			"error_reason_code": "dingtalk_resource_failed",
			"error_reason":      "DingTalk resource is unavailable; check access and the saved selection, then retry",
		},
	}
}

func decodeCursor(cursor *types.SyncCursor) (*cursorState, error) {
	empty := &cursorState{
		Version:   cursorVersion,
		Resources: make(map[string]map[string]string),
	}
	if cursor == nil || cursor.ConnectorCursor == nil {
		return empty, nil
	}
	raw, err := json.Marshal(cursor.ConnectorCursor)
	if err != nil {
		return nil, fmt.Errorf("marshal DingTalk cursor: %w", err)
	}
	var decoded cursorState
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("decode DingTalk cursor: %w", err)
	}
	switch decoded.Version {
	case 1:
		decoded.Resources = make(map[string]map[string]string, len(decoded.Workspaces))
		for workspaceID, revisions := range decoded.Workspaces {
			decoded.Resources[workspaceID] = cloneRevisions(revisions)
		}
		decoded.Workspaces = nil
		decoded.Version = cursorVersion
	case cursorVersion:
	default:
		return nil, fmt.Errorf("unsupported DingTalk cursor version %d", decoded.Version)
	}
	if decoded.Resources == nil {
		decoded.Resources = make(map[string]map[string]string)
	}
	return &decoded, nil
}

func encodeCursor(cursor *cursorState) (map[string]interface{}, error) {
	raw, err := json.Marshal(cursor)
	if err != nil {
		return nil, fmt.Errorf("marshal DingTalk cursor: %w", err)
	}
	var encoded map[string]interface{}
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, fmt.Errorf("encode DingTalk cursor: %w", err)
	}
	return encoded, nil
}

func uniqueIDs(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func parseDingTalkTime(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02T15:04Z07:00",
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04Z",
	} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed
		}
	}
	return time.Time{}
}

func sanitizeFilename(name string) string {
	name = strings.TrimSpace(name)
	name = strings.Map(func(value rune) rune {
		if unicode.IsControl(value) {
			return '_'
		}
		return value
	}, name)
	name = strings.NewReplacer(
		"/", "_", "\\", "_", ":", "_", "*", "_", "?", "_",
		"\"", "_", "<", "_", ">", "_", "|", "_",
	).Replace(name)
	name = strings.Trim(name, " ._")
	if name == "" {
		return "untitled"
	}

	const maxBytes = 200
	if len(name) > maxBytes {
		name = name[:maxBytes]
		for len(name) > 0 {
			r, size := utf8.DecodeLastRuneInString(name)
			if r != utf8.RuneError || size != 1 {
				break
			}
			name = name[:len(name)-1]
		}
		name = strings.Trim(name, " ._")
	}
	if name == "" {
		return "untitled"
	}
	return name
}
