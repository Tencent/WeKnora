package im

import (
	"encoding/json"
	"fmt"
	"strings"
)

// IMToolStep tracks one tool invocation for IM display (mirrors Web agent stream events).
type IMToolStep struct {
	ToolCallID string
	ToolName   string
	Pending    bool
	Success    bool
	Arguments  map[string]any
	Data       map[string]interface{}
	Output     string
}

// imLocalizedToolName returns zh-CN labels aligned with frontend agentStream.tools.
func imLocalizedToolName(toolName string) string {
	if name, ok := imToolNameLabels[toolName]; ok {
		return name
	}
	if strings.HasPrefix(toolName, "mcp_") {
		return formatMCPToolName(toolName)
	}
	return toolName
}

var imToolNameLabels = map[string]string{
	"search_knowledge":        "知识库检索",
	"knowledge_search":        "知识库检索",
	"grep_chunks":             "搜索关键词",
	"web_search":              "网络搜索",
	"web_fetch":               "网页抓取",
	"get_document_info":       "获取文档信息",
	"list_knowledge_chunks":   "查看知识分块",
	"get_related_documents":   "查找相关文档",
	"get_document_content":    "获取文档内容",
	"wiki_search":             "Wiki 搜索",
	"wiki_read_page":          "Wiki 阅读",
	"wiki_read_source_doc":    "精读源文档",
	"todo_write":              "计划管理",
	"knowledge_graph_extract": "知识图谱抽取",
	"thinking":                "思考",
	"image_analysis":          "查看图片内容",
	"query_understand":        "理解问题",
	"query_knowledge_graph":   "知识图谱查询",
	"read_skill":              "读取技能",
	"execute_skill_script":    "执行技能脚本",
	"data_analysis":           "数据分析",
	"data_schema":             "数据结构",
	"database_query":          "数据库查询",
}

func formatMCPToolName(rawName string) string {
	rest := strings.TrimPrefix(rawName, "mcp_")
	if rest == "" {
		return rawName
	}
	parts := strings.Split(rest, "_")
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
}

func collectQueryStrings(value any) []string {
	if value == nil {
		return nil
	}
	switch v := value.(type) {
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return nil
		}
		if strings.HasPrefix(trimmed, "[") {
			var parsed []any
			if err := json.Unmarshal([]byte(trimmed), &parsed); err == nil {
				var out []string
				for _, item := range parsed {
					if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
						out = append(out, strings.TrimSpace(s))
					}
				}
				return out
			}
		}
		return []string{trimmed}
	case []any:
		var out []string
		for _, item := range v {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	case []string:
		var out []string
		for _, s := range v {
			if strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	default:
		return nil
	}
}

func imGetQueryText(args any) string {
	if args == nil {
		return ""
	}
	parsed := args
	if s, ok := args.(string); ok {
		var obj map[string]any
		if err := json.Unmarshal([]byte(s), &obj); err != nil {
			return ""
		}
		parsed = obj
	}
	record, ok := parsed.(map[string]any)
	if !ok {
		return ""
	}
	seen := make(map[string]struct{})
	var queries []string
	for _, q := range collectQueryStrings(record["query"]) {
		if _, dup := seen[q]; dup {
			continue
		}
		seen[q] = struct{}{}
		queries = append(queries, q)
	}
	for _, q := range collectQueryStrings(record["queries"]) {
		if _, dup := seen[q]; dup {
			continue
		}
		seen[q] = struct{}{}
		queries = append(queries, q)
	}
	return strings.Join(queries, "，")
}

func imGetWikiPageText(args any) string {
	if args == nil {
		return ""
	}
	parsed := args
	if s, ok := args.(string); ok {
		var obj map[string]any
		if err := json.Unmarshal([]byte(s), &obj); err != nil {
			return ""
		}
		parsed = obj
	}
	record, ok := parsed.(map[string]any)
	if !ok {
		return ""
	}
	seen := make(map[string]struct{})
	var slugs []string
	for _, slug := range collectQueryStrings(record["slug"]) {
		if _, dup := seen[slug]; dup {
			continue
		}
		seen[slug] = struct{}{}
		slugs = append(slugs, slug)
	}
	for _, slug := range collectQueryStrings(record["slugs"]) {
		if _, dup := seen[slug]; dup {
			continue
		}
		seen[slug] = struct{}{}
		slugs = append(slugs, slug)
	}
	return strings.Join(slugs, "、")
}

func imGetGrepPatterns(args any) []string {
	if args == nil {
		return nil
	}
	record, ok := args.(map[string]any)
	if !ok {
		return nil
	}
	if queries := collectQueryStrings(record["queries"]); len(queries) > 0 {
		return queries
	}
	if patterns := collectQueryStrings(record["patterns"]); len(patterns) > 0 {
		return patterns
	}
	if q := imGetQueryText(record); q != "" {
		return []string{q}
	}
	if pattern, ok := record["pattern"].(string); ok && strings.TrimSpace(pattern) != "" {
		return []string{strings.TrimSpace(pattern)}
	}
	return nil
}

func imGetWebSearchQuery(step IMToolStep) string {
	if q := imGetQueryText(step.Arguments); q != "" {
		return q
	}
	return imGetQueryText(step.Data)
}

func imGetGrepPatternsFromStep(step IMToolStep) []string {
	if patterns := imGetGrepPatterns(step.Arguments); len(patterns) > 0 {
		return patterns
	}
	return imGetGrepPatterns(step.Data)
}

func imAppendQueryTitle(base, query string) string {
	if query == "" {
		return base
	}
	return fmt.Sprintf("%s：「%s」", base, query)
}

func imAppendPatternsTitle(base string, patterns []string) string {
	if len(patterns) == 0 {
		return base
	}
	display := patterns
	more := ""
	if len(patterns) > 2 {
		display = patterns[:2]
		more = fmt.Sprintf(" +%d", len(patterns)-2)
	}
	return fmt.Sprintf("%s：「%s%s」", base, strings.Join(display, "、"), more)
}

// FormatIMToolLine formats one agent tool step (no emoji; aligned with Web getToolTitle).
func FormatIMToolLine(step IMToolStep) string {
	title := imAgentToolTitle(step)
	if title == "" {
		return ""
	}
	if step.Pending {
		return title
	}
	if summary := imToolResultSummary(step); summary != "" {
		return title + " · " + summary
	}
	return title
}

// FormatIMRagPipelineLine formats quick-QA RAG pipeline steps (Web RagPipelineProgress).
func FormatIMRagPipelineLine(step IMToolStep) string {
	toolName := step.ToolName
	query := imGetQueryText(step.Arguments)
	if query == "" {
		query = imGetQueryText(step.Data)
	}

	switch toolName {
	case "query_understand":
		if step.Pending {
			return "正在理解问题..."
		}
		return "已完成问题理解"
	case "knowledge_search", "search_knowledge":
		if step.Pending {
			if query != "" {
				return fmt.Sprintf("正在检索知识库：「%s」", query)
			}
			return "正在检索知识库..."
		}
		base := "检索知识库"
		if !step.Success {
			base = "检索知识库失败"
		}
		line := imAppendQueryTitle(base, query)
		if summary := imKnowledgeSearchSummary(step.Data); summary != "" {
			return line + " · " + summary
		}
		return line
	default:
		return ""
	}
}

func imAgentToolTitle(step IMToolStep) string {
	if step.Pending {
		switch step.ToolName {
		case "image_analysis":
			return "正在查看图片内容..."
		case "wiki_search", "wiki_read_page":
			return imLocalizedToolName(step.ToolName) + "..."
		default:
			return fmt.Sprintf("正在调用 %s...", imLocalizedToolName(step.ToolName))
		}
	}

	toolName := step.ToolName
	isSearchTool := toolName == "search_knowledge" || toolName == "knowledge_search" || toolName == "wiki_search"
	if isSearchTool {
		base := imToolStatusDescription(step)
		query := imGetQueryText(step.Arguments)
		if query == "" {
			query = imGetQueryText(step.Data)
		}
		return imAppendQueryTitle(base, query)
	}

	if toolName == "web_search" {
		base := imToolStatusDescription(step)
		return imAppendQueryTitle(base, imGetWebSearchQuery(step))
	}

	if toolName == "grep_chunks" {
		base := imToolStatusDescription(step)
		return imAppendPatternsTitle(base, imGetGrepPatternsFromStep(step))
	}

	if toolName == "wiki_read_page" {
		pageLabel := ""
		if step.Data != nil {
			if title, ok := step.Data["title"].(string); ok {
				pageLabel = strings.TrimSpace(title)
			}
		}
		if pageLabel == "" {
			pageLabel = imGetWikiPageText(step.Arguments)
		}
		if pageLabel == "" {
			pageLabel = imGetWikiPageText(step.Data)
		}
		base := imToolStatusDescription(step)
		return imAppendQueryTitle(base, pageLabel)
	}

	if summary := imToolHeaderSummary(step); summary != "" {
		return summary
	}
	return imToolStatusDescription(step)
}

func imToolStatusDescription(step IMToolStep) string {
	success := step.Success
	toolName := step.ToolName

	switch toolName {
	case "search_knowledge", "knowledge_search":
		if success {
			return "检索知识库"
		}
		return "检索知识库失败"
	case "wiki_search", "wiki_read_page":
		name := imLocalizedToolName(toolName)
		if success {
			return name
		}
		return fmt.Sprintf("调用 %s 失败", name)
	case "web_search":
		if success {
			return "网络搜索"
		}
		return "网络搜索失败"
	case "grep_chunks":
		if success {
			return "搜索关键词"
		}
		return "搜索关键词失败"
	case "get_document_info":
		if success {
			return "获取文档信息"
		}
		return "获取文档信息失败"
	case "get_document_content", "wiki_read_source_doc":
		if success {
			return "获取文档内容"
		}
		return "获取文档内容失败"
	case "thinking":
		if success {
			return "完成思考"
		}
		return "思考失败"
	case "todo_write":
		if success {
			return "更新任务列表"
		}
		return "更新任务列表失败"
	case "image_analysis":
		if success {
			return "已查看图片内容"
		}
		return "图片内容查看失败"
	case "query_understand":
		if success {
			return "已完成问题理解"
		}
		return fmt.Sprintf("调用 %s 失败", imLocalizedToolName(toolName))
	default:
		name := imLocalizedToolName(toolName)
		if success {
			return fmt.Sprintf("调用 %s", name)
		}
		return fmt.Sprintf("调用 %s 失败", name)
	}
}

func imToolHeaderSummary(step IMToolStep) string {
	if step.Pending || !step.Success {
		return ""
	}
	toolName := step.ToolName
	data := step.Data

	switch toolName {
	case "search_knowledge", "knowledge_search":
		return ""
	case "get_document_info":
		if data != nil {
			if title, ok := data["title"].(string); ok && strings.TrimSpace(title) != "" {
				return fmt.Sprintf("获取文档：%s", strings.TrimSpace(title))
			}
		}
	case "list_knowledge_chunks":
		if data != nil {
			if question, ok := data["faq_question"].(string); ok && strings.TrimSpace(question) != "" {
				return fmt.Sprintf("查看 FAQ：%s", strings.TrimSpace(question))
			}
			if _, ok := data["fetched_chunks"]; ok {
				title := "文档"
				if t, ok := data["knowledge_title"].(string); ok && strings.TrimSpace(t) != "" {
					title = strings.TrimSpace(t)
				} else if id, ok := data["knowledge_id"].(string); ok && strings.TrimSpace(id) != "" {
					title = strings.TrimSpace(id)
				}
				return fmt.Sprintf("查看 %s", title)
			}
		}
	}
	return ""
}

func imToolResultSummary(step IMToolStep) string {
	if step.Pending || !step.Success {
		return ""
	}
	switch step.ToolName {
	case "search_knowledge", "knowledge_search":
		return imKnowledgeSearchSummary(step.Data)
	case "web_search":
		return imWebSearchSummary(step.Data)
	case "grep_chunks":
		return imGrepSearchSummary(step.Data)
	case "list_knowledge_chunks":
		return imKnowledgeChunksSummary(step.Data)
	default:
		return briefToolSummary(step.Output)
	}
}

func imKnowledgeSearchSummary(data map[string]interface{}) string {
	if data == nil {
		return ""
	}
	count := imResultCount(data)
	if count == 0 {
		return "未找到匹配的内容"
	}
	if kbCounts, ok := data["kb_counts"].(map[string]interface{}); ok && len(kbCounts) > 0 {
		return fmt.Sprintf("找到 %d 个结果，来自 %d 个文件", count, len(kbCounts))
	}
	return fmt.Sprintf("找到 %d 个结果", count)
}

func imWebSearchSummary(data map[string]interface{}) string {
	if data == nil {
		return ""
	}
	count := imResultCount(data)
	if count == 0 {
		return ""
	}
	return fmt.Sprintf("找到 %d 个网络搜索结果", count)
}

func imGrepSearchSummary(data map[string]interface{}) string {
	if data == nil {
		return ""
	}
	totalChunks := 0
	if v, ok := data["total_matches"].(float64); ok {
		totalChunks = int(v)
	} else if v, ok := data["total_matches"].(int); ok {
		totalChunks = v
	}
	if totalChunks == 0 {
		return "未找到匹配的内容"
	}
	docCount := imGrepDocumentCount(data)
	return fmt.Sprintf("找到 %d 个匹配片段，来自 %d 个文档", totalChunks, docCount)
}

func imGrepDocumentCount(data map[string]interface{}) int {
	if v, ok := data["document_count"].(float64); ok && v >= 0 {
		return int(v)
	}
	if v, ok := data["document_count"].(int); ok && v >= 0 {
		return v
	}
	if results, ok := data["knowledge_results"].([]interface{}); ok && len(results) > 0 {
		return len(results)
	}
	if results, ok := data["chunk_results"].([]interface{}); ok && len(results) > 0 {
		return len(results)
	}
	return 0
}

func imKnowledgeChunksSummary(data map[string]interface{}) string {
	if data == nil {
		return ""
	}
	fetched, ok := data["fetched_chunks"]
	if !ok {
		return ""
	}
	fetchedN := imNumericValue(fetched)
	totalN := imNumericValue(data["total_chunks"])
	summary := fmt.Sprintf("已加载 %d / %v 个分块", fetchedN, formatIMOptionalInt(totalN, data["total_chunks"]))
	pageSize := imNumericValue(data["page_size"])
	if totalN > pageSize && pageSize > 0 {
		page := imNumericValue(data["page"])
		if page <= 0 {
			page = 1
		}
		summary += fmt.Sprintf(" · 第 %d 页，每页 %d 个", page, pageSize)
	}
	return summary
}

func imResultCount(data map[string]interface{}) int {
	if results, ok := data["results"].([]interface{}); ok && len(results) > 0 {
		return len(results)
	}
	if count, ok := data["count"].(float64); ok && count > 0 {
		return int(count)
	}
	if count, ok := data["count"].(int); ok && count > 0 {
		return count
	}
	return 0
}

func imNumericValue(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		return 0
	}
}

func formatIMOptionalInt(n int, raw any) string {
	if raw == nil {
		return "?"
	}
	if _, ok := raw.(string); ok && n == 0 {
		return "?"
	}
	if n == 0 {
		return "?"
	}
	return fmt.Sprintf("%d", n)
}

func renderIMToolSteps(steps []IMToolStep, format func(IMToolStep) string) string {
	if len(steps) == 0 {
		return ""
	}
	var b strings.Builder
	for _, step := range steps {
		line := format(step)
		if line == "" {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

func mergeIMNarrativeAndTools(narrative string, toolLines string) string {
	narrative = strings.TrimSpace(narrative)
	toolLines = strings.TrimSpace(toolLines)
	switch {
	case narrative != "" && toolLines != "":
		return narrative + "\n" + toolLines
	case toolLines != "":
		return toolLines
	default:
		return narrative
	}
}

func upsertIMToolStep(steps *[]IMToolStep, index map[string]int, id string, update func(*IMToolStep)) {
	if i, ok := index[id]; ok {
		update(&(*steps)[i])
		return
	}
	step := IMToolStep{ToolCallID: id}
	update(&step)
	index[id] = len(*steps)
	*steps = append(*steps, step)
}
