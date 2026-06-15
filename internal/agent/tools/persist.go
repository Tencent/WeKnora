package tools

import (
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// persistStripFields lists bulky Data keys to drop before SSE replay / DB storage.
var persistStripFields = map[string][]string{
	"knowledge_chunks_list": {"chunks"},
	"grep_results":          {"chunk_results"},
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
