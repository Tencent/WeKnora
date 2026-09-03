package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSkillOnDemandInstallHintSkipInstallerOnFrozenVenv(t *testing.T) {
	t.Parallel()

	hint := skillOnDemandInstallHint(
		"律师助手",
		"scripts/install_deps.py",
		"word 依赖: python-docx==1.1.2\nX word 安装失败 (exit 1)\n",
		"/opt/weknora/tenant/skills/律师助手/.venv/bin/python: No module named pip\n",
	)
	require.NotEmpty(t, hint)
	assert.Contains(t, hint, "/workspace/.skill-packages/律师助手")
	assert.Contains(t, hint, "python3 -m pip install --target")
	assert.Contains(t, hint, "Skip this installer")
}

func TestSkillMissingPackageHint(t *testing.T) {
	t.Parallel()

	hint := skillMissingPackageHint("律师助手", "ModuleNotFoundError: No module named 'docx'\n")
	require.NotEmpty(t, hint)
	assert.Contains(t, hint, "frozen venv")
	assert.Contains(t, hint, "/workspace/.skill-packages/律师助手")
}

func TestIsMissingInterpreterModuleIgnoresMissingPip(t *testing.T) {
	t.Parallel()

	assert.False(t, isMissingInterpreterModule("No module named pip"))
	assert.True(t, isMissingInterpreterModule("ModuleNotFoundError: No module named 'docx'\n"))
}
