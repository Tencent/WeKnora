package tools

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestShouldOmitRawToolOutput(t *testing.T) {
	if !ShouldOmitRawToolOutput(ToolListKnowledgeChunks, map[string]interface{}{"display_type": "knowledge_chunks_list"}) {
		t.Fatal("structured list_knowledge_chunks output should be omitted")
	}
	if !ShouldOmitRawToolOutput(ToolGrepChunks, map[string]interface{}{"display_type": "grep_results"}) {
		t.Fatal("structured grep output should be omitted")
	}
	if ShouldOmitRawToolOutput("custom_tool", nil) {
		t.Fatal("unknown tools should keep raw output by default")
	}
}

func TestSanitizeToolDataForPersist_knowledgeChunksList(t *testing.T) {
	data := map[string]interface{}{
		"display_type":    "knowledge_chunks_list",
		"knowledge_title": "sample.pdf",
		"fetched_chunks":  50,
		"total_chunks":    282,
		"chunks":          []map[string]interface{}{{"content": "secret"}},
	}
	out := SanitizeToolDataForPersist(ToolListKnowledgeChunks, data)
	if _, ok := out["chunks"]; ok {
		t.Fatal("chunk bodies should be stripped from persisted tool data")
	}
	if out["fetched_chunks"] != 50 {
		t.Fatalf("summary fields should be kept, got %#v", out["fetched_chunks"])
	}
}

func TestSanitizeAgentStepsForStorage_stripsLargeOutput(t *testing.T) {
	steps := []types.AgentStep{{
		Iteration: 1,
		ToolCalls: []types.ToolCall{{
			ID:   "call-1",
			Name: ToolListKnowledgeChunks,
			Result: &types.ToolResult{
				Success: true,
				Output:  strings.Repeat("x", 10000),
				Data: map[string]interface{}{
					"display_type":    "knowledge_chunks_list",
					"knowledge_title": "sample.pdf",
					"fetched_chunks":  50,
					"total_chunks":    282,
					"chunks":          []map[string]interface{}{{"content": "body"}},
				},
			},
		}},
	}}

	sanitized := SanitizeAgentStepsForStorage(steps)
	result := sanitized[0].ToolCalls[0].Result
	if len(result.Output) >= 10000 {
		t.Fatal("persisted output should be compacted")
	}
	if !strings.Contains(result.Output, "content omitted from history") {
		t.Fatalf("unexpected compact output: %q", result.Output)
	}
	if _, ok := result.Data["chunks"]; ok {
		t.Fatal("chunk bodies should be removed from persisted data")
	}
}

func TestWikiReadPageDisabledPreviewRedactsClientAndStoredSources(t *testing.T) {
	result := &types.ToolResult{
		Success: true,
		Output:  `<wiki_page><metadata><sources><source knowledge_id="doc-1">Paper.pdf</source></sources></metadata><content>Readable Wiki body.</content></wiki_page>`,
		Data: map[string]interface{}{
			"preview_enabled": false,
		},
	}

	client := SanitizeToolResultForClient(ToolWikiReadPage, result)
	clientOutput, _ := client["output"].(string)
	if strings.Contains(clientOutput, "doc-1") || strings.Contains(clientOutput, "knowledge_id") {
		t.Fatalf("disabled client output must redact model-only source handles: %q", clientOutput)
	}
	if !strings.Contains(clientOutput, "<sources>") || !strings.Contains(clientOutput, "Paper.pdf") {
		t.Fatalf("source titles must remain visible: %q", clientOutput)
	}
	if !strings.Contains(clientOutput, "Readable Wiki body.") {
		t.Fatalf("Wiki content must remain visible: %q", clientOutput)
	}
	if !strings.Contains(result.Output, "doc-1") {
		t.Fatal("client sanitization must not mutate the in-memory model result")
	}

	stored := SanitizeAgentStepsForStorage([]types.AgentStep{{ToolCalls: []types.ToolCall{{
		Name:   ToolWikiReadPage,
		Result: result,
	}}}})
	if !strings.Contains(stored[0].ToolCalls[0].Result.Output, "doc-1") {
		t.Fatal("persisted model history must retain the source handle")
	}

	messages := []*types.Message{{AgentSteps: types.AgentSteps(stored)}}
	clientMessages := SanitizeMessagesForClient(messages)
	clientHistoryOutput := clientMessages[0].AgentSteps[0].ToolCalls[0].Result.Output
	if strings.Contains(clientHistoryOutput, "doc-1") || strings.Contains(clientHistoryOutput, "knowledge_id") {
		t.Fatalf("history API copy must redact source handles: %q", clientHistoryOutput)
	}
	if !strings.Contains(clientHistoryOutput, "Paper.pdf") {
		t.Fatalf("history API copy should retain the source title: %q", clientHistoryOutput)
	}
	if !strings.Contains(messages[0].AgentSteps[0].ToolCalls[0].Result.Output, "doc-1") {
		t.Fatal("client history sanitization must not mutate the persisted message")
	}
}

func TestWikiReadSourceDocArgumentsAreRedactedOnlyFromClientCopy(t *testing.T) {
	liveArgs := SanitizeToolArgumentsForClient(ToolWikiReadSourceDoc, map[string]interface{}{
		"knowledge_id": "doc-1",
		"query":        "evidence",
	})
	if _, ok := liveArgs["knowledge_id"]; ok {
		t.Fatal("live tool-call payload must not expose the source document ID")
	}
	if liveArgs["query"] != "evidence" {
		t.Fatalf("live non-identity arguments must remain, got %#v", liveArgs)
	}

	messages := []*types.Message{{AgentSteps: types.AgentSteps{{ToolCalls: []types.ToolCall{{
		Name: ToolWikiReadSourceDoc,
		Args: map[string]interface{}{
			"knowledge_id": "doc-1",
			"query":        "evidence",
		},
	}}}}}}

	client := SanitizeMessagesForClient(messages)
	args := client[0].AgentSteps[0].ToolCalls[0].Args
	if _, ok := args["knowledge_id"]; ok {
		t.Fatal("history API copy must not expose the source document ID")
	}
	if args["query"] != "evidence" {
		t.Fatalf("non-identity arguments must remain, got %#v", args)
	}
	if messages[0].AgentSteps[0].ToolCalls[0].Args["knowledge_id"] != "doc-1" {
		t.Fatal("client sanitization must not mutate persisted model history")
	}
}

func TestLegacyWikiResultsWithoutPreviewCapabilityFailClosed(t *testing.T) {
	pageClient := SanitizeToolResultForClient(ToolWikiReadPage, &types.ToolResult{
		Success: true,
		Output:  `<sources><source knowledge_id="legacy-doc">Legacy.pdf</source></sources>`,
		Data: map[string]interface{}{
			"source_documents": []interface{}{map[string]interface{}{"knowledge_id": "legacy-doc"}},
		},
	})
	if strings.Contains(pageClient["output"].(string), "legacy-doc") {
		t.Fatal("legacy Wiki page output without explicit preview capability must redact document IDs")
	}
	if _, ok := pageClient["source_documents"]; ok {
		t.Fatal("legacy Wiki page metadata without capability must be removed")
	}

	docClient := SanitizeToolResultForClient(ToolWikiReadSourceDoc, &types.ToolResult{
		Success: true,
		Data: map[string]interface{}{
			"knowledge_id":      "legacy-doc",
			"knowledge_base_id": "kb-1",
			"file_type":         "pdf",
		},
	})
	for _, key := range []string{"knowledge_id", "knowledge_base_id", "file_type"} {
		if _, ok := docClient[key]; ok {
			t.Fatalf("legacy source-doc client metadata must remove %s", key)
		}
	}
}

func TestWikiReadPageEnabledPreviewKeepsClientSources(t *testing.T) {
	result := &types.ToolResult{
		Success: true,
		Output:  `<sources><source knowledge_id="doc-1">Paper.pdf</source></sources>`,
		Data: map[string]interface{}{
			"preview_enabled": true,
		},
	}
	client := SanitizeToolResultForClient(ToolWikiReadPage, result)
	if !strings.Contains(client["output"].(string), "doc-1") {
		t.Fatal("enabled client output should keep model source handles")
	}
}

func TestSanitizeToolResultForClient_omitsOutput(t *testing.T) {
	meta := SanitizeToolResultForClient(ToolListKnowledgeChunks, &types.ToolResult{
		Success: true,
		Output:  "<knowledge_chunks>very large</knowledge_chunks>",
		Data: map[string]interface{}{
			"display_type":    "knowledge_chunks_list",
			"knowledge_title": "sample.pdf",
			"fetched_chunks":  1,
			"total_chunks":    1,
		},
	})
	if _, ok := meta["output"]; ok {
		t.Fatal("raw output should not be sent to client metadata")
	}
	if meta["fetched_chunks"] != 1 {
		t.Fatalf("summary metadata should remain, got %#v", meta["fetched_chunks"])
	}
}

func TestSandboxToolPersistenceStripsDuplicatePayloadsAndCompactsHistory(t *testing.T) {
	rawOutput := strings.Repeat("shell output ", 1000)
	steps := []types.AgentStep{{
		ToolCalls: []types.ToolCall{{
			Name: ToolShellExec,
			Result: &types.ToolResult{
				Success: true,
				Output:  rawOutput,
				Data: map[string]interface{}{
					"stdout":                strings.Repeat("x", 10000),
					"stderr":                strings.Repeat("y", 10000),
					"content":               strings.Repeat("z", 10000),
					"content_base64":        strings.Repeat("A", 10000),
					"exit_code":             0,
					"stdout_original_bytes": 10000,
					"stdout_truncated":      true,
				},
			},
		}},
	}}

	sanitized := SanitizeAgentStepsForStorage(steps)
	result := sanitized[0].ToolCalls[0].Result

	assert := func(condition bool, message string) {
		t.Helper()
		if !condition {
			t.Fatal(message)
		}
	}
	assert(len(result.Output) <= historicalSandboxOutputChars, "persisted shell output must be capped")
	assert(strings.Contains(result.Output, "shell output"),
		"persisted output must keep the stream, not a one-line omit")
	stdout, _ := result.Data["stdout"].(string)
	stderr, _ := result.Data["stderr"].(string)
	assert(stdout != "" && len(stdout) <= historicalSandboxOutputChars, "stdout should be kept and capped")
	assert(stderr != "" && len(stderr) <= historicalSandboxOutputChars, "stderr should be kept and capped")
	for _, key := range []string{"content", "content_base64"} {
		_, exists := result.Data[key]
		assert(!exists, key+" should be stripped")
	}
	assert(result.Data["exit_code"] == 0, "exit metadata should remain")
	assert(len(CompactToolOutputForHistory(ToolShellExec, steps[0].ToolCalls[0].Result)) <= historicalSandboxOutputChars,
		"historical replay must independently cap legacy raw output")
}

func TestSanitizeAgentStepsForStorage_shellExecKeepsStructuredOutput(t *testing.T) {
	skillDir := "/opt/weknora/tenant/skills/smart-charts"
	command := skillDir + "/.venv/bin/python " + skillDir + "/plot.py"
	stdout := "README.md\ncharts.py\nrequirements.txt\n"
	markdown := "=== Shell Exec ===\n**Command**: `" + command + "`\n" +
		"**Work Dir**: " + skillDir + "\n**Exit Code**: 0\n\n" +
		"## Stdout\n\n```\n" + stdout + "```\n"
	steps := []types.AgentStep{{
		ToolCalls: []types.ToolCall{{
			Name: ToolShellExec,
			Result: &types.ToolResult{
				Success: true,
				Output:  markdown,
				Data: map[string]interface{}{
					"display_type": "shell_exec",
					"command":      command,
					"work_dir":     skillDir,
					"exit_code":    0,
					"stdout":       stdout,
					"stderr":       "",
				},
			},
		}},
	}}

	sanitized := SanitizeAgentStepsForStorage(steps)
	result := sanitized[0].ToolCalls[0].Result
	if strings.Contains(result.Output, "omitted from history") {
		t.Fatalf("structured shell_exec must not collapse to an omit line, got %q", result.Output)
	}
	if !strings.Contains(result.Output, "README.md") {
		t.Fatalf("persisted output should keep stdout structure, got %q", result.Output)
	}
	if got, _ := result.Data["stdout"].(string); got != stdout {
		t.Fatalf("persisted stdout should remain for the UI card, got %#v", result.Data["stdout"])
	}

	history := CompactToolOutputForHistory(ToolShellExec, result)
	if strings.Contains(history, "omitted from history") {
		t.Fatalf("history replay must keep the streams, got %q", history)
	}
	if !strings.Contains(history, "README.md") {
		t.Fatalf("history replay should keep stdout, got %q", history)
	}
	if !strings.Contains(history, "plot.py") {
		t.Fatalf("history replay should keep the full command, got %q", history)
	}
}

func TestSanitizeToolResultForClientKeepsShellStreams(t *testing.T) {
	meta := SanitizeToolResultForClient(ToolShellExec, &types.ToolResult{
		Success: true,
		Output:  "=== Shell Exec ===\n**Command**: `ls`\n",
		Data: map[string]interface{}{
			"display_type": "shell_exec",
			"command":      "ls",
			"exit_code":    0,
			"stdout":       "README.md\n",
			"stderr":       "",
		},
	})
	if _, ok := meta["output"]; ok {
		t.Fatal("structured shell_exec should omit the markdown Output from client metadata")
	}
	if meta["stdout"] != "README.md\n" {
		t.Fatalf("live UI needs stdout, got %#v", meta["stdout"])
	}
	if meta["command"] != "ls" {
		t.Fatalf("command should remain, got %#v", meta["command"])
	}
	if meta["display_type"] != "shell_exec" {
		t.Fatalf("display_type should remain, got %#v", meta["display_type"])
	}
}

func TestCompactToolOutputForHistory_recoversStreamsFromPlaceholder(t *testing.T) {
	history := CompactToolOutputForHistory(ToolShellExec, &types.ToolResult{
		Success: true,
		Output:  "shell_exec exit=0 command=ls (output omitted from history)",
		Data: map[string]interface{}{
			"display_type": "shell_exec",
			"command":      "ls /opt/weknora/tenant/skills/smart-charts",
			"exit_code":    0,
			"stdout":       "SKILL.md\nplot.py\n",
		},
	})
	if strings.Contains(history, "omitted from history") {
		t.Fatalf("should rebuild from stdout instead of the omit placeholder, got %q", history)
	}
	if !strings.Contains(history, "SKILL.md") {
		t.Fatalf("rebuilt history should keep stdout, got %q", history)
	}
	if !strings.Contains(history, "smart-charts") {
		t.Fatalf("rebuilt history should keep the command, got %q", history)
	}
}
