package dingtalk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

var _ datasource.Connector = (*Connector)(nil)

type clientFactory func(*Config) dingTalkAPI

type Connector struct {
	newClient clientFactory
}

func NewConnector() *Connector {
	return &Connector{newClient: func(cfg *Config) dingTalkAPI { return newClient(cfg) }}
}

func (c *Connector) api(cfg *Config) dingTalkAPI {
	if c != nil && c.newClient != nil {
		return c.newClient(cfg)
	}
	return newClient(cfg)
}

func (c *Connector) Type() string { return types.ConnectorTypeDingTalk }

func (c *Connector) Validate(ctx context.Context, config *types.DataSourceConfig) error {
	cfg, err := parseDingTalkConfig(config)
	if err != nil {
		return err
	}
	if err := c.api(cfg).Ping(ctx); err != nil {
		return fmt.Errorf("dingtalk connection failed: %w", err)
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
	api := c.api(cfg)

	if strings.TrimSpace(parentID) == "" {
		workspaces, err := api.ListWorkspaces(ctx)
		if err != nil {
			return nil, fmt.Errorf("list dingtalk workspaces: %w", err)
		}
		resources := make([]types.Resource, 0, len(workspaces))
		for _, item := range workspaces {
			if strings.TrimSpace(item.WorkspaceID) == "" {
				continue
			}
			ref := workspaceResourceRef(item.WorkspaceID)
			externalID, err := encodeResourceRef(ref)
			if err != nil {
				return nil, err
			}
			name := strings.TrimSpace(item.Name)
			if name == "" {
				name = item.WorkspaceID
			}
			resources = append(resources, types.Resource{
				ExternalID:  externalID,
				Name:        name,
				Type:        "wiki_space",
				Description: item.Description,
				URL:         item.URL,
				ModifiedAt:  parseDingTalkTime(item.ModifiedTime),
				HasChildren: item.RootNodeID != "",
				Metadata: map[string]interface{}{
					"workspace_id":    item.WorkspaceID,
					"workspace_type":  item.Type,
					"permission_role": item.PermissionRole,
				},
			})
		}
		sortResources(resources)
		return resources, nil
	}

	parentRef, err := decodeResourceRef(parentID)
	if err != nil {
		return nil, fmt.Errorf("parse dingtalk parent resource: %w", err)
	}
	parentNodeID := parentRef.NodeID
	if parentNodeID == "" {
		item, err := api.GetWorkspace(ctx, parentRef.WorkspaceID)
		if err != nil {
			return nil, err
		}
		parentNodeID = item.RootNodeID
		if parentNodeID == "" {
			return []types.Resource{}, nil
		}
	}

	nodes, err := api.ListNodes(ctx, parentNodeID)
	if err != nil {
		return nil, err
	}
	resources := make([]types.Resource, 0, len(nodes))
	for _, node := range nodes {
		if !isFolderNode(node) && !isDocumentNode(node) {
			continue
		}
		resource, err := nodeToResource(parentRef, parentID, node)
		if err != nil {
			return nil, err
		}
		resources = append(resources, resource)
	}
	sortResources(resources)
	return resources, nil
}

func (c *Connector) ResolveResourceAncestors(
	ctx context.Context,
	config *types.DataSourceConfig,
	resourceIDs []string,
) ([]string, error) {
	if _, err := parseDingTalkConfig(config); err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	unique := make(map[string]struct{})
	for _, resourceID := range resourceIDs {
		ref, err := decodeResourceRef(resourceID)
		if err != nil {
			return nil, fmt.Errorf("parse selected dingtalk resource: %w", err)
		}
		ancestors, err := ancestorResourceIDs(ref)
		if err != nil {
			return nil, err
		}
		for _, ancestor := range ancestors {
			unique[ancestor] = struct{}{}
		}
	}
	out := make([]string, 0, len(unique))
	for resourceID := range unique {
		out = append(out, resourceID)
	}
	sort.Strings(out)
	return out, nil
}

func (c *Connector) FetchAll(
	ctx context.Context,
	config *types.DataSourceConfig,
	resourceIDs []string,
) ([]types.FetchedItem, error) {
	items, _, err := c.sync(ctx, config, resourceIDs, nil, false)
	return items, err
}

func (c *Connector) FetchIncremental(
	ctx context.Context,
	config *types.DataSourceConfig,
	cursor *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	if config == nil || len(config.ResourceIDs) == 0 {
		return nil, nil, fmt.Errorf("no resource IDs configured for dingtalk sync")
	}
	previous, err := decodeDingTalkCursor(cursor)
	if err != nil {
		return nil, nil, err
	}
	items, next, syncErr := c.sync(ctx, config, config.ResourceIDs, previous, true)
	if syncErr != nil {
		var partial *datasource.PartialFetchError
		if !errors.As(syncErr, &partial) {
			return nil, nil, syncErr
		}
	}
	connectorCursor, err := encodeDingTalkCursor(next)
	if err != nil {
		return nil, nil, err
	}
	return items, &types.SyncCursor{
		LastSyncTime:    next.SyncedAt,
		ConnectorCursor: connectorCursor,
	}, syncErr
}

type branchFailure struct {
	ParentNodeID string
	Err          error
}

type traversalResult struct {
	Nodes    []wikiNode
	Failures []branchFailure
	Complete bool
}

func (c *Connector) sync(
	ctx context.Context,
	config *types.DataSourceConfig,
	resourceIDs []string,
	previous *dingTalkCursor,
	incremental bool,
) ([]types.FetchedItem, *dingTalkCursor, error) {
	cfg, err := parseDingTalkConfig(config)
	if err != nil {
		return nil, nil, err
	}
	if len(resourceIDs) == 0 {
		return nil, nil, fmt.Errorf("no resource IDs configured for dingtalk sync")
	}
	api := c.api(cfg)
	next := &dingTalkCursor{
		Version:   cursorVersion,
		SyncedAt:  time.Now().UTC(),
		Resources: make(map[string]dingTalkResourceState, len(resourceIDs)),
	}
	var items []types.FetchedItem
	var warnings []string
	seenResourceIDs := make(map[string]struct{}, len(resourceIDs))

	for _, resourceID := range resourceIDs {
		resourceID = strings.TrimSpace(resourceID)
		if _, exists := seenResourceIDs[resourceID]; exists {
			continue
		}
		seenResourceIDs[resourceID] = struct{}{}
		ref, err := decodeResourceRef(resourceID)
		if err != nil {
			return nil, nil, fmt.Errorf("parse dingtalk sync resource %q: %w", resourceID, err)
		}
		previousState := dingTalkResourceState{Nodes: map[string]dingTalkNodeState{}}
		if previous != nil {
			if state, exists := previous.Resources[resourceID]; exists {
				previousState = state
				if previousState.Nodes == nil {
					previousState.Nodes = map[string]dingTalkNodeState{}
				}
			}
		}

		result, resolveErr := resolveAndTraverse(ctx, api, ref)
		if resolveErr != nil {
			if isContextError(resolveErr) {
				return nil, nil, resolveErr
			}
			items = append(items, resourceFailureItem(resourceID, ref, "resolve_resource", resolveErr))
			warnings = append(warnings, dingtalkFailureMessage(resolveErr))
			next.Resources[resourceID] = cloneResourceState(previousState)
			continue
		}

		state := dingTalkResourceState{Nodes: make(map[string]dingTalkNodeState)}
		if !result.Complete {
			state = cloneResourceState(previousState)
		}
		for _, failure := range result.Failures {
			items = append(items, branchFailureItem(resourceID, ref, failure))
			warnings = append(warnings, dingtalkFailureMessage(failure.Err))
		}

		currentDocuments := make(map[string]struct{})
		seenNodes := make(map[string]struct{}, len(result.Nodes))
		for _, node := range result.Nodes {
			if node.NodeID == "" || !isDocumentNode(node) {
				continue
			}
			if _, exists := seenNodes[node.NodeID]; exists {
				continue
			}
			seenNodes[node.NodeID] = struct{}{}
			currentDocuments[node.NodeID] = struct{}{}

			currentNodeState := dingTalkNodeState{ModifiedTime: node.ModifiedTime}
			previousNodeState, existed := previousState.Nodes[node.NodeID]
			if incremental && existed && node.ModifiedTime != "" && previousNodeState.ModifiedTime == node.ModifiedTime {
				state.Nodes[node.NodeID] = currentNodeState
				continue
			}

			blocks, err := api.GetDocumentBlocks(ctx, node.NodeID)
			if err != nil {
				if isContextError(err) {
					return nil, nil, err
				}
				items = append(items, documentFailureItem(resourceID, ref, node, err))
				warnings = append(warnings, dingtalkFailureMessage(err))
				// Do not advance the cursor for a failed changed/new document, so
				// the next incremental sync retries its content.
				if existed {
					state.Nodes[node.NodeID] = previousNodeState
				}
				continue
			}

			rendered := renderDocumentMarkdown(nodeDisplayName(node), blocks)
			items = append(items, fetchedDocumentItem(resourceID, ref, node, rendered))
			state.Nodes[node.NodeID] = currentNodeState
		}

		if incremental && result.Complete {
			for nodeID := range previousState.Nodes {
				if _, exists := currentDocuments[nodeID]; !exists {
					items = append(items, types.FetchedItem{
						ExternalID:       nodeID,
						IsDeleted:        true,
						SourceResourceID: resourceID,
					})
				}
			}
		}
		next.Resources[resourceID] = state
		logger.Infof(ctx, "[DingTalk] resource sync resource_id=%s nodes=%d documents=%d complete=%t failures=%d",
			redact(resourceID), len(result.Nodes), len(currentDocuments), result.Complete, len(result.Failures))
	}

	if len(warnings) > 0 {
		partial := &datasource.PartialFetchError{Details: warnings}
		if !incremental {
			return items, nil, partial
		}
		return items, next, partial
	}
	if !incremental {
		return items, nil, nil
	}
	return items, next, nil
}

func resolveAndTraverse(ctx context.Context, api dingTalkAPI, ref resourceRef) (traversalResult, error) {
	if ref.NodeID == "" {
		item, err := api.GetWorkspace(ctx, ref.WorkspaceID)
		if err != nil {
			return traversalResult{}, err
		}
		if item.RootNodeID == "" {
			return traversalResult{Complete: true}, nil
		}
		return traverseNodes(ctx, api, item.RootNodeID)
	}

	node, err := api.GetNode(ctx, ref.NodeID)
	if err != nil {
		return traversalResult{}, err
	}
	if node.WorkspaceID != "" && node.WorkspaceID != ref.WorkspaceID {
		return traversalResult{}, fmt.Errorf("dingtalk node %s belongs to workspace %s, not %s", node.NodeID, node.WorkspaceID, ref.WorkspaceID)
	}
	if isDocumentNode(node) {
		return traversalResult{Nodes: []wikiNode{node}, Complete: true}, nil
	}
	if !isFolderNode(node) {
		return traversalResult{}, fmt.Errorf("dingtalk node %s is not a supported document or folder", node.NodeID)
	}
	return traverseNodes(ctx, api, node.NodeID)
}

func traverseNodes(ctx context.Context, api dingTalkAPI, rootNodeID string) (traversalResult, error) {
	result := traversalResult{Complete: true}
	stack := []string{rootNodeID}
	visitedParents := make(map[string]struct{})
	seenNodes := make(map[string]struct{})

	for len(stack) > 0 {
		select {
		case <-ctx.Done():
			return traversalResult{}, ctx.Err()
		default:
		}
		parentID := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if _, exists := visitedParents[parentID]; exists {
			result.Complete = false
			result.Failures = append(result.Failures, branchFailure{
				ParentNodeID: parentID,
				Err:          fmt.Errorf("dingtalk node hierarchy contains a cycle"),
			})
			continue
		}
		visitedParents[parentID] = struct{}{}
		if len(visitedParents) > 100_000 {
			return traversalResult{}, fmt.Errorf("dingtalk traversal exceeded 100000 folders")
		}

		children, err := api.ListNodes(ctx, parentID)
		if err != nil {
			if isContextError(err) {
				return traversalResult{}, err
			}
			result.Complete = false
			result.Failures = append(result.Failures, branchFailure{ParentNodeID: parentID, Err: err})
			continue
		}
		for _, child := range children {
			child.ParentNodeID = parentID
			if child.NodeID == "" {
				continue
			}
			if _, exists := seenNodes[child.NodeID]; exists {
				continue
			}
			seenNodes[child.NodeID] = struct{}{}
			if len(seenNodes) > 1_000_000 {
				return traversalResult{}, fmt.Errorf("dingtalk traversal exceeded 1000000 nodes")
			}
			result.Nodes = append(result.Nodes, child)
			if isFolderNode(child) || child.HasChildren {
				stack = append(stack, child.NodeID)
			}
		}
	}
	return result, nil
}

func nodeToResource(parentRef resourceRef, parentID string, node wikiNode) (types.Resource, error) {
	ref := childResourceRef(parentRef, node.NodeID)
	externalID, err := encodeResourceRef(ref)
	if err != nil {
		return types.Resource{}, err
	}
	resourceType := "document"
	if isFolderNode(node) {
		resourceType = "folder"
	}
	return types.Resource{
		ExternalID:  externalID,
		Name:        nodeDisplayName(node),
		Type:        resourceType,
		URL:         node.URL,
		ModifiedAt:  parseDingTalkTime(node.ModifiedTime),
		ParentID:    parentID,
		HasChildren: isFolderNode(node) || node.HasChildren,
		Metadata: map[string]interface{}{
			"workspace_id":    parentRef.WorkspaceID,
			"node_id":         node.NodeID,
			"node_type":       node.NodeType,
			"category":        node.Category,
			"extension":       node.Extension,
			"permission_role": node.PermissionRole,
			"word_count":      node.StatisticalInfo.WordCount,
		},
	}, nil
}

func isFolderNode(node wikiNode) bool {
	return strings.EqualFold(node.NodeType, "FOLDER")
}

func isDocumentNode(node wikiNode) bool {
	if !strings.EqualFold(node.NodeType, "FILE") {
		return false
	}
	// ALIDOC is a broad content category that also includes spreadsheets
	// (axls) and AI tables (able). The document-block API used by this
	// connector is intended for DingTalk documents (adoc), so do not send
	// unrelated online file types to it. DOCUMENT is retained as a legacy
	// compatibility category used by older responses.
	return strings.EqualFold(node.Extension, "adoc") ||
		(strings.EqualFold(node.Category, "DOCUMENT") && strings.TrimSpace(node.Extension) == "")
}

func nodeDisplayName(node wikiNode) string {
	name := strings.TrimSpace(node.Name)
	if name == "" {
		return node.NodeID
	}
	return name
}

func sortResources(resources []types.Resource) {
	sort.SliceStable(resources, func(i, j int) bool {
		if resources[i].Type != resources[j].Type {
			if resources[i].Type == "folder" || resources[i].Type == "wiki_space" {
				return true
			}
			if resources[j].Type == "folder" || resources[j].Type == "wiki_space" {
				return false
			}
		}
		left := strings.ToLower(resources[i].Name)
		right := strings.ToLower(resources[j].Name)
		if left == right {
			return resources[i].ExternalID < resources[j].ExternalID
		}
		return left < right
	})
}

func resourceFailureItem(resourceID string, ref resourceRef, stage string, err error) types.FetchedItem {
	return types.FetchedItem{
		ExternalID:       "dingtalk-resource-" + ref.WorkspaceID,
		Title:            "DingTalk resource " + ref.WorkspaceID,
		SourceResourceID: resourceID,
		Metadata: dingtalkErrorItemMeta(err, map[string]string{
			"failure_stage": stage,
			"channel":       types.ChannelDingtalk,
			"workspace_id":  ref.WorkspaceID,
			"node_id":       ref.NodeID,
		}),
	}
}

func branchFailureItem(resourceID string, ref resourceRef, failure branchFailure) types.FetchedItem {
	return types.FetchedItem{
		ExternalID:       "dingtalk-branch-" + failure.ParentNodeID,
		Title:            "DingTalk folder " + failure.ParentNodeID,
		SourceResourceID: resourceID,
		Metadata: dingtalkErrorItemMeta(failure.Err, map[string]string{
			"failure_stage":  "list_children",
			"channel":        types.ChannelDingtalk,
			"workspace_id":   ref.WorkspaceID,
			"parent_node_id": failure.ParentNodeID,
		}),
	}
}

func documentFailureItem(resourceID string, ref resourceRef, node wikiNode, err error) types.FetchedItem {
	return types.FetchedItem{
		ExternalID:       node.NodeID,
		Title:            nodeDisplayName(node),
		SourceResourceID: resourceID,
		Metadata: dingtalkErrorItemMeta(err, map[string]string{
			"failure_stage": "fetch_content",
			"channel":       types.ChannelDingtalk,
			"workspace_id":  ref.WorkspaceID,
			"node_id":       node.NodeID,
			"category":      node.Category,
		}),
	}
}

func dingtalkFailure(err error) (code, codeValue, fallback string) {
	if err == nil {
		return "sync_failed", "", "Sync failed; will retry on the next sync"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "dingtalk_timeout", "", "DingTalk request timed out; will retry on the next sync"
	}

	var apiErr *dingTalkAPIError
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.StatusCode == http.StatusUnauthorized || apiErr.StatusCode == http.StatusForbidden:
			return "dingtalk_auth_or_permission", "", "Authentication or permission error; check credentials, app scopes, and operator access"
		case apiErr.StatusCode == http.StatusTooManyRequests:
			return "dingtalk_rate_limited", "", "DingTalk API rate limited; will retry on the next sync"
		case apiErr.StatusCode >= 500:
			return "dingtalk_server_unavailable", "", "DingTalk service temporarily unavailable; will retry on the next sync"
		default:
			return "dingtalk_api_error", apiErr.Code, "DingTalk API error; will retry on the next sync"
		}
	}
	if strings.Contains(strings.ToLower(err.Error()), "timeout") {
		return "dingtalk_timeout", "", "DingTalk request timed out; will retry on the next sync"
	}
	return "sync_failed", "", "Sync failed; will retry on the next sync"
}

func dingtalkFailureMessage(err error) string {
	_, _, fallback := dingtalkFailure(err)
	return fallback
}

func dingtalkErrorItemMeta(err error, extra map[string]string) map[string]string {
	code, codeValue, fallback := dingtalkFailure(err)
	metadata := map[string]string{
		"error":             err.Error(),
		"error_reason_code": code,
		"error_reason":      fallback,
	}
	if codeValue != "" {
		metadata["error_reason_code_value"] = codeValue
	}
	for key, value := range extra {
		metadata[key] = value
	}
	return metadata
}

func fetchedDocumentItem(
	resourceID string,
	ref resourceRef,
	node wikiNode,
	rendered markdownRenderResult,
) types.FetchedItem {
	url := strings.TrimSpace(node.URL)
	if url == "" {
		url = "https://alidocs.dingtalk.com/i/nodes/" + node.NodeID
	}
	metadata := map[string]string{
		"channel":      types.ChannelDingtalk,
		"workspace_id": ref.WorkspaceID,
		"node_id":      node.NodeID,
		"node_type":    node.NodeType,
		"category":     node.Category,
		"extension":    node.Extension,
		"word_count":   strconv.FormatInt(node.StatisticalInfo.WordCount, 10),
	}
	if len(rendered.UnknownTypes) > 0 {
		metadata["unknown_block_types"] = strings.Join(rendered.UnknownTypes, ",")
	}
	return types.FetchedItem{
		ExternalID:       node.NodeID,
		Title:            nodeDisplayName(node),
		Content:          []byte(rendered.Content),
		ContentType:      "text/markdown",
		FileName:         sanitizeFileName(nodeDisplayName(node)) + ".md",
		URL:              url,
		UpdatedAt:        parseDingTalkTime(node.ModifiedTime),
		Metadata:         metadata,
		SourceResourceID: resourceID,
	}
}

func cloneResourceState(state dingTalkResourceState) dingTalkResourceState {
	clone := dingTalkResourceState{Nodes: make(map[string]dingTalkNodeState, len(state.Nodes))}
	for nodeID, nodeState := range state.Nodes {
		clone.Nodes[nodeID] = nodeState
	}
	return clone
}

func decodeDingTalkCursor(cursor *types.SyncCursor) (*dingTalkCursor, error) {
	if cursor == nil || cursor.ConnectorCursor == nil {
		return &dingTalkCursor{Version: cursorVersion, Resources: map[string]dingTalkResourceState{}}, nil
	}
	encoded, err := json.Marshal(cursor.ConnectorCursor)
	if err != nil {
		return nil, fmt.Errorf("marshal existing dingtalk cursor: %w", err)
	}
	var decoded dingTalkCursor
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return nil, fmt.Errorf("decode existing dingtalk cursor: %w", err)
	}
	if decoded.Version == 0 {
		decoded.Version = cursorVersion
	}
	if decoded.Version != cursorVersion {
		return nil, fmt.Errorf("unsupported dingtalk cursor version %d", decoded.Version)
	}
	if decoded.Resources == nil {
		decoded.Resources = map[string]dingTalkResourceState{}
	}
	return &decoded, nil
}

func encodeDingTalkCursor(cursor *dingTalkCursor) (map[string]interface{}, error) {
	encoded, err := json.Marshal(cursor)
	if err != nil {
		return nil, fmt.Errorf("marshal dingtalk cursor: %w", err)
	}
	var out map[string]interface{}
	if err := json.Unmarshal(encoded, &out); err != nil {
		return nil, fmt.Errorf("encode dingtalk cursor map: %w", err)
	}
	return out, nil
}
