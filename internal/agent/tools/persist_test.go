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
	for _, key := range []string{"stdout", "stderr", "content", "content_base64"} {
		_, exists := result.Data[key]
		assert(!exists, key+" should be stripped")
	}
	assert(result.Data["exit_code"] == 0, "exit metadata should remain")
	assert(len(CompactToolOutputForHistory(ToolShellExec, steps[0].ToolCalls[0].Result)) <= historicalSandboxOutputChars,
		"historical replay must independently cap legacy raw output")
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
