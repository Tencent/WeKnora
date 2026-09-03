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

// persistStripFieldsByTool drops binary / duplicate blobs. stdout/stderr stay
// (compacted separately) so a history reload can still render the card.
var persistStripFieldsByTool = map[string][]string{
	ToolShellExec:        {"content", "content_base64"},
	ToolReadSandboxFile:  {"content", "content_base64"},
	ToolWriteSandboxFile: {"content", "content_base64"},
	ToolEditSandboxFile:  {"content", "content_base64"},
}

// clientStripFieldsByTool is the lighter omit list for live SSE. The UI
// needs stdout/stderr to render a terminal card; those streams are already
// capped by the tool. Persist still uses persistStripFieldsByTool.
var clientStripFieldsByTool = map[string][]string{
	ToolShellExec:        {"content", "content_base64"},
	ToolReadSandboxFile:  {"content", "content_base64"},
	ToolWriteSandboxFile: {"content", "content_base64"},
	ToolEditSandboxFile:  {"content", "content_base64"},
}

const historicalSandboxOutputChars = 4 * 1024

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
func SanitizeToolDataForPersist(toolName string, data map[string]interface{}) map[string]interface{} {
	return sanitizeToolData(data, persistStripFieldsByTool[toolName])
}

func sanitizeToolDataForClient(toolName string, data map[string]interface{}) map[string]interface{} {
	omit := clientStripFieldsByTool[toolName]
	if omit == nil {
		omit = persistStripFieldsByTool[toolName]
	}
	return sanitizeToolData(data, omit)
}

func sanitizeToolData(data map[string]interface{}, extraOmit []string) map[string]interface{} {
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
	for _, key := range extraOmit {
		delete(out, key)
	}
	return out
}

// SanitizeToolResultForClient builds stream / persistence metadata for the UI.
func SanitizeToolResultForClient(toolName string, result *types.ToolResult) map[string]interface{} {
	meta := map[string]interface{}{}
	if result == nil {
		return meta
	}
	if result.Data != nil {
		for k, v := range sanitizeToolDataForClient(toolName, result.Data) {
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
	if isSandboxContentTool(toolName) {
		return compactShellExecHeadline(data)
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
			if isSandboxContentTool(tc.Name) {
				// display_type is for the live card; history still needs the
				// command, exit, and a head+tail of the streams. Replacing
				// that with a one-line "output omitted" leaves the next turn
				// (and a reload of the card) with no structure at all.
				result.Output = compactHistoricalSandboxOutput(result.Output)
			} else if ShouldOmitRawToolOutput(tc.Name, result.Data) {
				result.Output = compactToolSummary(result.Success, result.Error, result.Data)
			}
			result.Data = SanitizeToolDataForPersist(tc.Name, result.Data)
			compactSandboxStreamFields(result.Data)
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
	if isSandboxContentTool(toolName) {
		if rebuilt := compactSandboxHistory(result); rebuilt != "" {
			if result.Success {
				return rebuilt
			}
			return failedToolVisibleContent(rebuilt, result.Error)
		}
	}
	if !result.Success {
		return failedToolVisibleContent(result.Output, result.Error)
	}
	if result.Output != "" && !ShouldOmitRawToolOutput(toolName, result.Data) {
		return result.Output
	}
	return compactToolSummary(result.Success, result.Error, result.Data)
}

func isSandboxContentTool(toolName string) bool {
	return toolName == ToolShellExec || toolName == ToolReadSandboxFile || toolName == ToolExecuteSkillScript
}

// failedToolVisibleContent keeps stdout/stderr (in Output) when a tool fails.
// Error is often a one-line exit status plus a retry hint; the streams are
// what the model needs to change arguments instead of guessing.
func failedToolVisibleContent(output, errMsg string) string {
	output = strings.TrimSpace(output)
	errMsg = strings.TrimSpace(errMsg)
	switch {
	case output == "" && errMsg == "":
		return "Error: tool call failed"
	case output == "":
		return "Error: " + errMsg
	case errMsg == "" || strings.Contains(output, errMsg):
		return output
	default:
		return output + "\n\nError: " + errMsg
	}
}

func compactHistoricalSandboxOutput(output string) string {
	if len(output) <= historicalSandboxOutputChars {
		return output
	}
	const marker = "\n...[historical tool output compacted]...\n"
	kept := historicalSandboxOutputChars - len(marker)
	head := kept / 4
	tail := kept - head
	return output[:head] + marker + output[len(output)-tail:]
}

func compactSandboxStreamFields(data map[string]interface{}) {
	if data == nil {
		return
	}
	for _, key := range []string{"stdout", "stderr"} {
		raw, ok := data[key]
		if !ok || raw == nil {
			continue
		}
		s, ok := raw.(string)
		if !ok || s == "" {
			continue
		}
		data[key] = compactHistoricalSandboxOutput(s)
	}
}

func compactSandboxHistory(result *types.ToolResult) string {
	if result == nil {
		return ""
	}
	if result.Output != "" && !isOmittedHistoryPlaceholder(result.Output) {
		return compactHistoricalSandboxOutput(result.Output)
	}
	if rebuilt := rebuildShellExecHistory(result.Data); rebuilt != "" {
		return rebuilt
	}
	return compactHistoricalSandboxOutput(result.Output)
}

func isOmittedHistoryPlaceholder(output string) bool {
	return strings.Contains(output, "omitted from history")
}

func compactShellExecHeadline(data map[string]interface{}) string {
	exit := intField(data, "exit_code")
	cmd := stringField(data, "command")
	if cmd == "" {
		return fmt.Sprintf("shell_exec exit=%d", exit)
	}
	const maxCmd = 240
	if len(cmd) > maxCmd {
		cmd = cmd[:maxCmd] + "..."
	}
	return fmt.Sprintf("shell_exec exit=%d command=%s", exit, cmd)
}

func rebuildShellExecHistory(data map[string]interface{}) string {
	if data == nil {
		return ""
	}
	stdout := stringField(data, "stdout")
	stderr := stringField(data, "stderr")
	if stdout == "" && stderr == "" {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "shell_exec exit=%d", intField(data, "exit_code"))
	if cmd := stringField(data, "command"); cmd != "" {
		fmt.Fprintf(&b, " command=%s", cmd)
	}
	if wd := stringField(data, "work_dir"); wd != "" {
		fmt.Fprintf(&b, " work_dir=%s", wd)
	}
	b.WriteByte('\n')
	if stdout != "" {
		b.WriteString("## Stdout\n```\n")
		b.WriteString(stdout)
		if !strings.HasSuffix(stdout, "\n") {
			b.WriteByte('\n')
		}
		b.WriteString("```\n")
	}
	if stderr != "" {
		b.WriteString("## Stderr\n```\n")
		b.WriteString(stderr)
		if !strings.HasSuffix(stderr, "\n") {
			b.WriteByte('\n')
		}
		b.WriteString("```\n")
	}
	return compactHistoricalSandboxOutput(b.String())
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
	case "shell_exec":
		if rebuilt := rebuildShellExecHistory(data); rebuilt != "" {
			return rebuilt
		}
		return compactShellExecHeadline(data)
	case "write_sandbox_file":
		path := stringField(data, "path")
		size := intField(data, "size")
		if path != "" {
			return fmt.Sprintf("Wrote %s (%d bytes)", path, size)
		}
	case "edit_sandbox_file":
		path := stringField(data, "path")
		size := intField(data, "size")
		n := intField(data, "replacements")
		if path != "" {
			return fmt.Sprintf("Edited %s (%d replacement(s), %d bytes)", path, n, size)
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
