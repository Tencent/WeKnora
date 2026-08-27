package tools

import (
	"fmt"
	"regexp"
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
	ToolShellExec:       {"content", "content_base64"},
	ToolReadSandboxFile: {"content", "content_base64"},
}

// clientStripFieldsByTool is the lighter omit list for live SSE. The UI
// needs stdout/stderr to render a terminal card; those streams are already
// capped by the tool. Persist still uses persistStripFieldsByTool.
var clientStripFieldsByTool = map[string][]string{
	ToolShellExec:       {"content", "content_base64"},
	ToolReadSandboxFile: {"content", "content_base64"},
}

const historicalSandboxOutputChars = 4 * 1024

var wikiSourceKnowledgeIDPattern = regexp.MustCompile(`(<source\b[^>]*?)\s+knowledge_id="[^"]*"`)

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
	out := sanitizeToolData(data, omit)
	if out == nil {
		return nil
	}
	previewEnabled, _ := out["preview_enabled"].(bool)
	if !previewEnabled {
		switch toolName {
		case ToolWikiReadPage:
			delete(out, "source_documents")
		case ToolWikiReadSourceDoc:
			delete(out, "knowledge_id")
			delete(out, "knowledge_base_id")
			delete(out, "file_type")
		}
	}
	return out
}

// sanitizeToolOutputForClient removes model-only Wiki source handles when the
// original-document preview feature was explicitly disabled. The in-memory
// ToolResult remains unchanged, so the Agent can still call
// wiki_read_source_doc with the full <sources> block.
// SanitizeToolArgumentsForClient removes model-only document handles from UI
// timeline payloads. The Agent's execution arguments and persisted model
// history remain unchanged.
func SanitizeToolArgumentsForClient(toolName string, args map[string]interface{}) map[string]interface{} {
	if toolName != ToolWikiReadSourceDoc || args == nil {
		return args
	}
	out := make(map[string]interface{}, len(args))
	for key, value := range args {
		if key != "knowledge_id" {
			out[key] = value
		}
	}
	return out
}

func sanitizeToolOutputForClient(toolName, output string, data map[string]interface{}) string {
	if toolName != ToolWikiReadPage || output == "" || data == nil {
		return output
	}
	previewEnabled, _ := data["preview_enabled"].(bool)
	if previewEnabled {
		return output
	}
	return wikiSourceKnowledgeIDPattern.ReplaceAllString(output, "$1")
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
		meta["output"] = sanitizeToolOutputForClient(toolName, result.Output, result.Data)
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
// SanitizeMessagesForClient copies message history before removing model-only
// Wiki document handles from Agent steps. Other immutable response fields may
// remain shared because this function never mutates them.
func SanitizeMessagesForClient(messages []*types.Message) []*types.Message {
	if len(messages) == 0 {
		return messages
	}
	out := make([]*types.Message, len(messages))
	for i, message := range messages {
		if message == nil {
			continue
		}
		copy := *message
		copy.AgentSteps = SanitizeAgentStepsForClient(message.AgentSteps)
		out[i] = &copy
	}
	return out
}

// SanitizeAgentStepsForClient returns a UI-safe copy without mutating the
// persisted steps used to replay tool calls to the model.
func SanitizeAgentStepsForClient(steps types.AgentSteps) types.AgentSteps {
	if len(steps) == 0 {
		return steps
	}
	out := make(types.AgentSteps, len(steps))
	for i, step := range steps {
		out[i] = step
		if len(step.ToolCalls) == 0 {
			continue
		}
		calls := make([]types.ToolCall, len(step.ToolCalls))
		for j, call := range step.ToolCalls {
			calls[j] = call
			calls[j].Args = SanitizeToolArgumentsForClient(call.Name, call.Args)
			if call.Result != nil {
				result := *call.Result
				result.Output = sanitizeToolOutputForClient(call.Name, result.Output, result.Data)
				result.Data = sanitizeToolDataForClient(call.Name, result.Data)
				calls[j].Result = &result
			}
		}
		out[i].ToolCalls = calls
	}
	return out
}

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
	if !result.Success {
		if result.Error != "" {
			return "Error: " + result.Error
		}
		return "Error: tool call failed"
	}
	if isSandboxContentTool(toolName) {
		if rebuilt := compactSandboxHistory(result); rebuilt != "" {
			return rebuilt
		}
	}
	if result.Output != "" && !ShouldOmitRawToolOutput(toolName, result.Data) {
		return result.Output
	}
	return compactToolSummary(result.Success, result.Error, result.Data)
}

func isSandboxContentTool(toolName string) bool {
	return toolName == ToolShellExec || toolName == ToolReadSandboxFile
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
