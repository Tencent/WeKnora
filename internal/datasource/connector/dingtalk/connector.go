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

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// Compile-time proof that *Connector satisfies both the datasource.Connector
// and the datasource.StreamingConnector interfaces. The service layer prefers
// FetchStream over FetchAll/FetchIncremental when a connector implements
// StreamingConnector, so a large DingTalk knowledge base sync is memory-bounded
// and resumable after a timeout instead of restarting from scratch.
var (
	_ datasource.Connector          = (*Connector)(nil)
	_ datasource.StreamingConnector = (*Connector)(nil)
)

// Connector implements datasource.Connector for DingTalk.
type Connector struct{}

// NewConnector creates a new DingTalk connector.
func NewConnector() *Connector { return &Connector{} }

// Type returns the connector type identifier.
func (c *Connector) Type() string { return types.ConnectorTypeDingTalk }

const dingtalkNodeResourceSeparator = ":"

// dingtalkStreamCheckpointInterval is how many processed nodes pass between
// cursor checkpoints during a streaming fetch. Small enough that a timed-out
// sync loses little work on resume, large enough that checkpoint persistence
// (a DB write) does not dominate. Overridable in tests. See FetchStream.
var dingtalkStreamCheckpointInterval = 50

// dingtalkStreamCheckpointMaxInterval bounds checkpointing by wall-clock time
// as well as node count. Without it, a sync of fewer than the node interval
// very slow (rate-limited) requests could reach the 2h task timeout having
// never checkpointed, and resume from scratch every retry.
var dingtalkStreamCheckpointMaxInterval = 30 * time.Second

// Validate verifies the given credentials by attempting to get an access token.
func (c *Connector) Validate(ctx context.Context, config *types.DataSourceConfig) error {
	cfg, err := parseDingTalkConfig(config)
	if err != nil {
		return err
	}
	cli := NewClient(cfg)
	if err := cli.Ping(ctx); err != nil {
		return fmt.Errorf("dingtalk connection failed: %w", err)
	}
	return nil
}

// ListResources lists DingTalk knowledge base resources for selection, loading
// the tree lazily one level at a time.
//
//   - parentID == ""                        → list all accessible knowledge base spaces.
//   - parentID == "spaceID"                 → list top-level nodes of that space.
//   - parentID == "spaceID:nodeID"          → list direct children of that node.
func (c *Connector) ListResources(
	ctx context.Context, config *types.DataSourceConfig, parentID string,
) ([]types.Resource, error) {
	cfg, err := parseDingTalkConfig(config)
	if err != nil {
		return nil, err
	}

	cli := NewClient(cfg)

	if parentID == "" {
		spaces, err := cli.ListSpaces(ctx)
		if err != nil {
			return nil, fmt.Errorf("list dingtalk spaces: %w", err)
		}

		resources := make([]types.Resource, 0, len(spaces))
		for _, space := range spaces {
			resources = append(resources, types.Resource{
				ExternalID:  space.SpaceID,
				Name:        space.Name,
				Type:        "kb_space",
				Description: space.Desc,
				URL:         WebBaseURL + "/dingdoc/space_" + space.SpaceID,
				HasChildren: true,
				Metadata: map[string]interface{}{
					"space_id": space.SpaceID,
				},
			})
		}
		return resources, nil
	}

	// Lazy load: list only the direct children of the given space / node.
	spaceID, nodeID := parseDingTalkResourceID(parentID)
	nodes, err := cli.ListSpaceNodes(ctx, spaceID, nodeID)
	if err != nil {
		return nil, fmt.Errorf("list dingtalk nodes under %s: %w", parentID, err)
	}

	resources := make([]types.Resource, 0, len(nodes))
	for _, node := range nodes {
		resources = append(resources, c.nodeToResource(spaceID, node))
	}
	return resources, nil
}

// ResolveResourceAncestors returns the resource IDs of every parent that has to
// be expanded so the lazily-loaded picker can reveal each given selection.
// For DingTalk, the tree is walked from the selected node up to the space root
// using the parentId field from ListSpaceNodes.
//
// DingTalk does not have a GetNode (single) API that returns the parent, so we
// list the parent's siblings to find the node's parent chain. In practice the
// resource picker only needs the space and any intermediate folder IDs.
func (c *Connector) ResolveResourceAncestors(
	ctx context.Context, config *types.DataSourceConfig, resourceIDs []string,
) ([]string, error) {
	cfg, err := parseDingTalkConfig(config)
	if err != nil {
		return nil, err
	}
	cli := NewClient(cfg)

	seen := make(map[string]bool)
	ancestors := make([]string, 0)
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			ancestors = append(ancestors, id)
		}
	}

	for _, rid := range resourceIDs {
		spaceID, nodeID := parseDingTalkResourceID(rid)
		if spaceID == "" || nodeID == "" {
			// A space-level selection is already a top-level node in the picker.
			continue
		}
		// The space's direct children must be loaded to reveal the top-level node.
		add(spaceID)

		// Walk up from the selection to the top by listing siblings to find
		// the parent of each node. DingTalk's node listing does not return a
		// parent_node_id for top-level nodes, so we best-effort traverse.
		// For now, we rely on the node's parentId field from ListSpaceNodes.
		// If parentId is empty, it's a top-level node and the space is enough.
		// This is a simplified version — if DingTalk adds a GetNode API that
		// returns parentId, we can walk the full chain.
		current := nodeID
		for current != "" {
			// Try to find this node's parent by listing siblings.
			// We need to know the parent to list siblings, which is circular.
			// Instead, we list all top-level nodes and search for current,
			// then check if current has a parentId.
			topNodes, err := cli.ListSpaceNodes(ctx, spaceID, "")
			if err != nil {
				logger.Warnf(ctx, "[DingTalk] resolve ancestors: list top nodes for %s: %v", spaceID, err)
				break
			}
			found := false
			for _, n := range topNodes {
				if n.NodeID == current {
					found = true
					if n.ParentID != "" {
						add(makeNodeResourceID(spaceID, n.ParentID))
						current = n.ParentID
					} else {
						current = ""
					}
					break
				}
			}
			if !found {
				// Node is not top-level; we can't walk up without a GetNode API.
				// Best-effort: stop here.
				break
			}
		}
	}

	return ancestors, nil
}

// FetchAll performs a full sync of all documents from the specified spaces.
func (c *Connector) FetchAll(ctx context.Context, config *types.DataSourceConfig, resourceIDs []string) ([]types.FetchedItem, error) {
	cfg, err := parseDingTalkConfig(config)
	if err != nil {
		return nil, err
	}

	cli := NewClient(cfg)

	var allItems []types.FetchedItem

	for _, resourceID := range resourceIDs {
		spaceID, _ := parseDingTalkResourceID(resourceID)
		nodes, err := cli.ListAllNodesRecursive(ctx, spaceID)
		if err != nil {
			var partialErr *partialNodeListError
			if !errors.As(err, &partialErr) {
				return nil, fmt.Errorf("list nodes for resource %s: %w", resourceID, err)
			}
			allItems = appendNodeListFailureItems(allItems, resourceID, partialErr.Failures)
		}

		tally := newFetchTally(len(nodes))
		for i, node := range nodes {
			item, err := c.fetchNodeContent(ctx, cli, node, resourceID)
			if err != nil {
				tally.fail()
				allItems = append(allItems, types.FetchedItem{
					ExternalID:       node.NodeID,
					Title:            node.Name,
					SourceResourceID: resourceID,
					Metadata:         dingtalkErrorItemMeta(err, nil),
				})
				continue
			}
			if item != nil {
				tally.fetch()
				allItems = append(allItems, *item)
			} else {
				tally.skip(node.Type)
			}
			if n := i + 1; n%100 == 0 {
				logger.Infof(ctx, "[DingTalk] sync progress resource=%s %d/%d (%s)",
					resourceID, n, len(nodes), tally.summary())
			}
		}
		logger.Infof(ctx, "[DingTalk] sync summary resource=%s %s", resourceID, tally.summary())
	}

	return allItems, nil
}

// FetchIncremental performs an incremental sync by comparing node edit times
// against the previously recorded state.
func (c *Connector) FetchIncremental(ctx context.Context, config *types.DataSourceConfig, cursor *types.SyncCursor) ([]types.FetchedItem, *types.SyncCursor, error) {
	cfg, err := parseDingTalkConfig(config)
	if err != nil {
		return nil, nil, err
	}

	cli := NewClient(cfg)

	// Parse the previous cursor state
	var prevCursor dingtalkCursor
	if cursor != nil && cursor.ConnectorCursor != nil {
		cursorBytes, _ := json.Marshal(cursor.ConnectorCursor)
		_ = json.Unmarshal(cursorBytes, &prevCursor)
	}

	// Build new cursor to track current state
	newCursor := dingtalkCursor{
		LastSyncTime:   time.Now(),
		SpaceNodeTimes: make(map[string]map[string]string),
	}

	var changedItems []types.FetchedItem

	resourceIDs := config.ResourceIDs
	if len(resourceIDs) == 0 {
		return nil, nil, fmt.Errorf("no resource IDs (space IDs) configured")
	}

	for _, resourceID := range resourceIDs {
		spaceID, _ := parseDingTalkResourceID(resourceID)
		nodes, err := cli.ListAllNodesRecursive(ctx, spaceID)
		var partialErr *partialNodeListError
		if err != nil {
			if !errors.As(err, &partialErr) {
				return nil, nil, fmt.Errorf("list nodes for resource %s: %w", resourceID, err)
			}
			changedItems = appendNodeListFailureItems(changedItems, resourceID, partialErr.Failures)
		}

		newCursor.SpaceNodeTimes[resourceID] = make(map[string]string)
		// On a partial listing, carry prior edit times forward so a later full
		// listing can still detect changes and deletions.
		if partialErr != nil && prevCursor.SpaceNodeTimes != nil {
			if prevTimes, ok := prevCursor.SpaceNodeTimes[resourceID]; ok {
				for nodeID, et := range prevTimes {
					newCursor.SpaceNodeTimes[resourceID][nodeID] = et
				}
			}
		}

		// Build a set of current node IDs for deletion detection
		currentNodes := make(map[string]bool)

		for _, node := range nodes {
			currentNodes[node.NodeID] = true
			editTimeStr := strconv.FormatInt(node.EditTime, 10)

			// Look up prior edit time for this node (if any).
			var prevEditTime string
			var hadPrev bool
			if prevCursor.SpaceNodeTimes != nil {
				if prevTimes, ok := prevCursor.SpaceNodeTimes[resourceID]; ok {
					prevEditTime, hadPrev = prevTimes[node.NodeID]
				}
			}

			// Unchanged fast-path: record current edit time and skip fetching.
			if hadPrev && prevEditTime == editTimeStr {
				newCursor.SpaceNodeTimes[resourceID][node.NodeID] = editTimeStr
				continue
			}

			// Node is new or changed — fetch its content.
			item, err := c.fetchNodeContent(ctx, cli, node, resourceID)
			if err != nil {
				// Do NOT record the current edit time: the content was never
				// fetched. Retain the prior edit time (if any) so the node is
				// retried on the next sync instead of being permanently skipped.
				if hadPrev {
					newCursor.SpaceNodeTimes[resourceID][node.NodeID] = prevEditTime
				}
				changedItems = append(changedItems, types.FetchedItem{
					ExternalID:       node.NodeID,
					Title:            node.Name,
					SourceResourceID: resourceID,
					Metadata:         dingtalkErrorItemMeta(err, nil),
				})
				continue
			}
			// Fetch succeeded (or unsupported type — nothing to fetch):
			// record the current edit time so the node is not re-processed.
			newCursor.SpaceNodeTimes[resourceID][node.NodeID] = editTimeStr
			if item != nil {
				changedItems = append(changedItems, *item)
			}
		}

		// Detect deleted nodes (only when the full tree was listed successfully).
		if partialErr == nil && prevCursor.SpaceNodeTimes != nil {
			if prevTimes, ok := prevCursor.SpaceNodeTimes[resourceID]; ok {
				for nodeID := range prevTimes {
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
	}

	// Build next sync cursor
	nextCursorMap := make(map[string]interface{})
	cursorBytes, _ := json.Marshal(newCursor)
	_ = json.Unmarshal(cursorBytes, &nextCursorMap)

	nextSyncCursor := &types.SyncCursor{
		LastSyncTime:    time.Now(),
		ConnectorCursor: nextCursorMap,
	}

	return changedItems, nextSyncCursor, nil
}

// FetchStream performs a resumable, memory-bounded sync. It unifies the full
// and incremental paths: with cursor == nil it fetches everything, and with a
// cursor it skips nodes whose recorded edit time is unchanged — the same
// mechanism that lets a sync which timed out mid-traversal resume from the last
// checkpoint instead of restarting.
//
// Instead of accumulating every item in memory (FetchAll), it emits each item
// as it is fetched and checkpoints the cursor every dingtalkStreamCheckpointInterval
// processed nodes, so progress is durable across the Asynq task's 2h timeout.
func (c *Connector) FetchStream(
	ctx context.Context, config *types.DataSourceConfig,
	cursor *types.SyncCursor, h datasource.StreamHandler,
) (*types.SyncCursor, error) {
	cfg, err := parseDingTalkConfig(config)
	if err != nil {
		return nil, err
	}
	cli := NewClient(cfg)

	var prevCursor dingtalkCursor
	if cursor != nil && cursor.ConnectorCursor != nil {
		cursorBytes, _ := json.Marshal(cursor.ConnectorCursor)
		_ = json.Unmarshal(cursorBytes, &prevCursor)
	}

	newCursor := dingtalkCursor{
		LastSyncTime:   time.Now(),
		SpaceNodeTimes: make(map[string]map[string]string),
	}

	resourceIDs := config.ResourceIDs
	if len(resourceIDs) == 0 {
		return nil, fmt.Errorf("no resource IDs (space IDs) configured")
	}

	processed := 0
	lastCheckpoint := time.Now()
	for _, resourceID := range resourceIDs {
		spaceID, _ := parseDingTalkResourceID(resourceID)
		nodes, err := cli.ListAllNodesRecursive(ctx, spaceID)
		var partialErr *partialNodeListError
		if err != nil {
			if !errors.As(err, &partialErr) {
				return nil, fmt.Errorf("list nodes for resource %s: %w", resourceID, err)
			}
			for _, item := range appendNodeListFailureItems(nil, resourceID, partialErr.Failures) {
				if eerr := h.Emit(ctx, item); eerr != nil {
					return nil, eerr
				}
			}
		}

		newCursor.SpaceNodeTimes[resourceID] = make(map[string]string)
		if partialErr != nil && prevCursor.SpaceNodeTimes != nil {
			if prevTimes, ok := prevCursor.SpaceNodeTimes[resourceID]; ok {
				for nodeID, et := range prevTimes {
					newCursor.SpaceNodeTimes[resourceID][nodeID] = et
				}
			}
		}

		currentNodes := make(map[string]bool)
		tally := newFetchTally(len(nodes))
		for i, node := range nodes {
			currentNodes[node.NodeID] = true
			editTimeStr := strconv.FormatInt(node.EditTime, 10)

			// Prior recorded edit time for this node, if any.
			var prevEdit string
			var hadPrev bool
			if prevCursor.SpaceNodeTimes != nil {
				if prevTimes, ok := prevCursor.SpaceNodeTimes[resourceID]; ok {
					prevEdit, hadPrev = prevTimes[node.NodeID]
				}
			}

			// Resume/incremental fast-path: a node recorded at its current edit
			// time is unchanged (or already synced this run) — keep the record
			// and skip re-fetching.
			if hadPrev && prevEdit == editTimeStr {
				newCursor.SpaceNodeTimes[resourceID][node.NodeID] = editTimeStr
				continue
			}

			item, ferr := c.fetchNodeContent(ctx, cli, node, resourceID)
			if ferr != nil {
				tally.fail()
				// Do NOT advance the cursor: the content was never fetched.
				// Retain the prior edit time (if any) so prev != current next
				// run and the node is retried, instead of being permanently
				// skipped on a transient failure.
				if hadPrev {
					newCursor.SpaceNodeTimes[resourceID][node.NodeID] = prevEdit
				}
				if eerr := h.Emit(ctx, types.FetchedItem{
					ExternalID:       node.NodeID,
					Title:            node.Name,
					SourceResourceID: resourceID,
					Metadata:         dingtalkErrorItemMeta(ferr, nil),
				}); eerr != nil {
					return nil, eerr
				}
			} else {
				// Fetched, or an unsupported type (nothing to fetch): record
				// the current edit time so the node is not re-processed next run.
				newCursor.SpaceNodeTimes[resourceID][node.NodeID] = editTimeStr
				if item != nil {
					tally.fetch()
					if eerr := h.Emit(ctx, *item); eerr != nil {
						return nil, eerr
					}
				} else {
					tally.skip(node.Type)
				}
			}

			processed++
			if processed%dingtalkStreamCheckpointInterval == 0 || time.Since(lastCheckpoint) >= dingtalkStreamCheckpointMaxInterval {
				if cerr := h.Checkpoint(ctx, newCursor.toSyncCursor()); cerr != nil {
					logger.Warnf(ctx, "[DingTalk] stream checkpoint failed: %v", cerr)
				}
				lastCheckpoint = time.Now()
			}
			if n := i + 1; n%100 == 0 {
				logger.Infof(ctx, "[DingTalk] stream progress resource=%s %d/%d (%s)",
					resourceID, n, len(nodes), tally.summary())
			}
		}

		// Detect deleted nodes (only when the full tree was listed successfully).
		if partialErr == nil && prevCursor.SpaceNodeTimes != nil {
			if prevTimes, ok := prevCursor.SpaceNodeTimes[resourceID]; ok {
				for nodeID := range prevTimes {
					if !currentNodes[nodeID] {
						if eerr := h.Emit(ctx, types.FetchedItem{
							ExternalID:       nodeID,
							IsDeleted:        true,
							SourceResourceID: resourceID,
						}); eerr != nil {
							return nil, eerr
						}
					}
				}
			}
		}
		logger.Infof(ctx, "[DingTalk] stream summary resource=%s %s", resourceID, tally.summary())
	}

	return newCursor.toSyncCursor(), nil
}

// toSyncCursor converts the connector-specific dingtalkCursor into the generic
// SyncCursor persisted by the service.
func (dc dingtalkCursor) toSyncCursor() *types.SyncCursor {
	m := make(map[string]interface{})
	cursorBytes, _ := json.Marshal(dc)
	_ = json.Unmarshal(cursorBytes, &m)
	return &types.SyncCursor{
		LastSyncTime:    dc.LastSyncTime,
		ConnectorCursor: m,
	}
}

// appendNodeListFailureItems converts partial listing failures into FetchedItem
// error items and appends them to the given slice.
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
			Metadata: dingtalkErrorItemMeta(failure.Err, map[string]string{
				"channel":       types.ChannelDingtalk,
				"node_id":       node.NodeID,
				"space_id":      node.SpaceID,
				"failure_stage": "list_children",
			}),
		})
	}
	return items
}

// fetchNodeContent fetches the content of a single node and converts it to FetchedItem.
// Dispatches based on node type:
//   - doc    → get node detail → Markdown content
//   - sheet  → skip (no content read API in v1)
//   - mindmap → skip (no content read API)
//   - folder → skip (container node, no content)
//   - file   → skip (no download API in v1)
func (c *Connector) fetchNodeContent(ctx context.Context, cli *Client, node docNode, resourceID string) (*types.FetchedItem, error) {
	if !isSupportedDocType(node.Type) {
		return nil, nil
	}

	detail, err := cli.GetNodeDetail(ctx, node.NodeID)
	if err != nil {
		return nil, fmt.Errorf("get node detail %s (%s): %w", node.Name, node.Type, err)
	}

	editTime := parseEditTime(node.EditTime)
	baseMeta := map[string]string{
		"node_id":   node.NodeID,
		"space_id":  node.SpaceID,
		"node_type": node.Type,
		"creator":   node.Creator,
		"channel":   types.ChannelDingtalk,
	}

	// DingTalk doc content is returned as Markdown (or HTML, depending on the API version).
	// We use "text/markdown" as the content type; the ingestion pipeline will handle parsing.
	contentType := "text/markdown"
	content := detail.Content

	// If content is empty, create a minimal item with just the title so the
	// document is still tracked in the knowledge base.
	if strings.TrimSpace(content) == "" {
		content = "# " + node.Name
	}

	return &types.FetchedItem{
		ExternalID:       node.NodeID,
		Title:            node.Name,
		Content:          []byte(content),
		ContentType:      contentType,
		FileName:         sanitizeFileName(node.Name) + ".md",
		URL:              WebBaseURL + "/dingdoc/space_" + node.SpaceID + "/node_" + node.NodeID,
		UpdatedAt:        editTime,
		SourceResourceID: resourceID,
		Metadata:         baseMeta,
	}, nil
}

// --- fetchTally (mirrors Feishu's tally pattern) ---

type fetchTally struct {
	discovered    int
	fetched       int
	failed        int
	skippedByType map[string]int
}

func newFetchTally(discovered int) *fetchTally {
	return &fetchTally{discovered: discovered, skippedByType: map[string]int{}}
}

func (t *fetchTally) fetch()              { t.fetched++ }
func (t *fetchTally) fail()               { t.failed++ }
func (t *fetchTally) skip(objType string) { t.skippedByType[objType]++ }

func (t *fetchTally) skipped() int {
	n := 0
	for _, c := range t.skippedByType {
		n += c
	}
	return n
}

func (t *fetchTally) summary() string {
	return fmt.Sprintf("discovered=%d fetched=%d failed=%d skipped_unsupported=%d by_type=%v",
		t.discovered, t.fetched, t.failed, t.skipped(), t.skippedByType)
}

// --- Error classification (mirrors Feishu's pattern) ---

// dingtalkFailure classifies a raw connector/API error into a stable i18n code.
func dingtalkFailure(err error) (code, codeValue, fallback string) {
	if err == nil {
		return "sync_failed", "", "Sync failed; will retry on the next sync"
	}
	s := strings.ToLower(err.Error())

	switch {
	case strings.Contains(s, "invalid credentials"),
		strings.Contains(s, "status=401"),
		strings.Contains(s, "status=403"),
		strings.Contains(s, "permission"):
		return "dingtalk_auth_or_permission", "", "Authentication or permission error; check credentials and app scopes"
	case strings.Contains(s, "rate limited"), strings.Contains(s, "status=429"):
		return "dingtalk_rate_limited", "", "DingTalk API rate limited; will retry on the next sync"
	case strings.Contains(s, "timed out"),
		strings.Contains(s, "timeout"),
		strings.Contains(s, "deadline exceeded"):
		return "dingtalk_timeout", "", "Request timed out; will retry on the next sync"
	case strings.Contains(s, "server error"):
		return "dingtalk_server_unavailable", "", "DingTalk service temporarily unavailable; will retry on the next sync"
	case strings.Contains(s, "api error"):
		return "dingtalk_api_error", "", "DingTalk API error; will retry on the next sync"
	default:
		return "sync_failed", "", "Sync failed; will retry on the next sync"
	}
}

// dingtalkErrorItemMeta builds the metadata for a failed item.
func dingtalkErrorItemMeta(err error, extra map[string]string) map[string]string {
	code, _, fallback := dingtalkFailure(err)
	m := map[string]string{
		"error":             err.Error(),
		"error_reason_code": code,
		"error_reason":      fallback,
	}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

// --- Helper functions ---

func makeNodeResourceID(spaceID, nodeID string) string {
	return spaceID + dingtalkNodeResourceSeparator + nodeID
}

func parseDingTalkResourceID(resourceID string) (spaceID string, nodeID string) {
	spaceID, nodeID, _ = strings.Cut(resourceID, dingtalkNodeResourceSeparator)
	return spaceID, nodeID
}

func (c *Connector) nodeToResource(spaceID string, node docNode) types.Resource {
	parentID := spaceID
	if node.ParentID != "" {
		parentID = makeNodeResourceID(spaceID, node.ParentID)
	}

	name := node.Name
	if name == "" {
		name = node.NodeID
	}

	return types.Resource{
		ExternalID:  makeNodeResourceID(spaceID, node.NodeID),
		Name:        name,
		Type:        "kb_node",
		URL:         WebBaseURL + "/dingdoc/space_" + spaceID + "/node_" + node.NodeID,
		ParentID:    parentID,
		HasChildren: node.Type == "folder",
		ModifiedAt:  parseEditTime(node.EditTime),
		Metadata: map[string]interface{}{
			"space_id":  spaceID,
			"node_id":   node.NodeID,
			"node_type": node.Type,
		},
	}
}

// isSupportedDocType checks if a DingTalk node type can be synced.
// folder, sheet, mindmap, and file are skipped in v1.
func isSupportedDocType(nodeType string) bool {
	switch nodeType {
	case "doc":
		return true
	default:
		// folder (container), sheet, mindmap, file — no content retrieval API in v1
		return false
	}
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
