package notion

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	contentTypeMarkdown  = "text/markdown"
	objectTypePage       = "page"
	objectTypeDatabase   = "database"
	objectTypeAttachment = "attachment"
	defaultUntitledName  = "Untitled"
)

// Connector implements the datasource.Connector interface for Notion.
type Connector struct{}

// NewConnector creates a new Notion connector.
func NewConnector() *Connector {
	return &Connector{}
}

// Type returns the connector type identifier.
func (c *Connector) Type() string {
	return types.ConnectorTypeNotion
}

// Validate verifies that the Notion configuration is valid by testing connectivity.
func (c *Connector) Validate(ctx context.Context, config *types.DataSourceConfig) error {
	notionCfg, err := parseNotionConfig(config)
	if err != nil {
		return err
	}

	client := newClient(notionCfg.APIKey, extractBaseURL(config))
	return client.Ping(ctx)
}

// ListResources lists all accessible Notion pages and databases as selectable resources.
// Returns all objects with parent-child relationships populated, allowing the frontend
// to render a tree view. Root objects have empty ParentID.
func (c *Connector) ListResources(ctx context.Context, config *types.DataSourceConfig) ([]types.Resource, error) {
	notionCfg, err := parseNotionConfig(config)
	if err != nil {
		return nil, err
	}

	client := newClient(notionCfg.APIKey, extractBaseURL(config))
	pages, err := client.SearchPages(ctx)
	if err != nil {
		return nil, fmt.Errorf("search notion pages: %w", err)
	}

	allIDs := make(map[string]bool, len(pages))
	for _, p := range pages {
		allIDs[p.ID] = true
	}

	// Precompute effective parent for each page.
	// data_source objects need database_parent (their `parent` points at the
	// database container, not the workspace location); see resolveParentID.
	parentOf := make(map[string]string, len(pages))
	for _, p := range pages {
		if p.InTrash {
			continue
		}
		parentOf[p.ID] = resolveParentID(p, allIDs)
	}

	childrenCount := make(map[string]int)
	for _, pid := range parentOf {
		if pid != "" {
			childrenCount[pid]++
		}
	}

	var resources []types.Resource
	for _, p := range pages {
		if p.InTrash {
			continue
		}

		resourceType := objectTypePage
		if p.isDatabase() {
			resourceType = objectTypeDatabase
		}

		resources = append(resources, types.Resource{
			ExternalID:  p.ID,
			Name:        p.Title,
			Type:        resourceType,
			URL:         p.URL,
			ParentID:    parentOf[p.ID],
			HasChildren: childrenCount[p.ID] > 0,
		})
	}

	return resources, nil
}

// FetchAll performs a full sync of all documents from the specified resources.
func (c *Connector) FetchAll(ctx context.Context, config *types.DataSourceConfig, resourceIDs []string) ([]types.FetchedItem, error) {
	notionCfg, err := parseNotionConfig(config)
	if err != nil {
		return nil, err
	}

	client := newClient(notionCfg.APIKey, extractBaseURL(config))
	visited := c.excludedSetFromListResources(ctx, config, resourceIDs)
	var allItems []types.FetchedItem

	for _, resourceID := range resourceIDs {
		page, err := client.GetPage(ctx, resourceID)
		if err == nil {
			allItems = append(allItems, c.fetchPage(ctx, client, page, visited)...)
			continue
		}
		// Not a page — treat as database/data_source.
		// fetchDatabase handles both data_source IDs (from search) and
		// database container IDs (from child_database blocks).
		items := c.fetchDatabase(ctx, client, resourceID, visited)
		if len(items) == 0 {
			logger.Warnf(ctx, "[Notion] failed to fetch resource %s as page or database", resourceID)
		}
		allItems = append(allItems, items...)
	}

	return allItems, nil
}

// FetchIncremental performs an incremental sync based on cursor state.
// On first sync (nil cursor), delegates to FetchAll for efficiency (no discovery needed).
func (c *Connector) FetchIncremental(ctx context.Context, config *types.DataSourceConfig, cursor *types.SyncCursor) ([]types.FetchedItem, *types.SyncCursor, error) {
	notionCfg, err := parseNotionConfig(config)
	if err != nil {
		return nil, nil, err
	}

	resourceIDs := config.ResourceIDs
	if len(resourceIDs) == 0 {
		return nil, nil, fmt.Errorf("no resource IDs configured")
	}

	client := newClient(notionCfg.APIKey, extractBaseURL(config))

	// Parse previous cursor
	var prevCursor notionCursor
	if cursor != nil && cursor.ConnectorCursor != nil {
		cursorBytes, _ := json.Marshal(cursor.ConnectorCursor)
		_ = json.Unmarshal(cursorBytes, &prevCursor)
	}

	// First sync: use FetchAll then build cursor from Search API (one call, no block traversal)
	isFirstSync := len(prevCursor.PageEditTimes) == 0
	if isFirstSync {
		logger.Infof(ctx, "[Notion] first sync, using FetchAll for %d resources", len(resourceIDs))
		items, err := c.FetchAll(ctx, config, resourceIDs)
		if err != nil {
			return nil, nil, err
		}

		// Build cursor from fetched items' UpdatedAt timestamps.
		// Record-level edit times are tracked individually (object_type == "page").
		// Database container IDs are also added so incremental sync can detect
		// whether a database actually changed, avoiding full record queries every cycle.
		newEditTimes := make(map[string]time.Time)
		for _, item := range items {
			if item.Metadata["object_type"] == objectTypePage {
				newEditTimes[item.ExternalID] = item.UpdatedAt
			}
		}
		// Ensure all selected resourceIDs appear in the cursor.
		// Pages already appear via their items; databases need explicit entries.
		for _, rid := range resourceIDs {
			if _, ok := newEditTimes[rid]; !ok {
				newEditTimes[rid] = time.Now()
			}
		}

		return items, buildCursor(newEditTimes), nil
	}

	// Subsequent syncs: discover all pages, diff against cursor, fetch only changes
	logger.Infof(ctx, "[Notion] incremental sync, discovering pages")
	pages, fetchVisited := c.discoverAllResources(ctx, client, resourceIDs)
	logger.Infof(ctx, "[Notion] discovered %d pages", len(pages))

	newEditTimes := make(map[string]time.Time)
	pageByID := make(map[string]*notionPage, len(pages))
	for i := range pages {
		newEditTimes[pages[i].ID] = pages[i].LastEditedTime
		pageByID[pages[i].ID] = &pages[i]
	}

	var changedItems []types.FetchedItem
	changedCount := 0

	for pageID, newTime := range newEditTimes {
		prevTime, existed := prevCursor.PageEditTimes[pageID]
		if existed && newTime.Equal(prevTime) {
			continue
		}
		if fetchVisited[pageID] {
			continue
		}
		changedCount++
		pg := pageByID[pageID]
		if pg == nil {
			continue
		}
		logger.Debugf(ctx, "[Notion] changed: %s (%s, %s)", pg.Title, pg.ID, pg.Object)
		if pg.isDatabase() {
			// Incremental database sync: query records, diff against cursor,
			// only fetch blocks for records whose edit time actually changed.
			items, recordEditTimes := c.fetchDatabaseIncremental(ctx, client, pg.ID, prevCursor.PageEditTimes, fetchVisited)
			changedItems = append(changedItems, items...)
			// Merge record-level edit times into the cursor
			for rid, rt := range recordEditTimes {
				newEditTimes[rid] = rt
			}
		} else {
			changedItems = append(changedItems, c.fetchPage(ctx, client, pg, fetchVisited)...)
		}
	}

	// Detect deletions. Only emit IsDeleted for pages that previously belonged
	// to a selected resource and have actually disappeared at source. Pages that
	// are still visible but no longer reachable from any selected root (i.e. the
	// user deselected their ancestor) live in fetchVisited as the "excluded" set
	// returned by discoverAllResources, and must NOT be reported as deletions.
	for pageID := range prevCursor.PageEditTimes {
		if _, exists := newEditTimes[pageID]; exists {
			continue
		}
		if fetchVisited[pageID] {
			continue
		}
		changedItems = append(changedItems, types.FetchedItem{
			ExternalID: pageID,
			IsDeleted:  true,
			Metadata:   map[string]string{"channel": types.ChannelNotion},
		})
	}

	logger.Infof(ctx, "[Notion] incremental: %d changed, %d total items", changedCount, len(changedItems))

	return changedItems, buildCursor(newEditTimes), nil
}

func buildCursor(editTimes map[string]time.Time) *types.SyncCursor {
	now := time.Now()
	cursorData := notionCursor{
		PageEditTimes: editTimes,
	}
	cursorBytes, _ := json.Marshal(cursorData)
	var cursorMap map[string]interface{}
	_ = json.Unmarshal(cursorBytes, &cursorMap)

	return &types.SyncCursor{
		LastSyncTime:    now,
		ConnectorCursor: cursorMap,
	}
}

// --- Internal methods ---

// fetchPage fetches a single page's content and attachments.
// If page is nil, it will be fetched from the API.
func (c *Connector) fetchPage(ctx context.Context, client *notionClient, page *notionPage, visited map[string]bool) []types.FetchedItem {
	if page == nil {
		return nil
	}
	if visited[page.ID] {
		return nil
	}
	visited[page.ID] = true

	if page.InTrash {
		return nil
	}

	// Database records store content in properties, not blocks — delegate to
	// buildRecordItem so they aren't dropped as empty pages. Record parent.type
	// can be either "database_id" (older GetPage responses) or "data_source_id"
	// (search API and 2025-09-03+ workspaces); accept both.
	if page.Parent.Type == parentTypeDatabaseID || page.Parent.Type == parentTypeDataSourceID {
		dbTitle := ""
		if dbInfo, err := getDatabaseOrDataSourceInfo(ctx, client, page.Parent.GetParentID()); err != nil {
			logger.Debugf(ctx, "[Notion] failed to resolve parent db title for record %s: %v", page.ID, err)
		} else {
			dbTitle = dbInfo.Page.Title
		}
		propNames := extractPropertySchema(*page)
		if item := c.buildRecordItem(ctx, client, *page, propNames, dbTitle); item != nil {
			return []types.FetchedItem{*item}
		}
		return nil
	}

	blocks, err := client.GetBlockChildrenAll(ctx, page.ID)
	if err != nil {
		logger.Warnf(ctx, "[Notion] failed to get blocks for page %s: %v", page.ID, err)
		return nil
	}

	resolveFileUploads(ctx, client, blocks)

	markdown, attachmentList := BlocksToMarkdown(blocks)

	var items []types.FetchedItem

	// Only skip truly empty pages (no content at all)
	if strings.TrimSpace(markdown) != "" {
		fileName := page.Title + ".md"
		if page.Title == "" {
			fileName = defaultUntitledName + ".md"
		}

		items = append(items, types.FetchedItem{
			ExternalID:  page.ID,
			Title:       page.Title,
			Content:     []byte(markdown),
			ContentType: contentTypeMarkdown,
			FileName:    fileName,
			URL:         page.URL,
			UpdatedAt:   page.LastEditedTime,
			Metadata: map[string]string{
				"channel":     types.ChannelNotion,
				"object_type": objectTypePage,
			},
		})
	}

	// Download file attachments (PDF, documents, etc.)
	// Skip images — they're already referenced in Markdown as ![](url),
	// and downloading them as separate items requires VLM to process.
	for _, att := range attachmentList {
		if att.URL == "" || att.Type == "image" {
			continue
		}
		data, err := client.DownloadFile(ctx, att.URL)
		if err != nil {
			logger.Warnf(ctx, "[Notion] failed to download attachment %s: %v", att.FileName, err)
			continue
		}
		items = append(items, types.FetchedItem{
			ExternalID:       fmt.Sprintf("%s:%s", page.ID, att.FileName),
			Title:            att.FileName,
			Content:          data,
			ContentType:      mimeTypeForAttachment(att.Type),
			FileName:         att.FileName,
			SourceResourceID: page.ID,
			Metadata: map[string]string{
				"channel":     types.ChannelNotion,
				"object_type": objectTypeAttachment,
			},
		})
	}

	for _, block := range blocks {
		switch block.Type {
		case "child_page":
			childPage, err := client.GetPage(ctx, block.ID)
			if err != nil {
				logger.Warnf(ctx, "[Notion] failed to get child page %s: %v", block.ID, err)
				continue
			}
			items = append(items, c.fetchPage(ctx, client, childPage, visited)...)
		case "child_database":
			items = append(items, c.fetchDatabase(ctx, client, block.ID, visited)...)
		}
	}

	return items
}

// fetchDatabase syncs each database record as an individual knowledge item (full sync).
// Accepts either a data_source_id (from search) or database_id (from child_database blocks).
func (c *Connector) fetchDatabase(ctx context.Context, client *notionClient, id string, visited map[string]bool) []types.FetchedItem {
	if visited[id] {
		return nil
	}
	visited[id] = true

	records, dbTitle, queryID, err := c.queryDatabaseRecords(ctx, client, id)
	if err != nil || len(records) == 0 {
		return nil
	}
	// Same database may be reached via two different IDs (data_source_id from search
	// vs database_container_id from child_database blocks). Mark the canonical
	// data_source_id as visited so the second path is deduped.
	if queryID != "" && queryID != id {
		if visited[queryID] {
			return nil
		}
		visited[queryID] = true
	}

	propNames := extractPropertySchema(records[0])
	var items []types.FetchedItem
	for _, record := range records {
		if record.InTrash {
			continue
		}
		item := c.buildRecordItem(ctx, client, record, propNames, dbTitle)
		if item != nil {
			items = append(items, *item)
		}
	}
	return items
}

// fetchDatabaseIncremental syncs only changed records by comparing edit times against cursor.
// Returns fetched items and a map of record_id → edit_time for cursor update.
func (c *Connector) fetchDatabaseIncremental(ctx context.Context, client *notionClient, id string, prevEditTimes map[string]time.Time, visited map[string]bool) ([]types.FetchedItem, map[string]time.Time) {
	if visited[id] {
		return nil, nil
	}
	visited[id] = true

	records, dbTitle, queryID, err := c.queryDatabaseRecords(ctx, client, id)
	if err != nil {
		return nil, nil
	}
	if queryID != "" && queryID != id {
		if visited[queryID] {
			return nil, nil
		}
		visited[queryID] = true
	}

	recordEditTimes := make(map[string]time.Time, len(records))
	var propNames []string
	if len(records) > 0 {
		propNames = extractPropertySchema(records[0])
	}

	var items []types.FetchedItem
	changedCount := 0
	for _, record := range records {
		if record.InTrash {
			continue
		}
		recordEditTimes[record.ID] = record.LastEditedTime

		prevTime, existed := prevEditTimes[record.ID]
		if existed && record.LastEditedTime.Equal(prevTime) {
			continue
		}
		changedCount++
		item := c.buildRecordItem(ctx, client, record, propNames, dbTitle)
		if item != nil {
			items = append(items, *item)
		}
	}

	logger.Infof(ctx, "[Notion] database %s incremental: %d changed out of %d records", id, changedCount, len(records))
	return items, recordEditTimes
}

// queryDatabaseRecords resolves the database ID and queries all records.
// Returns the records, the database title, and the canonical data_source_id used for the query.
func (c *Connector) queryDatabaseRecords(ctx context.Context, client *notionClient, id string) ([]notionPage, string, string, error) {
	dbInfo, err := getDatabaseOrDataSourceInfo(ctx, client, id)
	if err != nil {
		logger.Warnf(ctx, "[Notion] failed to get database/data_source info %s: %v", id, err)
		return nil, "", "", err
	}

	queryID := dbInfo.DataSourceID
	if queryID == "" {
		queryID = id
	}
	records, err := client.QueryDatabaseAll(ctx, queryID)
	if err != nil {
		logger.Warnf(ctx, "[Notion] failed to query database %s: %v", id, err)
		return nil, "", "", err
	}

	logger.Infof(ctx, "[Notion] database %s (%s): %d records", id, dbInfo.Page.Title, len(records))
	return records, dbInfo.Page.Title, queryID, nil
}

// buildRecordItem converts a single database record into a FetchedItem
// with properties as header and block content as body.
func (c *Connector) buildRecordItem(ctx context.Context, client *notionClient, record notionPage, propNames []string, dbTitle string) *types.FetchedItem {
	var content strings.Builder
	title := record.Title
	if title == "" {
		title = defaultUntitledName
	}
	content.WriteString("# " + title + "\n\n")

	if record.RawProperties != nil {
		var props map[string]interface{}
		json.Unmarshal(record.RawProperties, &props)
		for _, name := range propNames {
			if propMap, ok := props[name].(map[string]interface{}); ok {
				val := propertyToString(propMap)
				if val != "" {
					content.WriteString("- **" + name + "**: " + val + "\n")
				}
			}
		}
	}

	// Database records may contain page-style block content beyond their properties
	blocks, err := client.GetBlockChildrenAll(ctx, record.ID)
	if err != nil {
		logger.Warnf(ctx, "[Notion] failed to get blocks for record %s: %v", record.ID, err)
	} else if len(blocks) > 0 {
		resolveFileUploads(ctx, client, blocks)
		markdown, _ := BlocksToMarkdown(blocks)
		if strings.TrimSpace(markdown) != "" {
			content.WriteString("\n" + markdown)
		}
	}

	bodyStr := strings.TrimSpace(content.String())
	if bodyStr == "" {
		return nil
	}

	return &types.FetchedItem{
		ExternalID:  record.ID,
		Title:       title,
		Content:     []byte(bodyStr),
		ContentType: contentTypeMarkdown,
		FileName:    title + ".md",
		URL:         record.URL,
		UpdatedAt:   record.LastEditedTime,
		Metadata: map[string]string{
			"channel":     types.ChannelNotion,
			"object_type": objectTypePage,
			"database":    dbTitle,
		},
	}
}

// discoverAllResources uses the Search API to find all pages and data_sources,
// then filters by parent chain via BFS from the selected resource IDs.
// Returns the included pages plus the excluded set (visible pages NOT under any
// selected root) so callers can seed `visited` for child-block recursion without
// a second SearchPages round-trip.
func (c *Connector) discoverAllResources(ctx context.Context, client *notionClient, resourceIDs []string) (included []notionPage, excluded map[string]bool) {
	allPages, err := client.SearchPages(ctx)
	if err != nil {
		logger.Warnf(ctx, "[Notion] failed to search pages for discovery: %v", err)
		return nil, nil
	}

	// data_source objects use database_parent for workspace hierarchy.
	allIDs := make(map[string]bool, len(allPages))
	pageByID := make(map[string]notionPage, len(allPages))
	for _, p := range allPages {
		allIDs[p.ID] = true
		pageByID[p.ID] = p
	}

	childrenOf := make(map[string][]string)
	for _, p := range allPages {
		if p.InTrash {
			continue
		}
		parentID := resolveParentID(p, allIDs)
		if parentID != "" {
			childrenOf[parentID] = append(childrenOf[parentID], p.ID)
		}
	}

	// BFS from each resource root to collect all descendants
	includedSet := make(map[string]bool)
	queue := make([]string, 0, len(resourceIDs))
	for _, id := range resourceIDs {
		if _, ok := pageByID[id]; ok {
			includedSet[id] = true
			queue = append(queue, id)
		}
	}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, childID := range childrenOf[current] {
			if !includedSet[childID] {
				includedSet[childID] = true
				queue = append(queue, childID)
			}
		}
	}

	included = make([]notionPage, 0, len(includedSet))
	for id := range includedSet {
		included = append(included, pageByID[id])
	}
	excluded = make(map[string]bool, len(allPages)-len(includedSet))
	for id := range allIDs {
		if !includedSet[id] {
			excluded[id] = true
		}
	}
	return included, excluded
}

// --- File upload resolution ---

// resolveFileUploads walks the block tree and resolves file_upload type files
// by re-fetching the block to get a temporary download URL.
// This mutates blocks in place, replacing file_upload RawContent with resolved URLs.
func resolveFileUploads(ctx context.Context, client *notionClient, blocks []notionBlock) {
	for i := range blocks {
		if isFileBlock(blocks[i].Type) && blocks[i].RawContent != nil {
			var file notionFile
			json.Unmarshal(blocks[i].RawContent, &file)
			if file.GetFileUploadID() != "" {
				resolved, err := client.ResolveBlock(ctx, blocks[i].ID)
				if err != nil {
					logger.Warnf(ctx, "[Notion] failed to resolve file_upload in block %s: %v", blocks[i].ID, err)
					continue
				}
				blocks[i].RawContent = resolved.RawContent
			}
		}
		if len(blocks[i].Children) > 0 {
			resolveFileUploads(ctx, client, blocks[i].Children)
		}
	}
}

func isFileBlock(blockType string) bool {
	switch blockType {
	case "image", "file", "pdf", "video", "audio":
		return true
	}
	return false
}

// --- Property extraction ---

// propertyToString recursively extracts a string value from a Notion property.
// Follows the type chain generically, handling all 22 property types without hardcoding.
func propertyToString(value map[string]interface{}) string {
	if value == nil {
		return ""
	}

	typeName, _ := value["type"].(string)
	if typeName == "" {
		return extractLeafValue(value)
	}

	inner, exists := value[typeName]
	if !exists || inner == nil {
		return ""
	}

	return extractValue(inner)
}

func extractValue(v interface{}) string {
	switch val := v.(type) {
	case string:
		return val
	case float64:
		if val == float64(int64(val)) {
			return fmt.Sprintf("%d", int64(val))
		}
		return fmt.Sprintf("%g", val)
	case bool:
		return fmt.Sprintf("%t", val)
	case map[string]interface{}:
		return extractLeafValue(val)
	case []interface{}:
		var parts []string
		for _, item := range val {
			s := extractValue(item)
			if s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, ", ")
	case nil:
		return ""
	default:
		return fmt.Sprint(v)
	}
}

func extractLeafValue(m map[string]interface{}) string {
	if name, ok := m["name"].(string); ok {
		return name
	}
	if content, ok := m["content"].(string); ok {
		return content
	}
	if pt, ok := m["plain_text"].(string); ok {
		return pt
	}
	if start, ok := m["start"].(string); ok {
		if end, ok := m["end"].(string); ok && end != "" {
			return start + " ~ " + end
		}
		return start
	}
	if expr, ok := m["expression"].(string); ok {
		return expr
	}
	// Recurse if there's a type field
	if typeName, ok := m["type"].(string); ok {
		if inner, ok := m[typeName]; ok {
			return extractValue(inner)
		}
	}
	return ""
}

// extractPropertySchema extracts non-title property names from a record.
// Names are sorted alphabetically to ensure deterministic order
// (prevents false-positive content changes on incremental sync due to map iteration randomness).
func extractPropertySchema(record notionPage) (propNames []string) {
	if record.RawProperties == nil {
		return nil
	}
	var props map[string]json.RawMessage
	if err := json.Unmarshal(record.RawProperties, &props); err != nil {
		return nil
	}
	for name, propRaw := range props {
		var prop struct {
			Type string `json:"type"`
		}
		json.Unmarshal(propRaw, &prop)
		if prop.Type != "title" {
			propNames = append(propNames, name)
		}
	}
	sort.Strings(propNames)
	return
}

// resolveParentID determines where a page/data_source sits in the workspace hierarchy.
// For data_source objects, uses database_parent which points directly at the
// containing page; the regular `parent` field on a data_source points at its
// database container, not at the workspace location, so it's the wrong source.
func resolveParentID(p notionPage, allIDs map[string]bool) string {
	// Data sources: use database_parent which shows the workspace location
	// (e.g., which page contains this database), not the database container ID.
	if p.isDatabase() && p.DatabaseParent != nil {
		pid := p.DatabaseParent.GetParentID()
		if pid != "" && allIDs[pid] {
			return pid
		}
		return ""
	}
	// Pages: use direct parent
	if p.Parent.Type == parentTypeWorkspace {
		return ""
	}
	pid := p.Parent.GetParentID()
	if pid != "" && allIDs[pid] {
		return pid
	}
	return ""
}

// getDatabaseOrDataSourceInfo retrieves metadata for a database or data_source.
// Tries GET /v1/data_sources/{id} first (search returns data_source IDs),
// falls back to GET /v1/databases/{id} (child_database blocks use database IDs).
func getDatabaseOrDataSourceInfo(ctx context.Context, client *notionClient, id string) (*databaseInfo, error) {
	ds, err := client.GetDataSourceInfo(ctx, id)
	if err == nil {
		return &databaseInfo{Page: *ds, DataSourceID: id}, nil
	}
	return client.GetDatabaseInfo(ctx, id)
}

// computeExcludedSet returns IDs the user has explicitly deselected: visible in
// the picker but neither selected nor a descendant of a selected node. Used to
// seed `visited` so recursive child_page/child_database traversal skips them.
// Pages the user has never seen (e.g. added after the last config save) are
// not excluded, so a selected parent still picks them up automatically.
func computeExcludedSet(visibleIDs []string, parentOf map[string]string, selectedIDs []string) map[string]bool {
	selected := make(map[string]bool, len(selectedIDs))
	for _, id := range selectedIDs {
		selected[id] = true
	}
	excluded := make(map[string]bool, len(visibleIDs))
	for _, id := range visibleIDs {
		hasSelectedAncestor := false
		for cur := id; cur != ""; cur = parentOf[cur] {
			if selected[cur] {
				hasSelectedAncestor = true
				break
			}
		}
		if !hasSelectedAncestor {
			excluded[id] = true
		}
	}
	return excluded
}

// excludedSetFromListResources fetches the picker hierarchy via ListResources
// and computes the deselected set. Used by FetchAll where no other code path
// already has the page list in hand.
func (c *Connector) excludedSetFromListResources(ctx context.Context, config *types.DataSourceConfig, selectedIDs []string) map[string]bool {
	visible, err := c.ListResources(ctx, config)
	if err != nil {
		logger.Warnf(ctx, "[Notion] failed to list visible resources for exclusion: %v", err)
		return map[string]bool{}
	}
	ids := make([]string, 0, len(visible))
	parentOf := make(map[string]string, len(visible))
	for _, r := range visible {
		ids = append(ids, r.ExternalID)
		parentOf[r.ExternalID] = r.ParentID
	}
	return computeExcludedSet(ids, parentOf, selectedIDs)
}

// --- Helpers ---

func extractBaseURL(config *types.DataSourceConfig) string {
	if config.Settings != nil {
		if url, ok := config.Settings["base_url"].(string); ok && url != "" {
			return url
		}
	}
	return DefaultBaseURL
}

func mimeTypeForAttachment(attType string) string {
	switch attType {
	case "image":
		return "image/png"
	case "pdf":
		return "application/pdf"
	case "video":
		return "video/mp4"
	case "audio":
		return "audio/mpeg"
	default:
		return "application/octet-stream"
	}
}
