package tools

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

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
