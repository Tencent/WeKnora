package tools

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSkillScriptCommandDoesNotJoinWorkspacePaths(t *testing.T) {
	got := skillScriptCommand(ExecuteSkillScriptInput{
		SkillName:  "ppt-generator",
		ScriptPath: "/workspace/output/generate_rencui_ppt.py",
		Args:       []string{"--flag"},
	})
	require.Equal(t, "/workspace/output/generate_rencui_ppt.py --flag", got)
	assert.NotContains(t, got, "ppt-generator/")
}

func TestSkillScriptCommandKeepsRelativeSkillPaths(t *testing.T) {
	got := skillScriptCommand(ExecuteSkillScriptInput{
		SkillName:  "pdf",
		ScriptPath: "scripts/extract.py",
	})
	require.Equal(t, "pdf/scripts/extract.py", got)
}

func TestExecuteSkillScriptDescriptionAcceptsWorkspacePaths(t *testing.T) {
	assert.Contains(t, executeSkillScriptTool.Description(), "write_sandbox_file")
	assert.Contains(t, executeSkillScriptTool.Description(), "edit_sandbox_file")
	assert.Contains(t, executeSkillScriptTool.Description(), "/workspace/output")
	assert.Contains(t, executeSkillScriptTool.Description(), "/workspace/input")
	assert.Contains(t, executeSkillScriptTool.Description(), "install_deps.py")
	assert.Contains(t, executeSkillScriptTool.Description(), ".skill-packages")
}

// TestExecuteSkillScriptInputUnmarshalJSON covers the model-emitted shapes the
// UnmarshalJSON fallback must tolerate. The provider does not always honor the
// []string schema: it sometimes emits a stringified JSON array or a single
// command-line string. Each must round-trip to the intended argv.
func TestExecuteSkillScriptInputUnmarshalJSON(t *testing.T) {
	t.Run("real array", func(t *testing.T) {
		var in ExecuteSkillScriptInput
		require.NoError(t, json.Unmarshal([]byte(`{
			"skill_name": "s", "script_path": "p",
			"args": ["--project-name", "X", "--creator", "admin"]
		}`), &in))
		require.Equal(t, []string{"--project-name", "X", "--creator", "admin"}, in.Args)
	})

	t.Run("stringified json array", func(t *testing.T) {
		// Some providers emit args as a JSON string whose content is itself a
		// JSON array. strings.Fields would mangle the brackets/quotes into
		// one garbage token; the array must be recovered instead.
		var in ExecuteSkillScriptInput
		require.NoError(t, json.Unmarshal([]byte(`{
			"skill_name": "s", "script_path": "p",
			"args": "[\"--project-name\", \"X\", \"--creator\", \"admin\"]"
		}`), &in))
		require.Equal(t, []string{"--project-name", "X", "--creator", "admin"}, in.Args)
	})

	t.Run("single command line string", func(t *testing.T) {
		var in ExecuteSkillScriptInput
		require.NoError(t, json.Unmarshal([]byte(`{
			"skill_name": "s", "script_path": "p",
			"args": "ls --workspace /workspace/output"
		}`), &in))
		require.Equal(t, []string{"ls", "--workspace", "/workspace/output"}, in.Args)
	})

	t.Run("absent args", func(t *testing.T) {
		var in ExecuteSkillScriptInput
		require.NoError(t, json.Unmarshal([]byte(`{
			"skill_name": "s", "script_path": "p"
		}`), &in))
		require.Empty(t, in.Args)
	})

	t.Run("null args", func(t *testing.T) {
		var in ExecuteSkillScriptInput
		require.NoError(t, json.Unmarshal([]byte(`{
			"skill_name": "s", "script_path": "p", "args": null
		}`), &in))
		require.Empty(t, in.Args)
	})
}
