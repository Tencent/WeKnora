package modelcontext

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// ModelOutput returns a compact, source-centric representation for the LLM.
// The canonical ToolResult.Output remains untouched for UI, logs and storage.
func (r *sourceRegistry) ModelOutput(result *types.ToolResult) string {
	if result == nil {
		return ""
	}
	if !result.Success {
		if result.Error != "" {
			return "Error: " + r.CompactKnownText(result.Error)
		}
		return "Error: tool call failed"
	}
	displayType := stringValue(result.Data, "display_type")
	switch displayType {
	case "grep_results":
		return r.modelKnowledgeOutput("keyword", mapsValue(result.Data["chunk_results"]), result.Output)
	case "search_results":
		return r.modelKnowledgeOutput("semantic", mapsValue(result.Data["results"]), result.Output)
	case "knowledge_chunks_list":
		return r.modelKnowledgeChunksOutput(result.Data, result.Output)
	case "document_info":
		return r.modelDocumentInfoOutput(mapsValue(result.Data["documents"]), result.Output)
	case "graph_query_results":
		return r.modelKnowledgeOutput("graph", mapsValue(result.Data["results"]), result.Output)
	case "web_search_results":
		return r.modelWebSearchOutput(mapsValue(result.Data["results"]), result.Output)
	case "web_fetch_results":
		return r.modelWebFetchOutput(mapsValue(result.Data["results"]), result.Output)
	case "database_query":
		return r.modelDatabaseQueryOutput(mapsValue(result.Data["rows"]), result.Output)
	default:
		r.registerLabeledReferences(result.Output)
		r.registerStructuredReferences(result.Output)
		return r.CompactKnownText(result.Output)
	}
}

// registerStructuredReferences registers durable IDs found under explicitly
// labeled keys of a JSON tool result, using the same key dispatch as tool
// arguments. Non-JSON output is a no-op.
func (r *sourceRegistry) registerStructuredReferences(raw string) {
	var value interface{}
	if json.Unmarshal([]byte(raw), &value) != nil {
		return
	}
	var walk func(string, interface{})
	walk = func(key string, value interface{}) {
		switch typed := value.(type) {
		case string:
			r.registerSourceIDByKey(key, typed)
		case []interface{}:
			for _, item := range typed {
				walk(key, item)
			}
		case map[string]interface{}:
			for childKey, item := range typed {
				walk(childKey, item)
			}
		}
	}
	walk("", value)
}

func (r *sourceRegistry) modelDatabaseQueryOutput(rows []map[string]interface{}, fallback string) string {
	for _, row := range rows {
		for key, raw := range row {
			if value, ok := raw.(string); ok {
				r.registerSourceIDByKey(key, value)
			}
		}
	}
	return r.CompactKnownText(fallback)
}

func (r *sourceRegistry) modelDocumentInfoOutput(rows []map[string]interface{}, fallback string) string {
	if len(rows) == 0 {
		return r.CompactKnownText(fallback)
	}
	var b strings.Builder
	b.WriteString("<documents>\n")
	count := 0
	for _, row := range rows {
		knowledgeID := stringValue(row, "knowledge_id")
		docHandle := r.RegisterDocument(knowledgeID)
		if boolValue(row, "is_faq") {
			chunkID := stringValue(row, "faq_id")
			if chunkID == "" {
				continue
			}
			title := firstNonEmpty(stringValue(row, "faq_question"), stringValue(row, "title"))
			chunkHandle := r.RegisterChunk(ChunkReference{
				ChunkID:       chunkID,
				KnowledgeID:   knowledgeID,
				DocumentTitle: title,
				ChunkType:     "faq",
			})
			fmt.Fprintf(&b, "  <document id=\"%s\" type=\"faq\">\n", escapeAttr(docHandle))
			fmt.Fprintf(&b, "    <chunk id=\"%s\" type=\"faq\">\n", escapeAttr(chunkHandle))
			if title != "" {
				fmt.Fprintf(&b, "      <question>%s</question>\n", escapeText(title))
			}
			for _, answer := range stringSliceValue(row["faq_answers"]) {
				fmt.Fprintf(&b, "      <answer>%s</answer>\n", escapeText(answer))
			}
			b.WriteString("    </chunk>\n  </document>\n")
			count++
			continue
		}

		if docHandle == "" {
			continue
		}
		fmt.Fprintf(&b, "  <document id=\"%s\"", escapeAttr(docHandle))
		if title := stringValue(row, "title"); title != "" {
			fmt.Fprintf(&b, " title=\"%s\"", escapeAttr(title))
		}
		if docType := stringValue(row, "type"); docType != "" {
			fmt.Fprintf(&b, " type=\"%s\"", escapeAttr(docType))
		}
		if fileType := stringValue(row, "file_type"); fileType != "" {
			fmt.Fprintf(&b, " file_type=\"%s\"", escapeAttr(fileType))
		}
		fmt.Fprintf(&b, " chunk_count=\"%d\">\n", intValue(row, "chunk_count"))
		if description := stringValue(row, "description"); description != "" {
			fmt.Fprintf(&b, "    <description>%s</description>\n", escapeText(description))
		}
		b.WriteString("  </document>\n")
		count++
	}
	b.WriteString("</documents>")
	if count == 0 {
		return r.CompactKnownText(fallback)
	}
	return b.String()
}

type modelChunk struct {
	handle     string
	docHandle  string
	kbHandle   string
	title      string
	metadata   string
	chunkType  string
	index      int
	view       string
	match      string
	content    string
	question   string
	answers    []string
	images     []map[string]interface{}
	docRealID  string
	kbRealID   string
	chunkReal  string
	inputOrder int
}

func (r *sourceRegistry) modelKnowledgeOutput(mode string, rows []map[string]interface{}, fallback string) string {
	chunks := make([]modelChunk, 0, len(rows))
	for idx, row := range rows {
		chunkID := firstNonEmpty(stringValue(row, "chunk_id"), stringValue(row, "faq_id"), stringValue(row, "id"))
		knowledgeID := stringValue(row, "knowledge_id")
		kbID := firstNonEmpty(stringValue(row, "knowledge_base_id"), stringValue(row, "knowledge_base"))
		title := firstNonEmpty(stringValue(row, "knowledge_title"), stringValue(row, "title"))
		if chunkID == "" {
			continue
		}
		chunkType := stringValue(row, "chunk_type")
		if stringValue(row, "faq_id") != "" && chunkType == "" {
			chunkType = "faq"
		}
		chunkIndex := intValue(row, "chunk_index")
		if chunkIndex == 0 {
			chunkIndex = intValue(row, "index")
		}
		chunkHandle := r.RegisterChunk(ChunkReference{
			ChunkID:         chunkID,
			KnowledgeID:     knowledgeID,
			KnowledgeBaseID: kbID,
			DocumentTitle:   title,
			ChunkIndex:      chunkIndex,
			ChunkType:       chunkType,
		})
		chunks = append(chunks, modelChunk{
			handle:     chunkHandle,
			docHandle:  r.RegisterDocument(knowledgeID),
			kbHandle:   r.RegisterKnowledgeBase(kbID),
			title:      title,
			metadata:   stringValue(row, "knowledge_metadata"),
			chunkType:  chunkType,
			index:      chunkIndex,
			view:       viewForRow(row, mode),
			match:      firstNonEmpty(stringValue(row, "match_snippet"), stringValue(row, "matched_content")),
			content:    stringValue(row, "content"),
			question:   firstNonEmpty(stringValue(row, "faq_question"), stringValue(row, "faq_standard_question")),
			answers:    stringSliceValue(row["faq_answers"]),
			images:     mapsValue(row["images"]),
			docRealID:  knowledgeID,
			kbRealID:   kbID,
			chunkReal:  chunkID,
			inputOrder: idx,
		})
	}
	if len(chunks) == 0 {
		return r.CompactKnownText(fallback)
	}
	return renderKnowledgeChunks(mode, chunks)
}

func viewForRow(row map[string]interface{}, mode string) string {
	if stringValue(row, "content") != "" {
		return "full"
	}
	if mode == "deep_read" {
		return "full"
	}
	return "match"
}

func (r *sourceRegistry) modelKnowledgeChunksOutput(data map[string]interface{}, fallback string) string {
	rows := mapsValue(data["chunks"])
	title := stringValue(data, "knowledge_title")
	knowledgeID := stringValue(data, "knowledge_id")
	for _, row := range rows {
		if stringValue(row, "knowledge_id") == "" {
			row["knowledge_id"] = knowledgeID
		}
		if stringValue(row, "knowledge_title") == "" {
			row["knowledge_title"] = title
		}
	}
	output := r.modelKnowledgeOutput("deep_read", rows, fallback)
	if len(rows) == 0 {
		return output
	}
	remaining := intValue(data, "total_chunks") - intValue(data, "fetched_chunks")
	if remaining > 0 {
		output = strings.TrimSuffix(output, "</retrieval>")
		output += fmt.Sprintf("  <pagination remaining=\"%d\" page=\"%d\" page_size=\"%d\" />\n</retrieval>",
			remaining, intValue(data, "page"), intValue(data, "page_size"))
	}
	return output
}

func renderKnowledgeChunks(mode string, chunks []modelChunk) string {
	type docGroup struct {
		handle   string
		kbHandle string
		title    string
		metadata string
		chunks   []modelChunk
		order    int
	}
	groupsByKey := make(map[string]*docGroup)
	var groups []*docGroup
	for _, chunk := range chunks {
		key := chunk.docHandle
		if key == "" {
			key = "chunk:" + chunk.handle
		}
		group := groupsByKey[key]
		if group == nil {
			group = &docGroup{
				handle: chunk.docHandle, kbHandle: chunk.kbHandle, title: chunk.title,
				metadata: chunk.metadata, order: chunk.inputOrder,
			}
			groupsByKey[key] = group
			groups = append(groups, group)
		} else if group.metadata == "" {
			group.metadata = chunk.metadata
		}
		group.chunks = append(group.chunks, chunk)
	}
	sort.SliceStable(groups, func(i, j int) bool { return groups[i].order < groups[j].order })

	var b strings.Builder
	fmt.Fprintf(&b, "<retrieval type=\"knowledge\" mode=\"%s\">\n", escapeAttr(mode))
	for _, group := range groups {
		b.WriteString("  <document")
		if group.handle != "" {
			fmt.Fprintf(&b, " id=\"%s\"", escapeAttr(group.handle))
		}
		if group.kbHandle != "" {
			fmt.Fprintf(&b, " kb=\"%s\"", escapeAttr(group.kbHandle))
		}
		if group.title != "" {
			fmt.Fprintf(&b, " title=\"%s\"", escapeAttr(group.title))
		}
		b.WriteString(">\n")
		if group.metadata != "" {
			fmt.Fprintf(&b, "    <metadata>%s</metadata>\n", escapeText(group.metadata))
		}
		for _, chunk := range group.chunks {
			fmt.Fprintf(&b, "    <chunk id=\"%s\" index=\"%d\" view=\"%s\"", chunk.handle, chunk.index, chunk.view)
			if chunk.chunkType != "" {
				fmt.Fprintf(&b, " type=\"%s\"", escapeAttr(chunk.chunkType))
			}
			b.WriteString(">\n")
			if chunk.question != "" {
				fmt.Fprintf(&b, "      <question>%s</question>\n", escapeText(chunk.question))
			}
			if chunk.match != "" {
				fmt.Fprintf(&b, "      <match>%s</match>\n", escapeText(chunk.match))
			}
			if chunk.content != "" {
				fmt.Fprintf(&b, "      <content>%s</content>\n", escapeText(chunk.content))
			}
			for _, answer := range chunk.answers {
				fmt.Fprintf(&b, "      <answer>%s</answer>\n", escapeText(answer))
			}
			for _, image := range chunk.images {
				imageURL := stringValue(image, "url")
				if imageURL == "" {
					continue
				}
				caption := stringValue(image, "caption")
				fmt.Fprintf(&b, "      ![%s](%s)\n", caption, imageURL)
			}
			b.WriteString("    </chunk>\n")
		}
		b.WriteString("  </document>\n")
	}
	b.WriteString("</retrieval>")
	return b.String()
}

func (r *sourceRegistry) modelWebSearchOutput(rows []map[string]interface{}, fallback string) string {
	if len(rows) == 0 {
		return r.CompactKnownText(fallback)
	}
	var b strings.Builder
	b.WriteString("<retrieval type=\"web\" mode=\"search\">\n")
	count := 0
	for _, row := range rows {
		rawURL := stringValue(row, "url")
		if rawURL == "" {
			continue
		}
		handle := r.RegisterWeb(rawURL, stringValue(row, "title"))
		fmt.Fprintf(&b, "  <page id=\"%s\" title=\"%s\">\n", handle, escapeAttr(stringValue(row, "title")))
		b.WriteString("    <evidence type=\"search_summary\" verified=\"false\" />\n")
		if snippet := stringValue(row, "snippet"); snippet != "" {
			fmt.Fprintf(&b, "    <match>%s</match>\n", escapeText(snippet))
		}
		if content := stringValue(row, "content"); content != "" && content != stringValue(row, "snippet") {
			fmt.Fprintf(&b, "    <content>%s</content>\n", escapeText(content))
		}
		if published := stringValue(row, "published_at"); published != "" {
			fmt.Fprintf(&b, "    <published>%s</published>\n", escapeText(published))
		}
		b.WriteString("  </page>\n")
		count++
	}
	b.WriteString("</retrieval>")
	if count == 0 {
		return r.CompactKnownText(fallback)
	}
	return b.String()
}

func (r *sourceRegistry) modelWebFetchOutput(rows []map[string]interface{}, fallback string) string {
	if len(rows) == 0 {
		return r.CompactKnownText(fallback)
	}
	var b strings.Builder
	b.WriteString("<retrieval type=\"web\" mode=\"fetch\">\n")
	count, successCount, failedCount := 0, 0, 0
	for _, row := range rows {
		rawURL := stringValue(row, "url")
		if rawURL == "" {
			continue
		}
		title := stringValue(row, "title")
		handle := r.RegisterWeb(rawURL, title)
		status := stringValue(row, "status")
		if status == "" {
			status = "success"
		}
		fmt.Fprintf(&b, "  <page id=\"%s\" status=\"%s\"", handle, escapeAttr(status))
		if title != "" {
			fmt.Fprintf(&b, " title=\"%s\"", escapeAttr(title))
		}
		if status == "success" {
			b.WriteString(" view=\"full\">\n")
			successCount++
			if summary := stringValue(row, "summary"); summary != "" {
				fmt.Fprintf(&b, "    <summary>%s</summary>\n", escapeText(summary))
			}
			if summaryStatus := stringValue(row, "summary_status"); summaryStatus == "failed" {
				fmt.Fprintf(&b, "    <summary_error code=\"%s\">%s</summary_error>\n",
					escapeAttr(stringValue(row, "summary_error_code")), escapeText(stringValue(row, "summary_error_message")))
			}
			if content := stringValue(row, "raw_content"); content != "" {
				fmt.Fprintf(&b, "    <content>%s</content>\n", escapeText(content))
			}
		} else {
			fmt.Fprintf(&b, " retryable=\"%t\"", boolValue(row, "retryable"))
			if errorCode := stringValue(row, "error_code"); errorCode != "" {
				fmt.Fprintf(&b, " error_code=\"%s\"", escapeAttr(errorCode))
			}
			b.WriteString(">\n")
			if errorMessage := stringValue(row, "error_message"); errorMessage != "" {
				fmt.Fprintf(&b, "    <error>%s</error>\n", escapeText(errorMessage))
			}
			if status == "failed" {
				failedCount++
			}
		}
		b.WriteString("  </page>\n")
		count++
	}
	b.WriteString("</retrieval>")
	if count == 0 {
		return r.CompactKnownText(fallback)
	}
	if failedCount > 0 {
		b.WriteString("\n\n=== Next Steps ===\n")
		if successCount == 0 {
			b.WriteString("- All page fetches failed. Stop expanding web searches and answer from existing web_search titles, URLs, and snippets.\n")
			b.WriteString("- Explicitly state that page content was not verified and treat dynamic facts as uncertain.")
		} else {
			b.WriteString("- Use successful page content together with existing search snippets; failed URLs do not invalidate successful evidence.\n")
			b.WriteString("- Do not retry non-retryable failures. If evidence is sufficient, answer now.")
		}
	}
	return b.String()
}

func mapsValue(value interface{}) []map[string]interface{} {
	if value == nil {
		return nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var rows []map[string]interface{}
	if err := json.Unmarshal(encoded, &rows); err != nil {
		return nil
	}
	return rows
}

func stringValue(values map[string]interface{}, key string) string {
	if values == nil {
		return ""
	}
	switch value := values[key].(type) {
	case string:
		return value
	case fmt.Stringer:
		return value.String()
	case json.Number:
		return value.String()
	default:
		return ""
	}
}

func intValue(values map[string]interface{}, key string) int {
	if values == nil {
		return 0
	}
	switch value := values[key].(type) {
	case int:
		return value
	case int32:
		return int(value)
	case int64:
		return int(value)
	case float64:
		return int(value)
	case json.Number:
		result, _ := value.Int64()
		return int(result)
	default:
		return 0
	}
}

func boolValue(values map[string]interface{}, key string) bool {
	if values == nil {
		return false
	}
	value, _ := values[key].(bool)
	return value
}

func stringSliceValue(value interface{}) []string {
	if value == nil {
		return nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var values []string
	if err := json.Unmarshal(encoded, &values); err != nil {
		return nil
	}
	return values
}

func escapeText(value string) string {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return replacer.Replace(value)
}
