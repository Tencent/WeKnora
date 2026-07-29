package tools

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// persistStripFields lists bulky Data keys to drop before SSE replay / DB storage.
var persistStripFields = map[string][]string{
	"knowledge_chunks_list": {"chunks"},
	"grep_results":          {"chunk_results"},
}

// chunkRefSources maps a tool result's display_type to the fields needed to
// extract a compact chunk-id attribution summary before the bulky payload is
// stripped. The summary survives sanitization (as Data["_chunk_refs"]) so
// like/dislike feedback can still be attributed back to the chunks the agent
// retrieved via grep_chunks / list_knowledge_chunks — whose full payloads
// (content, snippets) are too large to persist, but whose chunk ids are
// essential for the per-chunk feedback statistics surface.
var chunkRefSources = map[string]chunkRefSource{
	"grep_results":          {listKey: "chunk_results", chunkIDKey: "chunk_id", kbIDKey: "knowledge_base_id"},
	"knowledge_chunks_list": {listKey: "chunks", chunkIDKey: "chunk_id", kbIDKey: "knowledge_base"},
}

type chunkRefSource struct {
	listKey    string
	chunkIDKey string
	kbIDKey    string
}

// extractPersistedChunkRefs pulls (chunk_id, knowledge_base_id) pairs out of a
// tool result's bulky Data field before it is stripped, returning a compact
// list that survives sanitization. Returns nil when no chunk ids are found.
//
// The list value may be a typed slice (e.g. []grepChunkResult from grep_chunks
// or []map[string]interface{} from list_knowledge_chunks) — neither is
// []interface{}, so reflection is used to iterate. Struct elements are
// normalized via JSON marshalling so json tags (chunk_id, knowledge_base_id)
// are respected.
func extractPersistedChunkRefs(data map[string]interface{}, src chunkRefSource) []interface{} {
	items := toInterfaceSlice(data[src.listKey])
	if len(items) == 0 {
		return nil
	}
	out := make([]interface{}, 0, len(items))
	for _, item := range items {
		m := toStringMap(item)
		if m == nil {
			continue
		}
		chunkID := strings.TrimSpace(fmt.Sprint(m[src.chunkIDKey]))
		if chunkID == "" {
			continue
		}
		kbID := strings.TrimSpace(fmt.Sprint(m[src.kbIDKey]))
		if kbID == "" {
			continue
		}
		out = append(out, map[string]interface{}{
			"chunk_id":          chunkID,
			"knowledge_base_id": kbID,
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// toInterfaceSlice converts any slice type to []interface{} using reflection.
// Handles typed slices like []grepChunkResult, []map[string]interface{}, etc.
func toInterfaceSlice(v interface{}) []interface{} {
	if v == nil {
		return nil
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			return nil
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Slice {
		return nil
	}
	out := make([]interface{}, rv.Len())
	for i := 0; i < rv.Len(); i++ {
		out[i] = rv.Index(i).Interface()
	}
	return out
}

// toStringMap converts a struct or map to map[string]interface{}.
// For map[string]interface{} it's a direct cast; for typed structs it
// uses JSON marshalling to respect json field tags.
func toStringMap(v interface{}) map[string]interface{} {
	if m, ok := v.(map[string]interface{}); ok {
		return m
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil
	}
	return m
}

// ShouldOmitRawToolOutput reports whether the raw XML/text Output should be
// excluded from SSE replay and persisted agent_steps. The full Output remains
// available in-memory for the current agent turn.
func ShouldOmitRawToolOutput(_ string, data map[string]interface{}) bool {
	if data == nil {
		return false
	}
	displayType, ok := data["display_type"].(string)
	return ok && displayType != ""
}

// SanitizeToolDataForPersist returns a copy of tool Data safe for DB / SSE replay.
func SanitizeToolDataForPersist(data map[string]interface{}) map[string]interface{} {
	if data == nil {
		return nil
	}
	out := make(map[string]interface{}, len(data))
	for k, v := range data {
		out[k] = v
	}
	displayType := stringField(data, "display_type")
	for _, key := range persistStripFields[displayType] {
		delete(out, key)
	}
	// Preserve a compact chunk-id attribution summary before the bulky
	// payload is dropped, so like/dislike feedback can still be attributed
	// back to the chunks the agent retrieved (grep_chunks /
	// list_knowledge_chunks). The full chunk content/snippets are stripped
	// for size, but the chunk ids are small and essential for feedback stats.
	if src, ok := chunkRefSources[displayType]; ok {
		if refs := extractPersistedChunkRefs(data, src); len(refs) > 0 {
			out["_chunk_refs"] = refs
		}
	}
	return out
}

// SanitizeToolResultForClient builds stream / persistence metadata for the UI.
func SanitizeToolResultForClient(_ string, result *types.ToolResult) map[string]interface{} {
	meta := map[string]interface{}{}
	if result == nil {
		return meta
	}
	if result.Data != nil {
		for k, v := range SanitizeToolDataForPersist(result.Data) {
			meta[k] = v
		}
	}
	if !ShouldOmitRawToolOutput("", result.Data) && result.Output != "" {
		meta["output"] = result.Output
	}
	return meta
}

// StreamContentForToolResult is the short SSE Content field for tool results.
func StreamContentForToolResult(toolName string, success bool, errMsg string, data map[string]interface{}) string {
	if !success {
		return errMsg
	}
	if ShouldOmitRawToolOutput(toolName, data) {
		return compactToolSummary(success, errMsg, data)
	}
	return ""
}

// SanitizeAgentStepsForStorage strips LLM-only payloads from persisted steps.
func SanitizeAgentStepsForStorage(steps []types.AgentStep) []types.AgentStep {
	if len(steps) == 0 {
		return steps
	}
	out := make([]types.AgentStep, len(steps))
	for i, step := range steps {
		out[i] = step
		if len(step.ToolCalls) == 0 {
			continue
		}
		toolCalls := make([]types.ToolCall, len(step.ToolCalls))
		for j, tc := range step.ToolCalls {
			toolCalls[j] = tc
			if tc.Result == nil {
				continue
			}
			result := *tc.Result
			if ShouldOmitRawToolOutput(tc.Name, result.Data) {
				result.Output = compactToolSummary(result.Success, result.Error, result.Data)
				result.Data = SanitizeToolDataForPersist(result.Data)
			}
			toolCalls[j].Result = &result
		}
		out[i].ToolCalls = toolCalls
	}
	return out
}

// CompactToolOutputForHistory rebuilds a short tool message when replaying history.
func CompactToolOutputForHistory(toolName string, result *types.ToolResult) string {
	if result == nil {
		return ""
	}
	if !result.Success {
		if result.Error != "" {
			return "Error: " + result.Error
		}
		return "Error: tool call failed"
	}
	if result.Output != "" && !ShouldOmitRawToolOutput(toolName, result.Data) {
		return result.Output
	}
	return compactToolSummary(result.Success, result.Error, result.Data)
}

func compactToolSummary(success bool, errMsg string, data map[string]interface{}) string {
	if !success {
		if errMsg != "" {
			return "Error: " + errMsg
		}
		return "Error: tool call failed"
	}
	switch stringField(data, "display_type") {
	case "knowledge_chunks_list":
		title := stringField(data, "knowledge_title")
		if title == "" {
			title = stringField(data, "knowledge_id")
		}
		fetched := intField(data, "fetched_chunks")
		total := intField(data, "total_chunks")
		if q := stringField(data, "faq_question"); q != "" {
			return fmt.Sprintf("Loaded FAQ entry: %s (content omitted from history)", q)
		}
		if title != "" && total > 0 {
			return fmt.Sprintf("Listed %d/%d chunks from %s (content omitted from history)", fetched, total, title)
		}
		if title != "" {
			return fmt.Sprintf("Listed chunks from %s (content omitted from history)", title)
		}
	case "grep_results":
		chunks := intField(data, "total_matches")
		docs := intField(data, "document_count")
		if docs == 0 {
			docs = intField(data, "result_count")
		}
		if chunks > 0 {
			return fmt.Sprintf("Keyword search found %d matching chunks across %d document(s) (details omitted from history)", chunks, docs)
		}
	case "search_results":
		count := intField(data, "result_count")
		if count == 0 {
			count = intField(data, "count")
		}
		if count > 0 {
			return fmt.Sprintf("Semantic search returned %d result(s) (details omitted from history)", count)
		}
	case "attachment_parsing":
		parsed := intField(data, "parsed_count")
		skipped := intField(data, "skipped_count")
		if skipped > 0 {
			return fmt.Sprintf("Parsed %d attachment(s), %d skipped (still processing)", parsed, skipped)
		}
		if parsed > 0 {
			return fmt.Sprintf("Parsed %d attachment(s)", parsed)
		}
	}
	if displayType := stringField(data, "display_type"); displayType != "" {
		return fmt.Sprintf("Tool completed (%s; payload omitted from history)", displayType)
	}
	return "Tool completed (payload omitted from history)"
}

func stringField(data map[string]interface{}, key string) string {
	if data == nil {
		return ""
	}
	v, ok := data[key]
	if !ok || v == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(v))
}

func intField(data map[string]interface{}, key string) int {
	if data == nil {
		return 0
	}
	v, ok := data[key]
	if !ok || v == nil {
		return 0
	}
	switch n := v.(type) {
	case int:
		return n
	case int32:
		return int(n)
	case int64:
		return int(n)
	case float64:
		return int(n)
	case float32:
		return int(n)
	default:
		return 0
	}
}
