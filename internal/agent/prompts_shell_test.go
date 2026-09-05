package agent

import (
	"github.com/Tencent/WeKnora/internal/agent/skills"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestToolGuidanceUsesActualCapabilities(t *testing.T) {
	metadata := []*skills.SkillMetadata{{Name: "demo", Description: "demo skill"}}
	text := formatSkillsMetadata(metadata, true)
	require.Contains(t, text, "read_file")
	require.NotContains(t, text, "execute_skill_script")
	require.NotContains(t, text, "MANDATORY")
	shell := formatToolGuidance([]string{"shell_exec", "read_file", "write_sandbox_file", "edit_sandbox_file"})
	require.Contains(t, shell, "shell_exec(skill_name=")
	require.Contains(t, shell, "/workspace/output")
	require.Contains(t, shell, "sandbox:<file name>")
	require.Contains(t, shell, "translate execute_skill_script")
	require.NotContains(t, formatToolGuidance([]string{"knowledge_search"}), "/workspace")
	require.NotContains(t, formatToolGuidance([]string{"read_file"}), "shell_exec")
	require.NotContains(t, formatToolGuidance([]string{"read_file"}), "execute_skill_script")
	require.Empty(t, formatToolGuidance(nil))
	require.NotContains(t, formatToolGuidance([]string{"execute_skill_script"}), "execute_skill_script is available")
}
