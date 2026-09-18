package agent

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/skills"
	"github.com/stretchr/testify/require"
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
	require.Contains(t, shell, "Copy the exact links supplied in the tool result's appended Output files list")
	require.Contains(t, shell, "including /tmp/task/previews, are internal working files")
	require.Contains(t, shell, "Rendering pages for your own layout checks does not publish them")
	require.Contains(t, shell, "translate execute_skill_script")
	require.NotContains(t, formatToolGuidance([]string{"knowledge_search"}), "/workspace")
	require.NotContains(t, formatToolGuidance([]string{"read_file"}), "shell_exec")
	require.NotContains(t, formatToolGuidance([]string{"read_file"}), "execute_skill_script")
	require.Empty(t, formatToolGuidance(nil))
	require.NotContains(t, formatToolGuidance([]string{"execute_skill_script"}), "execute_skill_script is available")
	require.NotContains(t, shell, "Browser source:")
	require.Contains(t, formatToolGuidance([]string{"local_browser"}), "requires no shell command")
}

func TestArtifactGuidanceUsesConfiguredOutputDirectory(t *testing.T) {
	t.Setenv("WEKNORA_SKILL_OUTPUT_DIR", "/workspace/deliverables")
	guidance := formatToolGuidance([]string{"shell_exec", "read_file"})
	require.Contains(t, guidance, "/workspace/deliverables is the only directory collected for download")
	require.NotContains(t, guidance, "/workspace/output is the only directory collected")
}

// A skill fetched in the sandbox is not installed; the guidance says so, and
// sends install requests to the card when search_skills is there.
func TestSkillInstallGuidanceFollowsTheInstallCard(t *testing.T) {
	withCard := formatToolGuidance([]string{"shell_exec", "search_skills"})
	require.Contains(t, withCard, "only loads it for this session")
	require.Contains(t, withCard, "call search_skills with its source")
	require.Contains(t, withCard, "say it is temporary")

	withoutCard := formatToolGuidance([]string{"shell_exec"})
	require.Contains(t, withoutCard, "only loads it for this session")
	require.Contains(t, withoutCard, "skill settings")
	require.NotContains(t, withoutCard, "search_skills")

	require.NotContains(t, formatToolGuidanceForMode([]string{"shell_exec"}, true), "only loads it for this session",
		"the installer agent is the one path that does install")
	require.NotContains(t, formatToolGuidance([]string{"read_file"}), "search_skills")
}
