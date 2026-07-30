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

type apiFactory func(*Config) dingTalkAPI

type Connector struct {
	newAPI apiFactory
}

func NewConnector() *Connector {
	return &Connector{newAPI: func(cfg *Config) dingTalkAPI { return newHTTPClient(cfg) }}
}

func (c *Connector) api(cfg *Config) dingTalkAPI {
	if c != nil && c.newAPI != nil {
		return c.newAPI(cfg)
	}
	return newHTTPClient(cfg)
}

func (c *Connector) Type() string {
	return types.ConnectorTypeDingTalk
}

func (c *Connector) Validate(ctx context.Context, config *types.DataSourceConfig) error {
	cfg, err := parseConfig(config)
	if err != nil {
		return err
	}
	if err := c.api(cfg).validate(ctx); err != nil {
		return fmt.Errorf("validate dingtalk datasource: %w", err)
	}
	return nil
}

func (c *Connector) ListResources(
	ctx context.Context,
	config *types.DataSourceConfig,
	parentID string,
) ([]types.Resource, error) {
	cfg, err := parseConfig(config)
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
			if strings.TrimSpace(item.WorkspaceID) == "" {
				continue
			}
			ref := resourceReference{WorkspaceID: item.WorkspaceID}
			id, err := encodeResourceReference(ref)
			if err != nil {
				return nil, err
			}
			name := strings.TrimSpace(item.Name)
			if name == "" {
				name = item.WorkspaceID
			}
			resources = append(resources, types.Resource{
				ExternalID:  id,
				Name:        name,
				Type:        "wiki_space",
				Description: item.Description,
				URL:         item.URL,
				ModifiedAt:  parseTimestamp(item.ModifiedTime),
				HasChildren: item.RootNodeID != "",
				Metadata: map[string]interface{}{
					"workspace_id":   item.WorkspaceID,
					"workspace_type": item.Type,
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
		item, err := api.getWorkspace(ctx, parentRef.WorkspaceID)
		if err != nil {
			return nil, err
		}
		parentNodeID = item.RootNodeID
		if parentNodeID == "" {
			return []types.Resource{}, nil
		}
	}
	children, err := api.listChildren(ctx, parentNodeID)
	if err != nil {
		return nil, err
	}
	resources := make([]types.Resource, 0, len(children))
	for _, child := range children {
		if !child.isFolder() && !child.isOnlineDocument() {
			continue
		}
		childRef := parentRef.child(child.NodeID)
		id, err := encodeResourceReference(childRef)
		if err != nil {
			return nil, err
		}
		resourceType := "document"
		if child.isFolder() {
			resourceType = "folder"
		}
		resources = append(resources, types.Resource{
			ExternalID:  id,
			Name:        child.displayName(),
			Type:        resourceType,
			URL:         child.URL,
			ModifiedAt:  nodeModifiedAt(child),
			ParentID:    parentID,
			HasChildren: child.isFolder() || child.HasChildren,
			Metadata: map[string]interface{}{
				"workspace_id":    parentRef.WorkspaceID,
				"node_id":         child.NodeID,
				"node_type":       child.Type,
				"category":        child.Category,
				"extension":       child.Extension,
				"permission_role": child.PermissionRole,
			},
		})
	}
	sortResources(resources)
	return resources, nil
}

func (c *Connector) ResolveResourceAncestors(
	ctx context.Context,
	config *types.DataSourceConfig,
	resourceIDs []string,
) ([]string, error) {
	if _, err := parseConfig(config); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	var out []string
	for _, resourceID := range resourceIDs {
		ref, err := decodeResourceReference(resourceID)
		if err != nil {
			return nil, err
		}
		ancestors, err := resourceAncestorIDs(ref)
		if err != nil {
			return nil, err
		}
		for _, ancestor := range ancestors {
			if _, exists := seen[ancestor]; exists {
				continue
			}
			seen[ancestor] = struct{}{}
			out = append(out, ancestor)
		}
	}
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
		return nil, nil, fmt.Errorf("no dingtalk resources selected")
	}
	previous, err := decodeCursor(cursor)
	if err != nil {
		return nil, nil, err
	}
	items, next, syncErr := c.sync(ctx, config, config.ResourceIDs, previous, true)
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

type branchFailure struct {
	ParentNodeID string
	Err          error
}

type scanResult struct {
	Documents []node
	Failures  []branchFailure
	Complete  bool
}

func (c *Connector) sync(
	ctx context.Context,
	config *types.DataSourceConfig,
	resourceIDs []string,
	previous *connectorCursor,
	incremental bool,
) ([]types.FetchedItem, *connectorCursor, error) {
	cfg, err := parseConfig(config)
	if err != nil {
		return nil, nil, err
	}
	if len(resourceIDs) == 0 {
		return nil, nil, fmt.Errorf("no dingtalk resources selected")
	}
	api := c.api(cfg)
	next := &connectorCursor{
		Version:   cursorVersion,
		SyncedAt:  time.Now().UTC(),
		Resources: make(map[string]resourceCursor),
	}
	var items []types.FetchedItem
	var warnings []string
	seenResources := make(map[string]struct{})

	for _, resourceID := range resourceIDs {
		resourceID = strings.TrimSpace(resourceID)
		if _, exists := seenResources[resourceID]; exists {
			continue
		}
		seenResources[resourceID] = struct{}{}
		ref, err := decodeResourceReference(resourceID)
		if err != nil {
			return nil, nil, err
		}
		oldState := resourceCursor{Documents: map[string]string{}}
		if previous != nil {
			if stored, exists := previous.Resources[resourceID]; exists {
				oldState = cloneResourceCursor(stored)
			}
		}

		scan, err := resolveSelection(ctx, api, ref)
		if err != nil {
			if isContextError(err) {
				return nil, nil, err
			}
			items = append(items, resourceErrorItem(resourceID, ref, err))
			warnings = append(warnings, failureMessage(err))
			next.Resources[resourceID] = oldState
			continue
		}

		state := resourceCursor{Documents: make(map[string]string)}
		if !scan.Complete {
			state = cloneResourceCursor(oldState)
		}
		for _, failure := range scan.Failures {
			items = append(items, branchErrorItem(resourceID, ref, failure))
			warnings = append(warnings, failureMessage(failure.Err))
		}

		current := make(map[string]struct{})
		seenDocuments := make(map[string]struct{})
		for _, document := range scan.Documents {
			if document.NodeID == "" {
				continue
			}
			if _, exists := seenDocuments[document.NodeID]; exists {
				continue
			}
			seenDocuments[document.NodeID] = struct{}{}
			current[document.NodeID] = struct{}{}
			revision := nodeRevision(document)
			oldRevision, existed := oldState.Documents[document.NodeID]
			if incremental && existed && revision != "" && revision == oldRevision {
				state.Documents[document.NodeID] = revision
				continue
			}

			blocks, err := api.listDocumentBlocks(ctx, document.NodeID)
			if err != nil {
				if isContextError(err) {
					return nil, nil, err
				}
				items = append(items, documentErrorItem(resourceID, ref, document, err))
				warnings = append(warnings, failureMessage(err))
				if existed {
					state.Documents[document.NodeID] = oldRevision
				}
				continue
			}
			rendered := renderDocument(document.displayName(), blocks)
			items = append(items, fetchedDocument(resourceID, ref, document, rendered))
			state.Documents[document.NodeID] = revision
		}

		if incremental && scan.Complete {
			for documentID := range oldState.Documents {
				if _, exists := current[documentID]; exists {
					continue
				}
				items = append(items, types.FetchedItem{
					ExternalID:       documentID,
					IsDeleted:        true,
					SourceResourceID: resourceID,
				})
			}
		}
		next.Resources[resourceID] = state
		logger.Infof(ctx,
			"[DingTalk datasource] resource=%s documents=%d complete=%t failures=%d",
			redactIdentifier(resourceID), len(current), scan.Complete, len(scan.Failures))
	}

	if len(warnings) > 0 {
		if !incremental {
			return items, nil, &datasource.PartialFetchError{Details: warnings}
		}
		return items, next, &datasource.PartialFetchError{Details: warnings}
	}
	if !incremental {
		return items, nil, nil
	}
	return items, next, nil
}

func resolveSelection(
	ctx context.Context,
	api dingTalkAPI,
	ref resourceReference,
) (scanResult, error) {
	if ref.NodeID == "" {
		item, err := api.getWorkspace(ctx, ref.WorkspaceID)
		if err != nil {
			return scanResult{}, err
		}
		if item.RootNodeID == "" {
			return scanResult{Complete: true}, nil
		}
		return scanTree(ctx, api, item.RootNodeID)
	}

	selected, err := api.getNode(ctx, ref.NodeID)
	if err != nil {
		return scanResult{}, err
	}
	if selected.WorkspaceID != "" && selected.WorkspaceID != ref.WorkspaceID {
		return scanResult{}, fmt.Errorf(
			"dingtalk node belongs to workspace %s, expected %s",
			selected.WorkspaceID, ref.WorkspaceID,
		)
	}
	if selected.isOnlineDocument() {
		return scanResult{Documents: []node{selected}, Complete: true}, nil
	}
	if selected.isFolder() {
		return scanTree(ctx, api, selected.NodeID)
	}
	return scanResult{}, fmt.Errorf("selected dingtalk node is not a folder or online document")
}

func scanTree(ctx context.Context, api dingTalkAPI, rootNodeID string) (scanResult, error) {
	result := scanResult{Complete: true}
	queue := []string{rootNodeID}
	visitedParents := make(map[string]struct{})
	seenNodes := make(map[string]struct{})

	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			return scanResult{}, err
		}
		parent := queue[0]
		queue = queue[1:]
		if _, exists := visitedParents[parent]; exists {
			result.Complete = false
			result.Failures = append(result.Failures, branchFailure{
				ParentNodeID: parent,
				Err:          fmt.Errorf("dingtalk node hierarchy contains a cycle"),
			})
			continue
		}
		visitedParents[parent] = struct{}{}
		if len(visitedParents) > 100_000 {
			return scanResult{}, fmt.Errorf("dingtalk traversal exceeded 100000 parent nodes")
		}

		children, err := api.listChildren(ctx, parent)
		if err != nil {
			if isContextError(err) {
				return scanResult{}, err
			}
			result.Complete = false
			result.Failures = append(result.Failures, branchFailure{ParentNodeID: parent, Err: err})
			continue
		}
		for _, child := range children {
			if child.NodeID == "" {
				continue
			}
			if _, exists := seenNodes[child.NodeID]; exists {
				continue
			}
			seenNodes[child.NodeID] = struct{}{}
			if len(seenNodes) > 1_000_000 {
				return scanResult{}, fmt.Errorf("dingtalk traversal exceeded 1000000 nodes")
			}
			if child.isOnlineDocument() {
				result.Documents = append(result.Documents, child)
			}
			if child.isFolder() || child.HasChildren {
				queue = append(queue, child.NodeID)
			}
		}
	}
	return result, nil
}

func sortResources(resources []types.Resource) {
	sort.SliceStable(resources, func(i, j int) bool {
		left := strings.ToLower(resources[i].Name)
		right := strings.ToLower(resources[j].Name)
		if left == right {
			return resources[i].ExternalID < resources[j].ExternalID
		}
		return left < right
	})
}

func fetchedDocument(
	resourceID string,
	ref resourceReference,
	document node,
	rendered renderResult,
) types.FetchedItem {
	metadata := map[string]string{
		"channel":      types.ChannelDingtalk,
		"workspace_id": ref.WorkspaceID,
		"node_id":      document.NodeID,
		"category":     document.Category,
		"extension":    document.Extension,
		"creator_id":   document.CreatorID,
		"modifier_id":  document.ModifierID,
	}
	if len(rendered.UnknownTypes) > 0 {
		metadata["unknown_block_types"] = strings.Join(rendered.UnknownTypes, ",")
	}
	documentURL := strings.TrimSpace(document.URL)
	if documentURL == "" {
		documentURL = "https://alidocs.dingtalk.com/i/nodes/" + document.NodeID
	}
	return types.FetchedItem{
		ExternalID:       document.NodeID,
		Title:            document.displayName(),
		Content:          []byte(rendered.Markdown),
		ContentType:      "text/markdown",
		FileName:         sanitizeFilename(document.displayName()) + ".md",
		URL:              documentURL,
		UpdatedAt:        nodeModifiedAt(document),
		Metadata:         metadata,
		SourceResourceID: resourceID,
	}
}

func resourceErrorItem(
	resourceID string,
	ref resourceReference,
	err error,
) types.FetchedItem {
	return types.FetchedItem{
		ExternalID:       "dingtalk-resource-" + ref.WorkspaceID,
		Title:            "DingTalk resource",
		SourceResourceID: resourceID,
		Metadata: failureMetadata(err, map[string]string{
			"failure_stage": "resolve_resource",
			"channel":       types.ChannelDingtalk,
			"workspace_id":  ref.WorkspaceID,
			"node_id":       ref.NodeID,
		}),
	}
}

func branchErrorItem(
	resourceID string,
	ref resourceReference,
	failure branchFailure,
) types.FetchedItem {
	return types.FetchedItem{
		ExternalID:       "dingtalk-branch-" + failure.ParentNodeID,
		Title:            "DingTalk folder",
		SourceResourceID: resourceID,
		Metadata: failureMetadata(failure.Err, map[string]string{
			"failure_stage":  "list_children",
			"channel":        types.ChannelDingtalk,
			"workspace_id":   ref.WorkspaceID,
			"parent_node_id": failure.ParentNodeID,
		}),
	}
}

func documentErrorItem(
	resourceID string,
	ref resourceReference,
	document node,
	err error,
) types.FetchedItem {
	return types.FetchedItem{
		ExternalID:       document.NodeID,
		Title:            document.displayName(),
		SourceResourceID: resourceID,
		Metadata: failureMetadata(err, map[string]string{
			"failure_stage": "fetch_content",
			"channel":       types.ChannelDingtalk,
			"workspace_id":  ref.WorkspaceID,
			"node_id":       document.NodeID,
		}),
	}
}

func failureMetadata(err error, extra map[string]string) map[string]string {
	code, codeValue, message := classifyFailure(err)
	metadata := map[string]string{
		"error":             err.Error(),
		"error_reason_code": code,
		"error_reason":      message,
	}
	if codeValue != "" {
		metadata["error_reason_code_value"] = codeValue
	}
	for key, value := range extra {
		metadata[key] = value
	}
	return metadata
}

func classifyFailure(err error) (code, codeValue, message string) {
	if err == nil {
		return "sync_failed", "", "Sync failed; retry on the next sync"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "dingtalk_timeout", "", "DingTalk request timed out; retry on the next sync"
	}
	var remote *apiError
	if errors.As(err, &remote) {
		switch {
		case remote.StatusCode == http.StatusUnauthorized || remote.StatusCode == http.StatusForbidden:
			return "dingtalk_auth_or_permission", "",
				"Check DingTalk credentials, application scopes, and operator access"
		case remote.StatusCode == http.StatusTooManyRequests:
			return "dingtalk_rate_limited", "",
				"DingTalk rate limited the request; retry on the next sync"
		case remote.StatusCode >= 500:
			return "dingtalk_unavailable", "",
				"DingTalk is temporarily unavailable; retry on the next sync"
		case remote.Code != "":
			return "dingtalk_api_error", remote.Code,
				"DingTalk API rejected the request; retry on the next sync"
		default:
			return "dingtalk_api_error_generic", "",
				"DingTalk API rejected the request; retry on the next sync"
		}
	}
	if strings.Contains(strings.ToLower(err.Error()), "timeout") {
		return "dingtalk_timeout", "", "DingTalk request timed out; retry on the next sync"
	}
	return "sync_failed", "", "Sync failed; retry on the next sync"
}

func failureMessage(err error) string {
	_, _, message := classifyFailure(err)
	return message
}

func nodeRevision(document node) string {
	if value := strings.TrimSpace(document.ModifiedTime); value != "" {
		return value
	}
	if document.ModifiedUnixMS > 0 {
		return strconv.FormatInt(document.ModifiedUnixMS, 10)
	}
	return ""
}

func nodeModifiedAt(document node) time.Time {
	if parsed := parseTimestamp(document.ModifiedTime); !parsed.IsZero() {
		return parsed
	}
	if document.ModifiedUnixMS > 0 {
		return time.UnixMilli(document.ModifiedUnixMS)
	}
	return time.Time{}
}

func cloneResourceCursor(input resourceCursor) resourceCursor {
	out := resourceCursor{Documents: make(map[string]string, len(input.Documents))}
	for documentID, revision := range input.Documents {
		out.Documents[documentID] = revision
	}
	return out
}

func decodeCursor(cursor *types.SyncCursor) (*connectorCursor, error) {
	if cursor == nil || cursor.ConnectorCursor == nil {
		return &connectorCursor{
			Version:   cursorVersion,
			Resources: make(map[string]resourceCursor),
		}, nil
	}
	data, err := json.Marshal(cursor.ConnectorCursor)
	if err != nil {
		return nil, fmt.Errorf("marshal dingtalk cursor: %w", err)
	}
	var decoded connectorCursor
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil, fmt.Errorf("decode dingtalk cursor: %w", err)
	}
	if decoded.Version == 0 {
		decoded.Version = cursorVersion
	}
	if decoded.Version != cursorVersion {
		return nil, fmt.Errorf("unsupported dingtalk cursor version %d", decoded.Version)
	}
	if decoded.Resources == nil {
		decoded.Resources = make(map[string]resourceCursor)
	}
	for resourceID, state := range decoded.Resources {
		if state.Documents == nil {
			state.Documents = make(map[string]string)
			decoded.Resources[resourceID] = state
		}
	}
	return &decoded, nil
}

func encodeCursor(cursor *connectorCursor) (map[string]interface{}, error) {
	data, err := json.Marshal(cursor)
	if err != nil {
		return nil, fmt.Errorf("marshal dingtalk cursor: %w", err)
	}
	var encoded map[string]interface{}
	if err := json.Unmarshal(data, &encoded); err != nil {
		return nil, fmt.Errorf("encode dingtalk cursor: %w", err)
	}
	return encoded, nil
}
