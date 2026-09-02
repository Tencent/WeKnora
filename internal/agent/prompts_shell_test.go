package agent

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/skills"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFormatSkillsMetadataIncludesShellGuidanceOnlyWhenEnabled(t *testing.T) {
	metadata := []*skills.SkillMetadata{{Name: "demo", Description: "demo skill"}}

	enabled := formatSkillsMetadata(metadata, true)
	require.Contains(t, enabled, "shell_exec")
	assert.Contains(t, enabled, "Freely execute shell commands")

	// Cross-tool routing is what this section is for, so the skill-environment
	// rules stay: which tool runs a script, and where an on-demand package goes.
	assert.Contains(t, enabled, "execute_skill_script")
	assert.Contains(t, enabled, "/workspace/...")
	assert.Contains(t, enabled, "do not `list_sandbox_files`")
	assert.Contains(t, enabled, "read_skill(skill_name, file_path)")
	assert.Contains(t, enabled, ".skill-packages")
	assert.Contains(t, enabled, "install_deps.py")

	// How to drive one tool belongs to that tool's description, which ships with
	// every request anyway. Repeating it here costs the tokens twice and lets
	// the two copies drift; TestShellExecDescriptionOwnsItsMechanics covers the
	// other half of this split.
	for _, mechanic := range []string{
		"never nest ASCII",
		"do not prefix `cd /workspace &&`",
		"Binary output is suppressed",
		"use `file` for an unknown type",
		"Increase `max_output_bytes`",
		"Non-zero exit codes are normal",
	} {
		assert.NotContains(t, enabled, mechanic)
	}

	disabled := formatSkillsMetadata(metadata, false)
	assert.NotContains(t, disabled, "shell_exec")
	// The workspace layout describes where skill scripts read and write, so it
	// stays even when the agent has no shell of its own.
	assert.Contains(t, disabled, "whose working directory is `/workspace`")
	assert.Contains(t, disabled, "/workspace/output")
}
