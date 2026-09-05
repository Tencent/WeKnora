package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFrozenSkillGuidanceUsesTheUnifiedExecutor(t *testing.T) {
	hint := frozenSkillTreeGuidance("律师助手")
	require.Contains(t, hint, "/workspace/.skill-packages/律师助手")
	require.Contains(t, hint, "python3 -m pip install --target")
	require.Contains(t, hint, "shell_exec")
	require.NotContains(t, hint, "execute_skill_script")
}

func TestIsMissingInterpreterModuleIgnoresMissingPip(t *testing.T) {
	t.Parallel()

	assert.False(t, isMissingInterpreterModule("No module named pip"))
	assert.True(t, isMissingInterpreterModule("ModuleNotFoundError: No module named 'docx'\n"))
}
