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
	for _, command := range []string{"find", "ls", "cat", "head", "tail", "sed", "grep", "awk"} {
		assert.Contains(t, enabled, command)
	}
	assert.Contains(t, enabled, "Freely execute shell commands")
	assert.Contains(t, enabled, "Binary output is suppressed")
	assert.Contains(t, enabled, "use `file` for an unknown type")
	assert.NotContains(t, enabled, "do not `apt-get install file`")
	assert.Contains(t, enabled, "write_sandbox_file")
	assert.Contains(t, enabled, "edit_sandbox_file")
	assert.Contains(t, enabled, "python3 -c")
	assert.Contains(t, enabled, "execute_skill_script")
	assert.Contains(t, enabled, "/workspace/...")
	assert.Contains(t, enabled, "do not `list_sandbox_files`")
	assert.Contains(t, enabled, "read_skill(skill_name, file_path)")
	assert.Contains(t, enabled, ".skill-packages")
	assert.Contains(t, enabled, "install_deps.py")
	assert.Contains(t, enabled, "never nest ASCII")

	disabled := formatSkillsMetadata(metadata, false)
	assert.NotContains(t, disabled, "shell_exec")
}
