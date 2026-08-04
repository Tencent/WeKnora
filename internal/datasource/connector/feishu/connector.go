package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// Connector implements the datasource.Connector interface for Feishu and, with
// the same code, for Lark: the two clouds expose an identical wiki/docx/drive
// API surface. A Region picks the cloud — see region.go.
type Connector struct {
	region Region
}

// NewConnector creates a connector for the given region (RegionFeishu or RegionLark).
func NewConnector(region Region) *Connector {
	return &Connector{region: region}
}

// Feishu supports resumable streaming sync; the service prefers FetchStream over
// FetchAll/FetchIncremental when a connector implements StreamingConnector.
var _ datasource.StreamingConnector = (*Connector)(nil)

// Type returns the connector type identifier.
func (c *Connector) Type() string {
	return c.region.ConnectorType
}

const feishuWikiNodeResourceSeparator = ":"

// feishuStreamCheckpointInterval is how many processed nodes pass between
// cursor checkpoints during a streaming fetch. Small enough that a timed-out
// sync loses little work on resume, large enough that checkpoint persistence
// (a DB write) does not dominate. Overridable in tests. See FetchStream.
var feishuStreamCheckpointInterval = 50

// feishuStreamCheckpointMaxInterval bounds checkpointing by wall-clock time as
// well as node count. Without it, a sync of fewer than
// feishuStreamCheckpointInterval very slow (rate-limited) exports could reach
// the 2h task timeout having never checkpointed, and resume from scratch every
// retry — the #2136 "never fully syncs" case. Overridable in tests.
var feishuStreamCheckpointMaxInterval = 30 * time.Second

// fetchTally accumulates the outcome of fetching a wiki node subtree so the
// connector can emit a single actionable summary. Without it, unsupported nodes
// (mindnote/slides/etc.) vanish with no item, no error and no log, leaving users
// unable to explain why "13 documents synced only 3" (Tencent/WeKnora#2136).
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

// Validate verifies that the Feishu configuration is valid by testing connectivity.
func (c *Connector) Validate(ctx context.Context, config *types.DataSourceConfig) error {
	feishuConfig, err := parseFeishuConfig(config, c.region)
	if err != nil {
		return err
	}

	client := NewClient(feishuConfig)
	if err := client.Ping(ctx); err != nil {
		return fmt.Errorf("feishu connection failed: %w", err)
	}

	return nil
}

// ListResources lists Feishu Wiki resources for selection, loading the tree
// lazily one level at a time to avoid traversing the entire wiki up front.
//
//   - parentID == ""        → list all accessible wiki spaces.
//   - parentID == spaceID   → list the top-level nodes of that space.
//   - parentID == "spaceID:nodeToken" → list the direct children of that node.
//
// Eagerly recursing the whole tree here used to time out for large wikis
// (Tencent/WeKnora#1672); the recursive walk now happens only at sync time.
func (c *Connector) ListResources(
	ctx context.Context, config *types.DataSourceConfig, parentID string,
) ([]types.Resource, error) {
	feishuConfig, err := parseFeishuConfig(config, c.region)
	if err != nil {
		return nil, err
	}

	client := NewClient(feishuConfig)

	if parentID == "" {
		spaces, err := client.ListWikiSpaces(ctx)
		if err != nil {
			return nil, fmt.Errorf("list feishu wiki spaces: %w", err)
		}

		resources := make([]types.Resource, 0, len(spaces))
		for _, space := range spaces {
			resources = append(resources, types.Resource{
				ExternalID:  space.SpaceID,
				Name:        space.Name,
				Type:        "wiki_space",
				Description: space.Description,
				URL:         c.region.wikiURL(space.SpaceID),
				HasChildren: true,
				Metadata: map[string]interface{}{
					"visibility": space.Visibility,
					"space_id":   space.SpaceID,
				},
			})
		}
		return resources, nil
	}

	// Lazy load: list only the direct children of the given space / node.
	spaceID, nodeToken := parseWikiResourceID(parentID)
	nodes, err := client.ListWikiNodes(ctx, spaceID, nodeToken)
	if err != nil {
		return nil, fmt.Errorf("list feishu wiki nodes under %s: %w", parentID, err)
	}

	resources := make([]types.Resource, 0, len(nodes))
	for _, node := range nodes {
		resources = append(resources, c.wikiNodeToResource(spaceID, node))
	}
	return resources, nil
}

// ResolveResourceAncestors returns the resource IDs of every parent that has to
// be expanded so the lazily-loaded picker can reveal each given selection. For a
// selected node "spaceID:nodeToken" that is its space plus every intermediate
// node up the tree; the walk uses GetWikiNode (parent_node_token) and is O(depth)
// per selection, so it never re-traverses the whole wiki.
func (c *Connector) ResolveResourceAncestors(
	ctx context.Context, config *types.DataSourceConfig, resourceIDs []string,
) ([]string, error) {
	feishuConfig, err := parseFeishuConfig(config, c.region)
	if err != nil {
		return nil, err
	}
	client := NewClient(feishuConfig)

	seen := make(map[string]bool)
	ancestors := make([]string, 0)
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			ancestors = append(ancestors, id)
		}
	}

	for _, rid := range resourceIDs {
		spaceID, nodeToken := parseWikiResourceID(rid)
		if spaceID == "" || nodeToken == "" {
			// A space-level selection is already a top-level node in the picker;
			// there is nothing above it to reveal.
			continue
		}
		// The space's direct children must be loaded to reveal the top-level node.
		add(spaceID)

		// Walk up from the selection to the top, loading each intermediate
		// parent so the path down to the selection becomes visible.
		current := nodeToken
		for current != "" {
			node, err := client.GetWikiNode(ctx, spaceID, current)
			if err != nil {
				// Best-effort: a broken path just stays collapsed, the rest of
				// the selections are still revealed.
				logger.Warnf(ctx, "[Feishu] resolve ancestors: get node %s:%s: %v", spaceID, current, err)
				break
			}
			if node.ParentNodeID == "" {
				break
			}
			add(makeWikiNodeResourceID(spaceID, node.ParentNodeID))
			current = node.ParentNodeID
		}
	}

	return ancestors, nil
}

// FetchAll performs a full sync of all documents from the specified wiki spaces.
func (c *Connector) FetchAll(ctx context.Context, config *types.DataSourceConfig, resourceIDs []string) ([]types.FetchedItem, error) {
	feishuConfig, err := parseFeishuConfig(config, c.region)
	if err != nil {
		return nil, err
	}

	client := NewClient(feishuConfig)

	var allItems []types.FetchedItem

	for _, resourceID := range resourceIDs {
		spaceID, nodeToken := parseWikiResourceID(resourceID)
		// List all nodes in this wiki space or selected node subtree recursively
		nodes, err := client.ListWikiNodesRecursiveFrom(ctx, spaceID, nodeToken)
		if err != nil {
			var partialErr *partialWikiNodeListError
			if !errors.As(err, &partialErr) {
				return nil, fmt.Errorf("list nodes for resource %s: %w", resourceID, err)
			}
			allItems = appendWikiNodeListFailureItems(allItems, spaceID, resourceID, partialErr.Failures)
		}

		// Fetch content for each document node, tallying outcomes so a single
		// summary line explains where every discovered node went.
		tally := newFetchTally(len(nodes))
		for i, node := range nodes {
			items, err := c.fetchNodeContent(ctx, client, node, spaceID, resourceID, config.MultimodalEnabled)
			if err != nil {
				tally.fail()
				// Log error but continue with other nodes
				allItems = append(allItems, types.FetchedItem{
					ExternalID:       node.NodeToken,
					Title:            node.Title,
					SourceResourceID: resourceID,
					Metadata:         feishuErrorItemMeta(err, nil),
				})
				continue
			}
			if len(items) > 0 {
				tally.fetch()
				for _, it := range items {
					allItems = append(allItems, *it)
				}
			} else {
				// Unsupported obj_type (mindnote/slides/…): skipped with no item.
				tally.skip(node.ObjType)
			}
			if n := i + 1; n%100 == 0 {
				logger.Infof(ctx, "[Feishu] sync progress resource=%s %d/%d (%s)",
					resourceID, n, len(nodes), tally.summary())
			}
		}
		logger.Infof(ctx, "[Feishu] sync summary resource=%s %s", resourceID, tally.summary())
	}

	return allItems, nil
}

// FetchIncremental performs an incremental sync by comparing node edit times
// against the previously recorded state.
func (c *Connector) FetchIncremental(ctx context.Context, config *types.DataSourceConfig, cursor *types.SyncCursor) ([]types.FetchedItem, *types.SyncCursor, error) {
	feishuConfig, err := parseFeishuConfig(config, c.region)
	if err != nil {
		return nil, nil, err
	}

	client := NewClient(feishuConfig)

	// Parse the previous cursor state
	var prevCursor feishuCursor
	if cursor != nil && cursor.ConnectorCursor != nil {
		cursorBytes, _ := json.Marshal(cursor.ConnectorCursor)
		_ = json.Unmarshal(cursorBytes, &prevCursor)
	}

	// Build new cursor to track current state
	newCursor := feishuCursor{
		LastSyncTime:   time.Now(),
		SpaceNodeTimes: make(map[string]map[string]string),
	}

	var changedItems []types.FetchedItem

	// Get resource IDs from config
	resourceIDs := config.ResourceIDs
	if len(resourceIDs) == 0 {
		return nil, nil, fmt.Errorf("no resource IDs (wiki space IDs or wiki node IDs) configured")
	}

	for _, resourceID := range resourceIDs {
		spaceID, nodeToken := parseWikiResourceID(resourceID)
		// List all nodes in this wiki space or selected node subtree
		nodes, err := client.ListWikiNodesRecursiveFrom(ctx, spaceID, nodeToken)
		var partialErr *partialWikiNodeListError
		if err != nil {
			if !errors.As(err, &partialErr) {
				return nil, nil, fmt.Errorf("list nodes for resource %s: %w", resourceID, err)
			}
			changedItems = appendWikiNodeListFailureItems(changedItems, spaceID, resourceID, partialErr.Failures)
		}

		newCursor.SpaceNodeTimes[resourceID] = make(map[string]string)
		if partialErr != nil && prevCursor.SpaceNodeTimes != nil {
			if prevTimes, ok := prevCursor.SpaceNodeTimes[resourceID]; ok {
				for nodeToken, editTime := range prevTimes {
					newCursor.SpaceNodeTimes[resourceID][nodeToken] = editTime
				}
			}
		}

		// Build a set of current node tokens for deletion detection
		currentNodes := make(map[string]bool)

		for _, node := range nodes {
			currentNodes[node.NodeToken] = true
			// Use ObjEditTime (document content edit time) for change detection,
			// NOT NodeEditTime which only tracks node attribute changes (title, position).
			editTimeStr := node.ObjEditTime
			if editTimeStr == "" {
				editTimeStr = node.NodeEditTime // fallback for nodes that don't have obj_edit_time
			}
			newCursor.SpaceNodeTimes[resourceID][node.NodeToken] = editTimeStr

			// Check if node has changed since last sync
			if prevCursor.SpaceNodeTimes != nil {
				if prevTimes, ok := prevCursor.SpaceNodeTimes[resourceID]; ok {
					if prevEditTime, exists := prevTimes[node.NodeToken]; exists {
						if prevEditTime == editTimeStr {
							// Node unchanged, skip
							continue
						}
					}
				}
			}

			// Node is new or changed — fetch its content
			fetchedItems, err := c.fetchNodeContent(ctx, client, node, spaceID, resourceID, config.MultimodalEnabled)
			if err != nil {
				// Record failed items
				changedItems = append(changedItems, types.FetchedItem{
					ExternalID:       node.NodeToken,
					Title:            node.Title,
					SourceResourceID: resourceID,
					Metadata:         feishuErrorItemMeta(err, nil),
				})
				continue
			}
			for _, it := range fetchedItems {
				changedItems = append(changedItems, *it)
			}
		}

		// Detect deleted nodes
		if partialErr == nil && prevCursor.SpaceNodeTimes != nil {
			if prevTimes, ok := prevCursor.SpaceNodeTimes[resourceID]; ok {
				for nodeToken := range prevTimes {
					if !currentNodes[nodeToken] {
						// Node was deleted
						changedItems = append(changedItems, types.FetchedItem{
							ExternalID:       nodeToken,
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
// checkpoint instead of restarting (Tencent/WeKnora#2136).
//
// Instead of accumulating every item in memory (FetchAll), it Emits each item
// as it is fetched and Checkpoints the cursor every feishuStreamCheckpointInterval
// processed nodes, so progress is durable across the Asynq task's 2h timeout.
func (c *Connector) FetchStream(
	ctx context.Context, config *types.DataSourceConfig,
	cursor *types.SyncCursor, h datasource.StreamHandler,
) (*types.SyncCursor, error) {
	feishuConfig, err := parseFeishuConfig(config, c.region)
	if err != nil {
		return nil, err
	}
	client := NewClient(feishuConfig)

	var prevCursor feishuCursor
	if cursor != nil && cursor.ConnectorCursor != nil {
		cursorBytes, _ := json.Marshal(cursor.ConnectorCursor)
		_ = json.Unmarshal(cursorBytes, &prevCursor)
	}

	newCursor := feishuCursor{
		LastSyncTime:   time.Now(),
		SpaceNodeTimes: make(map[string]map[string]string),
	}

	resourceIDs := config.ResourceIDs
	if len(resourceIDs) == 0 {
		return nil, fmt.Errorf("no resource IDs (wiki space IDs or wiki node IDs) configured")
	}

	processed := 0
	lastCheckpoint := time.Now()
	for _, resourceID := range resourceIDs {
		spaceID, nodeToken := parseWikiResourceID(resourceID)
		nodes, err := client.ListWikiNodesRecursiveFrom(ctx, spaceID, nodeToken)
		var partialErr *partialWikiNodeListError
		if err != nil {
			if !errors.As(err, &partialErr) {
				return nil, fmt.Errorf("list nodes for resource %s: %w", resourceID, err)
			}
			for _, item := range appendWikiNodeListFailureItems(nil, spaceID, resourceID, partialErr.Failures) {
				if eerr := h.Emit(ctx, item); eerr != nil {
					return nil, eerr
				}
			}
		}

		newCursor.SpaceNodeTimes[resourceID] = make(map[string]string)
		// On a partial listing, carry prior edit times forward so a later full
		// listing can still detect changes and deletions.
		if partialErr != nil && prevCursor.SpaceNodeTimes != nil {
			if prevTimes, ok := prevCursor.SpaceNodeTimes[resourceID]; ok {
				for tok, et := range prevTimes {
					newCursor.SpaceNodeTimes[resourceID][tok] = et
				}
			}
		}

		currentNodes := make(map[string]bool)
		tally := newFetchTally(len(nodes))
		for i, node := range nodes {
			currentNodes[node.NodeToken] = true
			editTimeStr := node.ObjEditTime
			if editTimeStr == "" {
				editTimeStr = node.NodeEditTime
			}

			// Prior recorded edit time for this node, if any.
			var prevEdit string
			var hadPrev bool
			if prevCursor.SpaceNodeTimes != nil {
				if prevTimes, ok := prevCursor.SpaceNodeTimes[resourceID]; ok {
					prevEdit, hadPrev = prevTimes[node.NodeToken]
				}
			}

			// Resume/incremental fast-path: a node recorded at its current edit
			// time is unchanged (or already synced this run) — keep the record
			// and skip re-fetching.
			if hadPrev && prevEdit == editTimeStr {
				newCursor.SpaceNodeTimes[resourceID][node.NodeToken] = editTimeStr
				continue
			}

			items, ferr := c.fetchNodeContent(ctx, client, node, spaceID, resourceID, config.MultimodalEnabled)
			if ferr != nil {
				tally.fail()
				// Do NOT advance the cursor: the content was never fetched.
				// Retain the prior edit time (if any) so prev != current next
				// run and the node is retried, instead of being permanently
				// skipped on a transient export failure (Tencent/WeKnora#2136).
				if hadPrev {
					newCursor.SpaceNodeTimes[resourceID][node.NodeToken] = prevEdit
				}
				if eerr := h.Emit(ctx, types.FetchedItem{
					ExternalID:       node.NodeToken,
					Title:            node.Title,
					SourceResourceID: resourceID,
					Metadata:         feishuErrorItemMeta(ferr, nil),
				}); eerr != nil {
					return nil, eerr
				}
			} else {
				// Fetched, or an unsupported obj_type (nothing to fetch): record
				// the current edit time so the node is not re-processed next run.
				newCursor.SpaceNodeTimes[resourceID][node.NodeToken] = editTimeStr
				if len(items) > 0 {
					tally.fetch()
					for _, it := range items {
						if eerr := h.Emit(ctx, *it); eerr != nil {
							return nil, eerr
						}
					}
				} else {
					// Unsupported obj_type (mindnote/slides/…): no item.
					tally.skip(node.ObjType)
				}
			}

			processed++
			if processed%feishuStreamCheckpointInterval == 0 || time.Since(lastCheckpoint) >= feishuStreamCheckpointMaxInterval {
				if cerr := h.Checkpoint(ctx, newCursor.toSyncCursor()); cerr != nil {
					logger.Warnf(ctx, "[Feishu] stream checkpoint failed: %v", cerr)
				}
				lastCheckpoint = time.Now()
			}
			if n := i + 1; n%100 == 0 {
				logger.Infof(ctx, "[Feishu] stream progress resource=%s %d/%d (%s)",
					resourceID, n, len(nodes), tally.summary())
			}
		}

		// Detect deleted nodes (only when the full tree was listed successfully).
		if partialErr == nil && prevCursor.SpaceNodeTimes != nil {
			if prevTimes, ok := prevCursor.SpaceNodeTimes[resourceID]; ok {
				for tok := range prevTimes {
					if !currentNodes[tok] {
						if eerr := h.Emit(ctx, types.FetchedItem{
							ExternalID:       tok,
							IsDeleted:        true,
							SourceResourceID: resourceID,
						}); eerr != nil {
							return nil, eerr
						}
					}
				}
			}
		}
		logger.Infof(ctx, "[Feishu] stream summary resource=%s %s", resourceID, tally.summary())
	}

	return newCursor.toSyncCursor(), nil
}

// toSyncCursor converts the connector-specific feishuCursor into the generic
// SyncCursor persisted by the service. It marshals through JSON so the returned
// value is a snapshot, decoupled from later mutation of the connector's maps.
func (fc feishuCursor) toSyncCursor() *types.SyncCursor {
	m := make(map[string]interface{})
	cursorBytes, _ := json.Marshal(fc)
	_ = json.Unmarshal(cursorBytes, &m)
	return &types.SyncCursor{
		LastSyncTime:    fc.LastSyncTime,
		ConnectorCursor: m,
	}
}

var reFeishuErrorCode = regexp.MustCompile(`code["\s]*[:=]\s*(\d+)`)

// feishuErrorCode extracts the numeric Feishu error code from a raw error string
// (e.g. `body={"code":1663,...}` or `code=1663`), best-effort.
func feishuErrorCode(raw string) string {
	if m := reFeishuErrorCode.FindStringSubmatch(raw); len(m) == 2 {
		return m[1]
	}
	return ""
}

// feishuFailure classifies a raw connector/API error into a stable i18n code
// (mapped to a localized string on the frontend), an optional numeric Feishu
// error code for interpolation, and an English fallback message for clients
// without the i18n key. The raw status/JSON body/log_id is never returned here —
// it stays in the server logs. Dumping it in the UI is the anti-pattern
// Airbyte/Fivetran/Onyx warn against. Transient errors are retried next sync
// (the cursor is retained); auth/permission errors point at the fix instead.
func feishuFailure(err error) (code, codeValue, fallback string) {
	if err == nil {
		return "sync_failed", "", "Sync failed; will retry on the next sync"
	}
	s := strings.ToLower(err.Error())

	switch {
	case strings.Contains(s, "auth error"),
		strings.Contains(s, "invalid access token"),
		strings.Contains(s, "permission"),
		strings.Contains(s, "forbidden"),
		strings.Contains(s, "status=403"):
		return "feishu_auth_or_permission", "", "Authentication or permission error; check credentials and app scopes"
	case strings.Contains(s, "rate limited"), strings.Contains(s, "status=429"):
		return "feishu_rate_limited", "", "Feishu API rate limited; will retry on the next sync"
	case strings.Contains(s, "timed out"),
		strings.Contains(s, "timeout"),
		strings.Contains(s, "deadline exceeded"):
		return "feishu_timeout", "", "Export or request timed out; will retry on the next sync"
	case strings.Contains(s, "server error"):
		return "feishu_server_unavailable", "", "Feishu service temporarily unavailable; will retry on the next sync"
	case strings.Contains(s, "api error"),
		strings.Contains(s, "export task failed"),
		strings.Contains(s, "download failed"):
		if v := feishuErrorCode(err.Error()); v != "" {
			return "feishu_api_error", v, fmt.Sprintf("Feishu API error (code=%s); will retry on the next sync", v)
		}
		return "feishu_api_error_generic", "", "Feishu API error; will retry on the next sync"
	default:
		return "sync_failed", "", "Sync failed; will retry on the next sync"
	}
}

// feishuErrorItemMeta builds the metadata for a failed item: the raw error (for
// server logs) plus the classified i18n code / param / fallback (for a
// localisable SyncItemError in the UI), merged with any caller-supplied extras.
func feishuErrorItemMeta(err error, extra map[string]string) map[string]string {
	code, codeValue, fallback := feishuFailure(err)
	m := map[string]string{
		"error":             err.Error(),
		"error_reason_code": code,
		"error_reason":      fallback,
	}
	if codeValue != "" {
		m["error_reason_code_value"] = codeValue
	}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func appendWikiNodeListFailureItems(items []types.FetchedItem, spaceID string, resourceID string, failures []wikiNodeListFailure) []types.FetchedItem {
	for _, failure := range failures {
		node := failure.Node
		title := node.Title
		if title == "" {
			title = node.NodeToken
		}
		items = append(items, types.FetchedItem{
			ExternalID:       node.NodeToken,
			Title:            title,
			SourceResourceID: resourceID,
			Metadata: feishuErrorItemMeta(failure.Err, map[string]string{
				"channel":       types.ChannelFeishu,
				"node_token":    node.NodeToken,
				"space_id":      spaceID,
				"failure_stage": "list_children",
			}),
		})
	}
	return items
}

// parseableAttachmentExts are attachment extensions worth ingesting as their
// own knowledge entries; other files (icons, tiny decor) are skipped.
var parseableAttachmentExts = map[string]bool{
	".pdf": true, ".doc": true, ".docx": true, ".xls": true, ".xlsx": true,
	".ppt": true, ".pptx": true, ".txt": true, ".md": true, ".csv": true,
}

// minAttachmentBytes filters out decorative micro-files.
const minAttachmentBytes = 2 * 1024

// fetchNodeContent fetches the content of a single wiki node and converts it to a
// slice of FetchedItems. For docx nodes it fans out into a main Markdown document
// plus optional attachment sub-items. Dispatches to different retrieval strategies
// based on obj_type:
//   - docx       → blocks API (Markdown) with export fallback; may return attachments
//   - doc/sheet/bitable → export API → binary file
//   - file       → drive download → original file (PDF/Word/image/etc.)
//   - mindnote   → skip (no API)
//   - slides     → skip (no API)
func (c *Connector) fetchNodeContent(ctx context.Context, client *Client, node wikiNode, spaceID string, resourceID string, multimodalEnabled bool) ([]*types.FetchedItem, error) {
	if !isSupportedDocType(node.ObjType) {
		return nil, nil
	}

	editTime := parseFeishuTimestamp(node.NodeEditTime)
	baseMeta := map[string]string{
		"obj_token":  node.ObjToken,
		"obj_type":   node.ObjType,
		"node_token": node.NodeToken,
		"space_id":   spaceID,
		"creator":    node.Creator,
		"owner":      node.Owner,
		"channel":    types.ChannelFeishu,
	}

	switch node.ObjType {
	case "docx":
		return c.fetchDocxWithBlocks(ctx, client, node, resourceID, editTime, baseMeta, multimodalEnabled)
	case "doc", "sheet", "bitable":
		item, err := c.fetchViaExport(ctx, client, node, resourceID, editTime, baseMeta)
		if err != nil {
			return nil, err
		}
		return []*types.FetchedItem{item}, nil
	case "file":
		item, err := c.fetchDriveFile(ctx, client, node, resourceID, editTime, baseMeta)
		if err != nil {
			return nil, err
		}
		return []*types.FetchedItem{item}, nil
	default:
		return nil, nil
	}
}

// fetchViaExport exports a doc/sheet/bitable node via the async export API and
// returns a single FetchedItem containing the exported binary.
func (c *Connector) fetchViaExport(ctx context.Context, client *Client, node wikiNode, resourceID string, editTime time.Time, baseMeta map[string]string) (*types.FetchedItem, error) {
	// Export as a file via the async export API
	data, fileName, err := client.ExportAndDownload(ctx, node.ObjToken, node.ObjType)
	if err != nil {
		return nil, fmt.Errorf("export %s (%s): %w", node.Title, node.ObjType, err)
	}

	// Ensure a reasonable file name with correct extension
	ext := exportFileExtToSuffix[objTypeToExportFileExtension[node.ObjType]]
	if fileName == "" {
		fileName = sanitizeFileName(node.Title) + ext
	} else if !strings.HasSuffix(strings.ToLower(fileName), ext) {
		// Feishu often returns the doc title without extension — append it
		fileName = sanitizeFileName(fileName) + ext
	}

	return &types.FetchedItem{
		ExternalID:       node.NodeToken,
		Title:            node.Title,
		Content:          data,
		ContentType:      "application/octet-stream",
		FileName:         fileName,
		URL:              c.region.wikiURL(node.NodeToken),
		UpdatedAt:        editTime,
		SourceResourceID: resourceID,
		Metadata:         baseMeta,
	}, nil
}

// fetchDriveFile downloads an original uploaded file from Drive and returns a
// single FetchedItem containing the raw bytes.
func (c *Connector) fetchDriveFile(ctx context.Context, client *Client, node wikiNode, resourceID string, editTime time.Time, baseMeta map[string]string) (*types.FetchedItem, error) {
	// Download the original uploaded file from Drive
	data, err := client.DownloadDriveFile(ctx, node.ObjToken)
	if err != nil {
		return nil, fmt.Errorf("download file %s (%s): %w", node.Title, node.ObjToken, err)
	}

	// Use the node title as file name; it usually preserves the original extension
	fileName := node.Title
	if fileName == "" {
		fileName = node.ObjToken
	}

	return &types.FetchedItem{
		ExternalID:       node.NodeToken,
		Title:            node.Title,
		Content:          data,
		ContentType:      "application/octet-stream",
		FileName:         fileName,
		URL:              c.region.wikiURL(node.NodeToken),
		UpdatedAt:        editTime,
		SourceResourceID: resourceID,
		Metadata:         baseMeta,
	}, nil
}

// fetchDocxWithBlocks retrieves a docx node via the blocks API, converts it to
// Markdown, and returns a main item plus any parseable attachment sub-items.
// Falls back to the export API if the blocks API returns an error.
func (c *Connector) fetchDocxWithBlocks(ctx context.Context, client *Client, node wikiNode,
	resourceID string, editTime time.Time, baseMeta map[string]string, multimodalEnabled bool) ([]*types.FetchedItem, error) {

	blocks, err := client.ListDocumentBlocks(ctx, node.ObjToken)
	if err != nil {
		logger.Warnf(ctx, "[Feishu] blocks API failed for %s (%s), falling back to export: %v",
			node.Title, node.ObjToken, err)
		item, ferr := c.fetchViaExport(ctx, client, node, resourceID, editTime, baseMeta)
		if ferr != nil {
			return nil, ferr
		}
		// Do NOT set ReplacesSubtree here. The export path cannot re-enumerate the
		// doc's attachments, so it emits no sub-items. If the blocks API failed
		// transiently (rate limit / 5xx), sweeping would delete the good attachment
		// children from the prior blocks-path sync with nothing to replace them —
		// silent data loss. Leaving stale children is the safe choice; they are
		// reconciled on the next successful blocks-path sync.
		return []*types.FetchedItem{item}, nil
	}

	md, atts, err := blocksToMarkdown(ctx, client, blocks)
	if err != nil {
		return nil, fmt.Errorf("convert blocks %s: %w", node.Title, err)
	}

	// A docx that renders to empty Markdown (a blank page, or only block types that
	// produce no text) must NOT be emitted as a main item with empty Content plus a
	// wiki URL: ingestItem would then take its URL branch and CreateKnowledgeFromURL
	// against the login-gated Feishu page — a guaranteed failure that reports the
	// node as Failed. Fall back to the export path, which always yields a valid (if
	// minimal) .docx binary, preserving the invariant that a supported docx node
	// ingests as content bytes. An empty render implies no File/Image blocks either
	// (both write a placeholder into the Markdown), so no attachment/image sub-items
	// are lost by skipping the downdrill here.
	if len(strings.TrimSpace(string(md))) == 0 {
		logger.Infof(ctx, "[Feishu] doc %s (%s): blocks rendered empty Markdown, falling back to export",
			node.Title, node.ObjToken)
		item, ferr := c.fetchViaExport(ctx, client, node, resourceID, editTime, baseMeta)
		if ferr != nil {
			return nil, ferr
		}
		return []*types.FetchedItem{item}, nil
	}

	main := &types.FetchedItem{
		ExternalID:       node.NodeToken,
		Title:            node.Title,
		Content:          md,
		ContentType:      "text/markdown",
		FileName:         sanitizeFileName(node.Title) + ".md",
		URL:              c.region.wikiURL(node.NodeToken),
		UpdatedAt:        editTime,
		SourceResourceID: resourceID,
		Metadata:         baseMeta,
		ReplacesSubtree:  true, // sweep stale attachment sub-items on re-sync
	}
	items := []*types.FetchedItem{main}

	// keep collects the external_id of every attachment still present in the doc
	// this sync — parseable or not, downloaded or not. The subtree sweep deletes
	// only children NOT in this set, so an attachment that is still present but
	// could not be re-ingested this cycle (unclassifiable filename, transient
	// download failure) keeps its previously-synced good copy instead of being
	// deleted with nothing to replace it. Only attachments genuinely removed from
	// the doc fall out of keep and get swept — one bad attachment no longer
	// freezes reconciliation of its siblings.
	keep := make([]string, 0, len(atts))
	childMeta := func() map[string]string {
		m := maps.Clone(baseMeta)
		m["parent_node_token"] = node.NodeToken
		m["attachment"] = "true"
		return m
	}
	for _, a := range atts {
		childID := types.SubtreeChildID(node.NodeToken, "file", a.FileToken)
		keep = append(keep, childID) // present in the doc → never sweep as stale
		ext := strings.ToLower(filepath.Ext(a.Name))
		if ext == "" {
			// No filename extension to classify by: we can neither decide whether
			// the file is parseable nor build a valid typed filename downstream, so
			// it cannot be ingested as a standalone item this cycle. Log instead of
			// dropping silently; its prior copy (if any) is preserved via keep.
			logger.Warnf(ctx, "[Feishu] doc %s: skipping attachment with no usable filename (token=%s name=%q)",
				node.ObjToken, a.FileToken, a.Name)
			continue
		}
		if !parseableAttachmentExts[ext] {
			continue // decorative/non-parseable → not a standalone knowledge item
		}
		data, derr := client.DownloadMediaFile(ctx, a.FileToken)
		if derr != nil {
			// Degrade gracefully: a single failed attachment (revoked token,
			// permission gap, transient error) must NOT discard the already-built
			// document body and its other attachments. Surface it as a per-item
			// sync error (visible in the UI and counted, not just a server log);
			// keep already preserves its prior copy so the sweep won't delete it.
			logger.Warnf(ctx, "[Feishu] doc %s: attachment %q (token=%s) download failed: %v",
				node.ObjToken, a.Name, a.FileToken, derr)
			items = append(items, &types.FetchedItem{
				ExternalID:       childID,
				Title:            a.Name,
				SourceResourceID: resourceID,
				Metadata:         feishuErrorItemMeta(derr, childMeta()),
			})
			continue
		}
		if len(data) < minAttachmentBytes {
			// Below the decorative-micro-file floor. Log rather than drop silently
			// (the sibling skip paths above also surface a note); its prior copy, if
			// any, is preserved via keep.
			logger.Infof(ctx, "[Feishu] doc %s: skipping tiny attachment %q (token=%s, %d bytes < %d)",
				node.ObjToken, a.Name, a.FileToken, len(data), minAttachmentBytes)
			continue
		}
		items = append(items, &types.FetchedItem{
			ExternalID:       childID,
			Title:            a.Name,
			Content:          data,
			ContentType:      "application/octet-stream",
			FileName:         sanitizeFileName(a.Name),
			URL:              c.region.wikiURL(node.NodeToken),
			UpdatedAt:        editTime,
			SourceResourceID: resourceID,
			Metadata:         childMeta(),
		})
	}

	// Embedded images: the Markdown body only carries a token-free placeholder for
	// each image, so image-borne text (screenshots/diagrams) would be unsearchable.
	// Emit each image as a standalone sub-item whose bytes flow through WeKnora's
	// VLM OCR+caption pipeline, recovering that text. The image's external_id is
	// ALWAYS added to keep (so toggling VLM off later does not sweep previously
	// OCR'd images), but the bytes are only downloaded and ingested when the KB has
	// multimodal enabled — ingesting an image into a non-VLM KB is rejected, so
	// doing so would turn every image into a failed sync item.
	imgMeta := func() map[string]string {
		m := maps.Clone(baseMeta)
		m["parent_node_token"] = node.NodeToken
		m["embedded_image"] = "true"
		return m
	}
	for _, b := range blocks {
		if b.BlockType != blockTypeImage || b.Image == nil || b.Image.Token == "" {
			continue
		}
		childID := types.SubtreeChildID(node.NodeToken, "image", b.Image.Token)
		keep = append(keep, childID) // present in the doc → never sweep as stale
		if !multimodalEnabled {
			continue // KB can't OCR images; the inline placeholder is all we keep
		}
		data, derr := client.DownloadMediaFile(ctx, b.Image.Token)
		if derr != nil {
			// A failed image download (revoked token, permission gap, transient
			// error) is a genuine fetch failure, not the best-effort ingest
			// rejection a non-VLM KB produces — surface it as a visible per-item
			// sync error exactly like a failed attachment download, instead of
			// dropping it to a server log only. keep already preserves any prior
			// OCR'd copy so the sweep won't delete it.
			logger.Warnf(ctx, "[Feishu] doc %s: image (token=%s) download failed: %v",
				node.ObjToken, b.Image.Token, derr)
			items = append(items, &types.FetchedItem{
				ExternalID:       childID,
				Title:            fmt.Sprintf("%s（内嵌图片）", node.Title),
				SourceResourceID: resourceID,
				Metadata:         feishuErrorItemMeta(derr, imgMeta()),
			})
			continue
		}
		if len(data) < minAttachmentBytes {
			continue // decorative micro-image (icon/spacer)
		}
		ext, contentType, ok := supportedImageExt(data)
		if !ok {
			logger.Warnf(ctx, "[Feishu] doc %s: skipping image (token=%s) of unsupported type %q",
				node.ObjToken, b.Image.Token, contentType)
			continue
		}
		items = append(items, &types.FetchedItem{
			ExternalID:       childID,
			Title:            fmt.Sprintf("%s（内嵌图片）", node.Title),
			Content:          data,
			ContentType:      contentType,
			FileName:         "image-" + b.Image.Token + ext,
			URL:              c.region.wikiURL(node.NodeToken),
			UpdatedAt:        editTime,
			SourceResourceID: resourceID,
			Metadata:         imgMeta(),
		})
	}

	// Reconcile the subtree against the attachments still present in the doc: the
	// sweep (in datasource_service) deletes only prior children absent from keep.
	main.SubtreeKeep = keep
	return items, nil
}

// supportedImageExt sniffs image bytes and returns the filename extension and
// content type WeKnora accepts for a standalone image knowledge item (png/jpg/
// gif — the image set isValidFileType admits). ok is false for non-image or
// unsupported formats (e.g. webp/bmp), which the caller skips rather than
// mislabel — a wrong extension would fail parsing. The detected content type is
// returned even when ok is false so the caller can log it without re-sniffing.
func supportedImageExt(data []byte) (ext, contentType string, ok bool) {
	switch ct := http.DetectContentType(data); ct {
	case "image/png":
		return ".png", ct, true
	case "image/jpeg":
		return ".jpg", ct, true
	case "image/gif":
		return ".gif", ct, true
	default:
		return "", ct, false
	}
}

// --- Helper functions ---

func makeWikiNodeResourceID(spaceID, nodeToken string) string {
	return spaceID + feishuWikiNodeResourceSeparator + nodeToken
}

func parseWikiResourceID(resourceID string) (spaceID string, nodeToken string) {
	spaceID, nodeToken, _ = strings.Cut(resourceID, feishuWikiNodeResourceSeparator)
	return spaceID, nodeToken
}

func (c *Connector) wikiNodeToResource(spaceID string, node wikiNode) types.Resource {
	parentID := spaceID
	if node.ParentNodeID != "" {
		parentID = makeWikiNodeResourceID(spaceID, node.ParentNodeID)
	}

	name := node.Title
	if name == "" {
		name = node.NodeToken
	}

	modifiedAt := parseFeishuTimestamp(node.ObjEditTime)
	if modifiedAt.IsZero() {
		modifiedAt = parseFeishuTimestamp(node.NodeEditTime)
	}

	return types.Resource{
		ExternalID:  makeWikiNodeResourceID(spaceID, node.NodeToken),
		Name:        name,
		Type:        "wiki_node",
		URL:         c.region.wikiURL(node.NodeToken),
		ParentID:    parentID,
		HasChildren: node.HasChild,
		ModifiedAt:  modifiedAt,
		Metadata: map[string]interface{}{
			"space_id":   spaceID,
			"node_token": node.NodeToken,
			"obj_token":  node.ObjToken,
			"obj_type":   node.ObjType,
		},
	}
}

// parseFeishuConfig extracts and validates Feishu/Lark-specific configuration.
//
// base_url stays an explicit override so existing data sources that pointed a
// "feishu" connector at open.larksuite.com keep working; when it is unset the
// region's own host is filled in, making the resolved Config.BaseURL concrete
// for everything downstream.
func parseFeishuConfig(config *types.DataSourceConfig, region Region) (*Config, error) {
	if config == nil {
		return nil, fmt.Errorf("config is nil")
	}

	credBytes, err := json.Marshal(config.Credentials)
	if err != nil {
		return nil, fmt.Errorf("marshal credentials: %w", err)
	}

	var feishuConfig Config
	if err := json.Unmarshal(credBytes, &feishuConfig); err != nil {
		return nil, fmt.Errorf("parse %s credentials: %w", region.ConnectorType, err)
	}

	if feishuConfig.AppID == "" || feishuConfig.AppSecret == "" {
		return nil, fmt.Errorf("%s app_id and app_secret are required", region.ConnectorType)
	}

	if feishuConfig.BaseURL == "" {
		feishuConfig.BaseURL = region.OpenBaseURL
	}

	// Timezone is a display setting (bitable date rendering), not a credential, so
	// it lives in Settings. Empty falls back to GMT+8 in resolveLocation.
	if feishuConfig.Timezone == "" && config.Settings != nil {
		if tz, ok := config.Settings["timezone"].(string); ok {
			feishuConfig.Timezone = strings.TrimSpace(tz)
		}
	}

	if err := datasource.ValidateConnectorBaseURL(feishuConfig.GetBaseURL()); err != nil {
		return nil, err
	}

	return &feishuConfig, nil
}

// isSupportedDocType checks if a Feishu document type can be synced.
// mindnote and slides have no content read API and are skipped.
func isSupportedDocType(objType string) bool {
	switch objType {
	case "docx", "doc", "sheet", "bitable", "file":
		return true
	default:
		// mindnote, slides — no content retrieval API available
		return false
	}
}

// parseFeishuTimestamp parses a Feishu unix timestamp string (seconds) into time.Time.
func parseFeishuTimestamp(ts string) time.Time {
	if ts == "" {
		return time.Time{}
	}
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(sec, 0)
}

// sanitizeFileName removes characters that are invalid in filenames and
// truncates at a UTF-8 rune boundary. Raw byte truncation would split a
// multi-byte codepoint (Chinese chars are 3 bytes) and produce invalid UTF-8
// that downstream validation (utf8.ValidString) rejects.
//
// The extension is preserved across truncation: only the base name is trimmed,
// so a long attachment name like "很长的名字….pdf" keeps its ".pdf" suffix that
// downstream file-type classification depends on.
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
	if len(result) <= maxBytes {
		return result
	}
	ext := filepath.Ext(result)
	if len(ext) >= maxBytes {
		ext = "" // pathological: extension alone overflows the budget → drop it
	}
	base := truncateUTF8(result[:len(result)-len(ext)], maxBytes-len(ext))
	return base + ext
}

// truncateUTF8 shortens s to at most maxBytes bytes without splitting a
// multi-byte rune: after a hard byte cut it trims any trailing partial codepoint.
func truncateUTF8(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	s = s[:maxBytes]
	for len(s) > 0 {
		r, size := utf8.DecodeLastRuneInString(s)
		if r != utf8.RuneError || size != 1 {
			break
		}
		s = s[:len(s)-1]
	}
	return s
}
