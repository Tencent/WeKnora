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
	for _, command := range []string{"find", "file", "cat", "head", "tail", "sed", "grep", "awk"} {
		assert.Contains(t, enabled, command)
	}
	assert.Contains(t, enabled, "Freely execute shell commands")
	assert.Contains(t, enabled, "Binary output is suppressed")

	disabled := formatSkillsMetadata(metadata, false)
	assert.NotContains(t, disabled, "shell_exec")
}
