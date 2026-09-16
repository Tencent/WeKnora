package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMissingSkillPackageGuidanceUsesTheUnifiedExecutor(t *testing.T) {
	hint := missingSkillPackageGuidance("律师助手")
	require.Contains(t, hint, skillPythonPackageInstallCommand)
	require.Contains(t, hint, "shell_exec")
	require.NotContains(t, hint, "execute_skill_script")
	require.NotContains(t, hint, "/workspace/.skill-packages",
		"the session overlay is gone; a package belongs in the skill's own environment")
}

// An unnamed hint asks for a named call; every command also refuses an unset
// or empty skill directory before invoking a package manager.
func TestMissingSkillPackageGuidanceWithoutASkillNameStaysConcrete(t *testing.T) {
	hint := missingSkillPackageGuidance("")
	require.Contains(t, hint, skillPythonVenvCreateCommand)
	require.Contains(t, hint, "skill_name=<skill>")
}

func TestIsMissingInterpreterModuleIgnoresMissingPip(t *testing.T) {
	t.Parallel()

	assert.False(t, isMissingInterpreterModule("No module named pip"))
	assert.True(t, isMissingInterpreterModule("ModuleNotFoundError: No module named 'docx'\n"))
}

// Run the actual commands advertised in recovery guidance against a local
// wheel. No network or installed pip is needed for the uv path.
func TestSkillPythonPackageRecoveryInstallsWithoutPip(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is required")
	}
	for _, variant := range []struct{ name, command string }{
		{"uv", skillPythonPackageInstallCommand},
		{"ensurepip", skillPythonPackageFallbackCommand},
	} {
		t.Run(variant.name, func(t *testing.T) {
			if variant.name == "uv" {
				if _, err := exec.LookPath("uv"); err != nil {
					t.Skip("uv is required")
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			dir := t.TempDir()
			skillDir := filepath.Join(dir, "staged skill 中文")
			require.NoError(t, os.MkdirAll(skillDir, 0o755))
			env := append(os.Environ(), "WEKNORA_SKILL_DIR="+skillDir,
				"UV_OFFLINE=1", "UV_CACHE_DIR="+filepath.Join(dir, "uv-cache"),
				"PIP_NO_INDEX=1", "PIP_DISABLE_PIP_VERSION_CHECK=1")
			run := func(command string) string {
				t.Helper()
				cmd := exec.CommandContext(ctx, "/bin/bash", "--noprofile", "--norc", "-c", command)
				cmd.Dir, cmd.Env = dir, env
				out, err := cmd.CombinedOutput()
				require.NoError(t, err, "%s: %s", command, out)
				return string(out)
			}
			run(skillPythonVenvCreateCommand)
			python := filepath.Join(skillDir, ".venv", "bin", "python")
			cmd := exec.CommandContext(ctx, python, "-m", "pip", "--version")
			out, err := cmd.CombinedOutput()
			require.Error(t, err)
			require.Contains(t, string(out), "No module named pip")
			build := exec.CommandContext(ctx, "python3", "-c", `import zipfile
files = {
    "recovery_probe.py": "VALUE = 42\n",
    "recovery_probe-1.0.dist-info/METADATA": "Metadata-Version: 2.1\nName: recovery-probe\nVersion: 1.0\n",
    "recovery_probe-1.0.dist-info/WHEEL": ("Wheel-Version: 1.0\nGenerator: test\n"
                                           "Root-Is-Purelib: true\nTag: py3-none-any\n"),
}
record = "recovery_probe-1.0.dist-info/RECORD"
files[record] = "".join(name + ",,\n" for name in [*files, record])
with zipfile.ZipFile("recovery_probe-1.0-py3-none-any.whl", "w") as wheel:
    for name, content in files.items():
        wheel.writestr(name, content)
`)
			build.Dir = dir
			out, err = build.CombinedOutput()
			require.NoError(t, err, "%s", out)
			run(strings.ReplaceAll(variant.command, "<package>", "./recovery_probe-1.0-py3-none-any.whl"))
			cmd = exec.CommandContext(ctx, python, "-c", "import recovery_probe; print(recovery_probe.VALUE)")
			out, err = cmd.CombinedOutput()
			require.NoError(t, err, "%s", out)
			require.Equal(t, "42\n", string(out))
		})
	}
}

func TestSkillPackageCommandsRefuseMissingDirectory(t *testing.T) {
	dir := t.TempDir()
	for _, tool := range []string{"uv", "npm", "python3"} {
		script := []byte("#!/bin/sh\necho tool-invoked\n")
		require.NoError(t, os.WriteFile(filepath.Join(dir, tool), script, 0o755))
	}
	for _, command := range []string{
		skillPythonPackageInstallCommand, skillPythonPackageFallbackCommand,
		skillPythonVenvCreateCommand, skillNodePackageInstallCommand,
	} {
		for _, empty := range []bool{false, true} {
			cmd := exec.Command("/bin/bash", "--noprofile", "--norc", "-c",
				strings.ReplaceAll(command, "<package>", "test-package"))
			cmd.Env = []string{"PATH=" + dir}
			if empty {
				cmd.Env = append(cmd.Env, "WEKNORA_SKILL_DIR=")
			}
			out, err := cmd.CombinedOutput()
			require.Error(t, err, "%s", command)
			require.Contains(t, string(out), "WEKNORA_SKILL_DIR")
			require.NotContains(t, string(out), "tool-invoked",
				"no package manager may run without a skill directory")
		}
	}
}
