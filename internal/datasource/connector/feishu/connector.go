package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

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
		return fetchDocxWithBlocks(ctx, client, docxFetchInput{
			docToken:          node.NodeToken,
			objToken:          node.ObjToken,
			title:             node.Title,
			url:               c.region.wikiURL(node.NodeToken),
			resourceID:        resourceID,
			editTime:          editTime,
			baseMeta:          baseMeta,
			multimodalEnabled: multimodalEnabled,
		})
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
