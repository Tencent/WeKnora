package dingtalk

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// Compile-time proof that *Connector satisfies the datasource.Connector interface.
var _ datasource.Connector = (*Connector)(nil)

type clientFactory func(*Config) *client

// Connector implements datasource.Connector for DingTalk.
type Connector struct {
	newClient        clientFactory
	perDocumentDelay time.Duration
}

type resourceTarget struct {
	raw         string
	workspaceID string
	nodeID      string
	kind        string
}

const (
	dingtalkResourceSeparator = ":"
	dingtalkResourceSpace     = "wiki_space"
	dingtalkResourceFolder    = "folder"
	dingtalkResourceDocument  = "document"
)

// NewConnector creates a new DingTalk connector.
func NewConnector() *Connector {
	return &Connector{
		newClient:        newClient,
		perDocumentDelay: 300 * time.Millisecond,
	}
}

func (c *Connector) clientFor(cfg *Config) *client {
	if c != nil && c.newClient != nil {
		return c.newClient(cfg)
	}
	return newClient(cfg)
}

func parseResourceTarget(resourceID string) resourceTarget {
	parts := strings.Split(resourceID, dingtalkResourceSeparator)
	target := resourceTarget{raw: resourceID}
	if len(parts) > 0 {
		target.workspaceID = parts[0]
	}
	if len(parts) > 1 {
		target.nodeID = parts[1]
	}
	if len(parts) > 2 {
		target.kind = parts[2]
	}
	return target
}

func makeNodeResourceID(workspaceID string, node WikiNode) string {
	return strings.Join([]string{workspaceID, node.NodeID, nodeResourceType(node)}, dingtalkResourceSeparator)
}

func nodeResourceType(node WikiNode) string {
	if strings.EqualFold(node.NodeType, "FOLDER") {
		return dingtalkResourceFolder
	}
	if isDocumentNode(node) {
		return dingtalkResourceDocument
	}
	return strings.ToLower(firstNonEmpty(node.Category, node.NodeType, "file"))
}

func isDocumentNode(node WikiNode) bool {
	// DingTalk reports uploaded attachments such as PDFs as category=DOCUMENT.
	// Those nodes are not accepted by the document blocks API. Native DingTalk
	// documents are identified by category=ALIDOC or the compatible adoc
	// extension exposed by the Wiki node API.
	extension := strings.TrimPrefix(strings.TrimSpace(node.Extension), ".")
	return strings.EqualFold(node.NodeType, "FILE") &&
		(strings.EqualFold(node.Category, "ALIDOC") || strings.EqualFold(extension, "adoc"))
}

// Type returns the connector type identifier.
func (c *Connector) Type() string { return types.ConnectorTypeDingTalk }

// Validate verifies the given credentials by pinging the workspaces endpoint.
func (c *Connector) Validate(ctx context.Context, config *types.DataSourceConfig) error {
	cfg, err := parseDingTalkConfig(config)
	if err != nil {
		return err
	}
	cli := c.clientFor(cfg)
	if err := cli.Ping(ctx, cfg.OperatorID); err != nil {
		return fmt.Errorf("dingtalk connection failed: %w", err)
	}
	return nil
}

// ResolveResourceAncestors returns the lazy-loaded parent resources needed to
// reveal previously selected DingTalk nodes when editing a data source.
func (c *Connector) ResolveResourceAncestors(
	ctx context.Context, config *types.DataSourceConfig, resourceIDs []string,
) ([]string, error) {
	cfg, err := parseDingTalkConfig(config)
	if err != nil {
		return nil, err
	}
	cli := c.clientFor(cfg)
	operatorID := cfg.OperatorID

	workspaces, err := cli.ListWorkspaces(ctx, operatorID)
	if err != nil {
		return nil, fmt.Errorf("list workspaces: %w", err)
	}
	rootByWorkspace := make(map[string]string, len(workspaces))
	for _, w := range workspaces {
		rootByWorkspace[w.WorkspaceID] = w.RootNodeID
	}

	needed := make(map[string]bool)
	targetsByWorkspace := make(map[string]map[string]bool)
	for _, resourceID := range resourceIDs {
		target := parseResourceTarget(resourceID)
		if target.workspaceID == "" || target.nodeID == "" {
			continue
		}
		needed[target.workspaceID] = true
		if targetsByWorkspace[target.workspaceID] == nil {
			targetsByWorkspace[target.workspaceID] = make(map[string]bool)
		}
		targetsByWorkspace[target.workspaceID][target.nodeID] = true
	}

	for workspaceID, targetNodes := range targetsByWorkspace {
		rootNodeID := rootByWorkspace[workspaceID]
		if rootNodeID == "" {
			continue
		}
		if err := c.collectAncestorPaths(ctx, cli, workspaceID, rootNodeID, operatorID, targetNodes, []string{workspaceID}, needed); err != nil {
			return nil, err
		}
	}

	out := make([]string, 0, len(needed))
	for id := range needed {
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}

// ListResources returns DingTalk workspaces at the root and lazily lists child
// folders/documents when the resource picker expands a workspace or folder.
func (c *Connector) ListResources(
	ctx context.Context, config *types.DataSourceConfig, parentID string,
) ([]types.Resource, error) {
	cfg, err := parseDingTalkConfig(config)
	if err != nil {
		return nil, err
	}
	cli := c.clientFor(cfg)

	// Get workspaces - use operatorID from config if provided.
	operatorID := cfg.OperatorID
	workspaces, err := cli.ListWorkspaces(ctx, operatorID)
	if err != nil {
		return nil, fmt.Errorf("list workspaces: %w", err)
	}

	if parentID != "" {
		parent := parseResourceTarget(parentID)
		if parent.workspaceID == "" {
			return []types.Resource{}, nil
		}
		parentNodeID := parent.nodeID
		if parentNodeID == "" {
			for _, w := range workspaces {
				if w.WorkspaceID == parent.workspaceID {
					parentNodeID = w.RootNodeID
					break
				}
			}
		}
		if parentNodeID == "" {
			return []types.Resource{}, nil
		}
		nodes, err := listDirectNodes(ctx, cli, parentNodeID, operatorID)
		if err != nil {
			return nil, fmt.Errorf("list nodes for parent %s: %w", parentID, err)
		}
		out := make([]types.Resource, 0, len(nodes))
		for _, node := range nodes {
			if !node.hasChildNodes() && !isDocumentNode(node) {
				continue
			}
			out = append(out, nodeResource(parent.workspaceID, parentID, node))
		}
		sort.Slice(out, func(i, j int) bool { return out[i].ExternalID < out[j].ExternalID })
		return out, nil
	}

	out := make([]types.Resource, 0, len(workspaces))
	for _, w := range workspaces {
		out = append(out, types.Resource{
			ExternalID:  w.WorkspaceID,
			Name:        w.Name,
			Type:        dingtalkResourceSpace,
			URL:         w.URL,
			Description: w.Description,
			ModifiedAt:  parseTime(w.ModifiedTime),
			HasChildren: w.RootNodeID != "",
			Metadata: map[string]interface{}{
				"workspace_type": w.Type,
				"root_node_id":   w.RootNodeID,
				"corp_id":        w.CorpID,
			},
		})
	}

	// Stable, deterministic order for UI rendering
	sort.Slice(out, func(i, j int) bool { return out[i].ExternalID < out[j].ExternalID })
	return out, nil
}

func listDirectNodes(ctx context.Context, cli *client, parentNodeID, operatorID string) ([]WikiNode, error) {
	var out []WikiNode
	nextToken := ""
	for {
		nodes, token, err := cli.listNodePage(ctx, parentNodeID, operatorID, nextToken)
		if err != nil {
			return nil, err
		}
		out = append(out, nodes...)
		if token == "" {
			break
		}
		nextToken = token
	}
	return out, nil
}

func nodeResource(workspaceID, parentID string, node WikiNode) types.Resource {
	return types.Resource{
		ExternalID:  makeNodeResourceID(workspaceID, node),
		Name:        node.Name,
		Type:        nodeResourceType(node),
		URL:         node.URL,
		ModifiedAt:  node.modifiedAt(),
		ParentID:    parentID,
		HasChildren: node.hasChildNodes(),
		Metadata: map[string]interface{}{
			"workspace_id": workspaceID,
			"node_id":      node.NodeID,
			"doc_key":      node.DocKey,
			"node_type":    node.NodeType,
			"category":     node.Category,
			"word_count":   node.wordCount(),
		},
	}
}

func (c *Connector) collectAncestorPaths(
	ctx context.Context,
	cli *client,
	workspaceID, parentNodeID, operatorID string,
	targetNodes map[string]bool,
	path []string,
	needed map[string]bool,
) error {
	nextToken := ""
	for {
		nodes, token, err := cli.listNodePage(ctx, parentNodeID, operatorID, nextToken)
		if err != nil {
			return fmt.Errorf("list ancestor candidates for workspace %s: %w", workspaceID, err)
		}
		for _, node := range nodes {
			if targetNodes[node.NodeID] {
				for _, ancestorID := range path {
					needed[ancestorID] = true
				}
			}
			if !node.hasChildNodes() {
				continue
			}
			nodeID := makeNodeResourceID(workspaceID, node)
			if err := c.collectAncestorPaths(ctx, cli, workspaceID, node.NodeID, operatorID, targetNodes, append(path, nodeID), needed); err != nil {
				return err
			}
		}
		if token == "" {
			break
		}
		nextToken = token
	}
	return nil
}

// FetchAll performs a full sync of all workspaces specified in resourceIDs.
func (c *Connector) FetchAll(ctx context.Context, config *types.DataSourceConfig, resourceIDs []string) ([]types.FetchedItem, error) {
	items, _, err := c.walk(ctx, config, resourceIDs, nil, false)
	return items, err
}

// walk is the shared implementation for FetchAll / FetchIncremental.
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
	cli := c.clientFor(cfg)
	operatorID := cfg.OperatorID

	newCursor := &dingtalkCursor{
		LastSyncTime:   time.Now(),
		WorkspaceTimes: make(map[string]map[string]time.Time),
	}
	var out []types.FetchedItem

	workspaces, err := cli.ListWorkspaces(ctx, operatorID)
	if err != nil {
		return nil, nil, fmt.Errorf("list workspaces: %w", err)
	}
	rootByWorkspace := make(map[string]string, len(workspaces))
	for _, w := range workspaces {
		rootByWorkspace[w.WorkspaceID] = w.RootNodeID
	}

	for _, resourceID := range resourceIDs {
		target := parseResourceTarget(resourceID)
		workspaceID := target.workspaceID
		if workspaceID == "" {
			continue
		}
		newCursor.WorkspaceTimes[resourceID] = make(map[string]time.Time)

		rootNodeID := rootByWorkspace[workspaceID]
		if rootNodeID == "" {
			logger.Warnf(ctx, "[DingTalk] workspace %s not found or has no root node", workspaceID)
			continue
		}

		allNodes, err := c.nodesForTarget(ctx, cli, rootNodeID, operatorID, target)
		if err != nil {
			return nil, nil, fmt.Errorf("list nodes for resource %s: %w", resourceID, err)
		}

		var skippedFolder, skippedNonDoc, kept int
		var sampleSkip string
		for _, node := range allNodes {
			// Skip folders - they are listed but not synced as content
			if strings.EqualFold(node.NodeType, "FOLDER") {
				skippedFolder++
				continue
			}

			// Only native DingTalk documents are accepted by the blocks API.
			// Generic uploaded files (including category=DOCUMENT PDFs) are
			// intentionally skipped.
			if !isDocumentNode(node) {
				skippedNonDoc++
				if sampleSkip == "" {
					sampleSkip = fmt.Sprintf("nodeId=%s type=%s category=%s name=%q", node.NodeID, node.NodeType, node.Category, node.Name)
				}
				continue
			}

			currentModTime := node.modifiedAt()

			// Incremental: skip if content hasn't changed
			if incremental && prev != nil && prev.WorkspaceTimes != nil {
				if prevTimes, ok := prev.WorkspaceTimes[resourceID]; ok {
					if prevModTime, ok := prevTimes[node.NodeID]; ok {
						newCursor.WorkspaceTimes[resourceID][node.NodeID] = currentModTime
						if !currentModTime.After(prevModTime) {
							continue
						}
					}
				}
			}

			kept++

			if c.perDocumentDelay > 0 {
				if err := sleepCtx(ctx, c.perDocumentDelay); err != nil {
					return nil, nil, err
				}
			}

			blocks, err := cli.GetDocumentBlocks(ctx, node.contentKey(), operatorID)
			if err != nil {
				if incremental && prev != nil && prev.WorkspaceTimes != nil {
					if prevTimes, ok := prev.WorkspaceTimes[resourceID]; ok {
						if prevModTime, ok := prevTimes[node.NodeID]; ok {
							newCursor.WorkspaceTimes[resourceID][node.NodeID] = prevModTime
						}
					}
				}
				out = append(out, types.FetchedItem{
					ExternalID:       node.NodeID,
					Title:            node.Name,
					SourceResourceID: resourceID,
					Metadata: map[string]string{
						"error":         err.Error(),
						"channel":       types.ChannelDingtalk,
						"node_id":       node.NodeID,
						"workspace_id":  workspaceID,
						"node_type":     node.NodeType,
						"category":      node.Category,
						"failure_stage": "fetch_content",
					},
				})
				continue
			}
			newCursor.WorkspaceTimes[resourceID][node.NodeID] = currentModTime

			content := renderBlocksMarkdown(node.Name, blocks)
			if strings.TrimSpace(content) == "" {
				content = "# " + node.Name + "\n"
			}

			out = append(out, types.FetchedItem{
				ExternalID:       node.NodeID,
				Title:            node.Name,
				Content:          []byte(content),
				ContentType:      "text/markdown",
				FileName:         sanitizeFileName(node.Name) + ".md",
				URL:              node.URL,
				UpdatedAt:        currentModTime,
				SourceResourceID: resourceID,
				Metadata: map[string]string{
					"node_id":      node.NodeID,
					"workspace_id": workspaceID,
					"node_type":    node.NodeType,
					"category":     node.Category,
					"word_count":   fmt.Sprintf("%d", node.wordCount()),
					"channel":      types.ChannelDingtalk,
				},
			})
		}

		logger.Infof(ctx, "[DingTalk] resource %s: total=%d kept=%d skipped_folder=%d skipped_non_doc=%d sample_skip={%s}",
			resourceID, len(allNodes), kept, skippedFolder, skippedNonDoc, sampleSkip)

		// Deletion detection (incremental only)
		if incremental && prev != nil && prev.WorkspaceTimes != nil {
			if prevTimes, ok := prev.WorkspaceTimes[resourceID]; ok {
				currentNodes := make(map[string]bool, len(newCursor.WorkspaceTimes[resourceID]))
				for nodeID := range newCursor.WorkspaceTimes[resourceID] {
					currentNodes[nodeID] = true
				}
				for prevNodeID := range prevTimes {
					if !currentNodes[prevNodeID] {
						out = append(out, types.FetchedItem{
							ExternalID:       prevNodeID,
							IsDeleted:        true,
							SourceResourceID: resourceID,
						})
					}
				}
			}
		}
	}

	if !incremental {
		return out, nil, nil
	}
	return out, newCursor, nil
}

func (c *Connector) nodesForTarget(ctx context.Context, cli *client, rootNodeID, operatorID string, target resourceTarget) ([]WikiNode, error) {
	if target.nodeID == "" {
		return cli.ListAllNodes(ctx, rootNodeID, operatorID)
	}
	if target.kind == dingtalkResourceDocument {
		allNodes, err := cli.ListAllNodes(ctx, rootNodeID, operatorID)
		if err != nil {
			return nil, err
		}
		for _, node := range allNodes {
			if node.NodeID == target.nodeID {
				return []WikiNode{node}, nil
			}
		}
		return []WikiNode{{NodeID: target.nodeID, Name: target.nodeID, NodeType: "FILE", Category: "ALIDOC"}}, nil
	}
	return cli.ListAllNodes(ctx, target.nodeID, operatorID)
}

func renderBlocksMarkdown(title string, blocks []docBlock) string {
	var b strings.Builder
	if strings.TrimSpace(title) != "" {
		b.WriteString("# ")
		b.WriteString(title)
		b.WriteString("\n\n")
	}
	for _, block := range blocks {
		writeBlockMarkdown(&b, block)
	}
	return strings.TrimSpace(b.String()) + "\n"
}

func writeBlockMarkdown(b *strings.Builder, block docBlock) {
	raw := blockRawObject(block)
	blockType := normalizeBlockType(firstNonEmpty(block.BlockType, block.Type, stringValue(raw["blockType"]), stringValue(raw["type"])))
	if blockType == "table" {
		if table := tableMarkdown(raw); table != "" {
			b.WriteString(table)
			b.WriteString("\n\n")
		}
		for _, child := range block.Children {
			writeBlockMarkdown(b, child)
		}
		return
	}
	text := strings.TrimSpace(blockText(block))
	if text == "" {
		text = strings.TrimSpace(inlineText(raw))
	}
	if text != "" {
		switch blockType {
		case "heading", "heading1", "h1", "title":
			level := headingLevel(raw)
			if level <= 1 {
				b.WriteString("# ")
			} else if level == 2 {
				b.WriteString("## ")
			} else {
				b.WriteString(strings.Repeat("#", min(level, 6)))
				b.WriteString(" ")
			}
		case "heading2", "h2":
			b.WriteString("## ")
		case "heading3", "h3":
			b.WriteString("### ")
		case "bullet", "unorderedlist", "list":
			b.WriteString("- ")
		case "orderedlist":
			b.WriteString("1. ")
		case "blockquote", "callout":
			writeQuotedMarkdown(b, text)
			for _, child := range block.Children {
				writeBlockMarkdown(b, child)
			}
			return
		case "image":
			if image := imageMarkdown(raw); image != "" {
				b.WriteString(image)
				b.WriteString("\n\n")
				for _, child := range block.Children {
					writeBlockMarkdown(b, child)
				}
				return
			}
		}
		b.WriteString(text)
		b.WriteString("\n\n")
	} else if blockType == "image" {
		if image := imageMarkdown(raw); image != "" {
			b.WriteString(image)
			b.WriteString("\n\n")
		}
	}
	for _, child := range block.Children {
		writeBlockMarkdown(b, child)
	}
}

func writeQuotedMarkdown(b *strings.Builder, text string) {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		b.WriteString("> ")
		b.WriteString(line)
		b.WriteString("\n")
	}
	b.WriteString("\n")
}

func blockText(block docBlock) string {
	if block.Text != "" {
		return block.Text
	}
	if block.Content != "" {
		return block.Content
	}
	if len(block.Raw) == 0 {
		return ""
	}
	return inlineText(blockRawObject(block))
}

func blockRawObject(block docBlock) map[string]interface{} {
	if len(block.Raw) == 0 {
		return nil
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(block.Raw, &raw); err != nil {
		return nil
	}
	return raw
}

func normalizeBlockType(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "_", "")
	value = strings.ReplaceAll(value, "-", "")
	return value
}

func headingLevel(raw map[string]interface{}) int {
	if heading, ok := raw["heading"].(map[string]interface{}); ok {
		if level := intValue(heading["level"]); level > 0 {
			return level
		}
	}
	if level := intValue(raw["level"]); level > 0 {
		return level
	}
	return 1
}

func inlineText(value interface{}) string {
	return strings.TrimSpace(strings.Join(extractInlineParts(value, ""), ""))
}

func extractInlineParts(value interface{}, parentKey string) []string {
	switch v := value.(type) {
	case map[string]interface{}:
		if link := linkMarkdown(v); link != "" {
			return []string{link}
		}
		if normalizeBlockType(stringValue(v["blockType"])) == "image" {
			if image := imageMarkdown(v); image != "" {
				return []string{image}
			}
		}
		if s := directText(v); s != "" {
			return []string{s}
		}
		var out []string
		keys := make([]string, 0, len(v))
		for key := range v {
			if !isNoiseTextKey(key) {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		for _, key := range keys {
			out = append(out, extractInlineParts(v[key], key)...)
		}
		return out
	case []interface{}:
		var out []string
		for _, child := range v {
			out = append(out, extractInlineParts(child, parentKey)...)
		}
		return out
	case string:
		if isContentTextKey(parentKey) {
			if s := strings.TrimSpace(v); s != "" {
				return []string{s}
			}
		}
	}
	return nil
}

func directText(v map[string]interface{}) string {
	for _, key := range []string{"text", "content", "plainText", "plain_text", "value"} {
		switch child := v[key].(type) {
		case string:
			return child
		case map[string]interface{}:
			if s := directText(child); s != "" {
				return s
			}
		}
	}
	return ""
}

func linkMarkdown(v map[string]interface{}) string {
	href := firstNonEmpty(
		stringValue(v["href"]),
		stringValue(v["url"]),
		stringValue(nestedValue(v, "properties", "href")),
	)
	if href == "" {
		if link, ok := v["link"].(map[string]interface{}); ok {
			return linkMarkdown(link)
		}
		return ""
	}
	label := firstNonEmpty(
		stringValue(v["label"]),
		stringValue(v["title"]),
		directText(v),
		inlineText(v["children"]),
		inlineText(v["elements"]),
		href,
	)
	return fmt.Sprintf("[%s](%s)", escapeMarkdownLinkText(label), href)
}

func imageMarkdown(v map[string]interface{}) string {
	imageNode := v
	if nested, ok := v["image"].(map[string]interface{}); ok {
		imageNode = nested
	}
	src := firstNonEmpty(
		stringValue(imageNode["src"]),
		stringValue(imageNode["url"]),
		stringValue(imageNode["downloadUrl"]),
		stringValue(nestedValue(imageNode, "properties", "src")),
	)
	if src == "" {
		return ""
	}
	alt := firstNonEmpty(
		stringValue(imageNode["alt"]),
		stringValue(imageNode["title"]),
		stringValue(imageNode["name"]),
		"image",
	)
	return fmt.Sprintf("![%s](%s)", escapeMarkdownLinkText(alt), src)
}

func tableMarkdown(raw map[string]interface{}) string {
	tableNode := raw
	if nested, ok := raw["table"].(map[string]interface{}); ok {
		tableNode = nested
	}
	rows := tableRows(tableNode["cells"])
	if len(rows) == 0 {
		rows = tableRows(tableNode["rows"])
	}
	if len(rows) == 0 {
		return ""
	}
	cols := 0
	for _, row := range rows {
		if len(row) > cols {
			cols = len(row)
		}
	}
	if cols == 0 {
		return ""
	}
	var b strings.Builder
	writeTableRow(&b, rows[0], cols)
	separator := make([]string, cols)
	for i := range separator {
		separator[i] = "---"
	}
	writeTableRow(&b, separator, cols)
	for _, row := range rows[1:] {
		writeTableRow(&b, row, cols)
	}
	return strings.TrimRight(b.String(), "\n")
}

func tableRows(value interface{}) [][]string {
	rawRows, ok := value.([]interface{})
	if !ok {
		return nil
	}
	rows := make([][]string, 0, len(rawRows))
	for _, rawRow := range rawRows {
		cells, ok := rawRow.([]interface{})
		if !ok {
			continue
		}
		row := make([]string, 0, len(cells))
		for _, cell := range cells {
			row = append(row, sanitizeTableCell(tableCellText(cell)))
		}
		rows = append(rows, row)
	}
	return rows
}

func tableCellText(value interface{}) string {
	if s, ok := value.(string); ok {
		return s
	}
	return inlineText(value)
}

func writeTableRow(b *strings.Builder, row []string, cols int) {
	b.WriteString("|")
	for i := 0; i < cols; i++ {
		cell := ""
		if i < len(row) {
			cell = row[i]
		}
		b.WriteString(" ")
		b.WriteString(cell)
		b.WriteString(" |")
	}
	b.WriteString("\n")
}

func sanitizeTableCell(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "|", "\\|")
	return strings.TrimSpace(s)
}

func escapeMarkdownLinkText(s string) string {
	s = strings.ReplaceAll(s, "[", "\\[")
	s = strings.ReplaceAll(s, "]", "\\]")
	return strings.TrimSpace(s)
}

func nestedValue(v map[string]interface{}, keys ...string) interface{} {
	var cur interface{} = v
	for _, key := range keys {
		m, ok := cur.(map[string]interface{})
		if !ok {
			return nil
		}
		cur = m[key]
	}
	return cur
}

func stringValue(v interface{}) string {
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

func intValue(v interface{}) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	case string:
		value := strings.ToLower(strings.TrimSpace(n))
		value = strings.TrimPrefix(value, "heading-")
		value = strings.TrimPrefix(value, "heading_")
		value = strings.TrimPrefix(value, "h")
		i, _ := strconv.Atoi(value)
		return i
	default:
		return 0
	}
}

func isContentTextKey(key string) bool {
	switch strings.ToLower(key) {
	case "text", "content", "plaintext", "plain_text", "value", "cells":
		return true
	default:
		return false
	}
}

func isNoiseTextKey(key string) bool {
	switch strings.ToLower(key) {
	case "blockid", "block_id", "blocktype", "block_type", "type", "category", "extension", "url", "href", "src":
		return true
	default:
		return false
	}
}

func extractTextFields(value interface{}) []string {
	return extractInlineParts(value, "")
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// FetchIncremental returns items changed (or deleted) since the prior cursor.
func (c *Connector) FetchIncremental(
	ctx context.Context,
	config *types.DataSourceConfig,
	cursor *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	resourceIDs := config.ResourceIDs
	if len(resourceIDs) == 0 {
		return nil, nil, fmt.Errorf("no resource IDs (workspace IDs) configured")
	}

	// Decode prior cursor (if any)
	var prev *dingtalkCursor
	if cursor != nil && cursor.ConnectorCursor != nil {
		var p dingtalkCursor
		b, _ := json.Marshal(cursor.ConnectorCursor)
		_ = json.Unmarshal(b, &p)
		prev = &p
	}

	items, newCursor, err := c.walk(ctx, config, resourceIDs, prev, true)
	if err != nil {
		return nil, nil, err
	}

	// Marshal newCursor into a generic map for the SyncCursor wrapper
	cursorMap := make(map[string]interface{})
	b, _ := json.Marshal(newCursor)
	_ = json.Unmarshal(b, &cursorMap)

	return items, &types.SyncCursor{
		LastSyncTime:    newCursor.LastSyncTime,
		ConnectorCursor: cursorMap,
	}, nil
}
