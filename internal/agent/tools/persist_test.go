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

func TestCompactToolOutputForHistory_failedSkillScriptKeepsStdout(t *testing.T) {
	stdout := `{"chart":{"success":false,"error":{"error":"X轴字段不存在：工作项目","available":["name","value"]}}}`
	output := "=== Script Execution: smart-charts/scripts/cli.py ===\n\n**Exit Code**: 1\n\n## Standard Output\n\n```\n" + stdout + "\n```\n"
	errMsg := "Script exited with code 1\n\n[Analyze the error above and try a different approach.]"
	history := CompactToolOutputForHistory(ToolExecuteSkillScript, &types.ToolResult{
		Success: false,
		Output:  output,
		Error:   errMsg,
		Data: map[string]interface{}{
			"display_type": "shell_exec",
			"command":      "smart-charts/scripts/cli.py --x-axis 工作项目",
			"exit_code":    1,
			"stdout":       stdout,
		},
	})
	if !strings.Contains(history, "X轴字段不存在：工作项目") {
		t.Fatalf("failed skill script history must keep stdout, got %q", history)
	}
	if !strings.Contains(history, "Error: Script exited with code 1") {
		t.Fatalf("failed skill script history must still surface the error, got %q", history)
	}
}

func TestSanitizeAgentStepsForStorage_skillScriptKeepsStreamsOnFailure(t *testing.T) {
	stdout := `{"chart":{"success":false,"error":{"error":"X轴字段不存在：工作项目"}}}`
	output := "=== Script Execution: smart-charts/scripts/cli.py ===\n\n" + stdout
	steps := []types.AgentStep{{
		ToolCalls: []types.ToolCall{{
			Name: ToolExecuteSkillScript,
			Result: &types.ToolResult{
				Success: false,
				Output:  output,
				Error:   "Script exited with code 1",
				Data: map[string]interface{}{
					"display_type": "shell_exec",
					"command":      "smart-charts/scripts/cli.py",
					"exit_code":    1,
					"stdout":       stdout,
					"stderr":       "",
				},
			},
		}},
	}}

	sanitized := SanitizeAgentStepsForStorage(steps)
	result := sanitized[0].ToolCalls[0].Result
	if strings.Contains(result.Output, "omitted from history") {
		t.Fatalf("failed skill script must not collapse to an omit line, got %q", result.Output)
	}
	if got, _ := result.Data["stdout"].(string); !strings.Contains(got, "X轴字段不存在") {
		t.Fatalf("persisted stdout should remain for the UI card, got %#v", result.Data["stdout"])
	}
}

func TestCompactToolOutputForHistory_writeSandboxFileKeepsPath(t *testing.T) {
	history := CompactToolOutputForHistory(ToolWriteSandboxFile, &types.ToolResult{
		Success: true,
		Output:  "=== Wrote sandbox file: /workspace/output/generate_ppt.py ===\n",
		Data: map[string]interface{}{
			"display_type": ToolWriteSandboxFile,
			"path":         "/workspace/output/generate_ppt.py",
			"size":         12345,
		},
	})
	if history != "Wrote /workspace/output/generate_ppt.py (12345 bytes)" {
		t.Fatalf("history should keep path and size, got %q", history)
	}
}

func TestCompactToolOutputForHistory_editSandboxFileKeepsPath(t *testing.T) {
	history := CompactToolOutputForHistory(ToolEditSandboxFile, &types.ToolResult{
		Success: true,
		Output:  "=== Edited sandbox file: /workspace/output/generate_ppt.py ===\n",
		Data: map[string]interface{}{
			"display_type": ToolEditSandboxFile,
			"path":         "/workspace/output/generate_ppt.py",
			"size":         12345,
			"replacements": 1,
		},
	})
	if history != "Edited /workspace/output/generate_ppt.py (1 replacement(s), 12345 bytes)" {
		t.Fatalf("history should keep path, replacements, and size, got %q", history)
	}
}
