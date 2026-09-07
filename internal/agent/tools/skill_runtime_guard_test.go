package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMissingSkillPackageGuidanceUsesTheUnifiedExecutor(t *testing.T) {
	hint := missingSkillPackageGuidance("律师助手")
	require.Contains(t, hint, "/opt/weknora/tenant/skills/律师助手/.venv/bin/python -m pip install")
	require.Contains(t, hint, "shell_exec")
	require.NotContains(t, hint, "execute_skill_script")
	require.NotContains(t, hint, "/workspace/.skill-packages",
		"the session overlay is gone; a package belongs in the skill's own environment")
}

// The unnamed case still has to produce a runnable shape rather than an empty
// path, because the skill name is only recovered from the command when the
// model wrote one.
func TestMissingSkillPackageGuidanceWithoutASkillNameStaysConcrete(t *testing.T) {
	hint := missingSkillPackageGuidance("")
	require.Contains(t, hint, "/opt/weknora/tenant/skills/<skill>/.venv/bin/python")
	require.Contains(t, hint, "skill_name=<skill>")
}

func TestIsMissingInterpreterModuleIgnoresMissingPip(t *testing.T) {
	t.Parallel()

	assert.False(t, isMissingInterpreterModule("No module named pip"))
	assert.True(t, isMissingInterpreterModule("ModuleNotFoundError: No module named 'docx'\n"))
}
