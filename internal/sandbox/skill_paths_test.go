package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSkillDirFor(t *testing.T) {
	require.Equal(t, "/opt/weknora/tenant/skills/sk-1", SkillDirFor("sk-1"))
}

func TestSkillDirForImageScript(t *testing.T) {
	t.Run("flat script path resolves to skill directory", func(t *testing.T) {
		skillDir, ok := SkillDirForImageScript(SkillsImageRoot + "/sk-1/run.py")
		require.True(t, ok)
		require.Equal(t, SkillsImageRoot+"/sk-1", skillDir)
	})

	t.Run("nested script path resolves to skill directory", func(t *testing.T) {
		skillDir, ok := SkillDirForImageScript(SkillsImageRoot + "/sk-1/scripts/tools/run.py")
		require.True(t, ok)
		require.Equal(t, SkillsImageRoot+"/sk-1", skillDir)
	})

	t.Run("path outside image skill root is rejected", func(t *testing.T) {
		skillDir, ok := SkillDirForImageScript("/workspace/run.py")
		require.False(t, ok)
		require.Empty(t, skillDir)
	})
}

func TestSkillInterpreterCommand(t *testing.T) {
	dir := SkillDirFor("sk-1")

	t.Run("python prefers the skill's own venv", func(t *testing.T) {
		cmd, args := SkillInterpreterCommand(dir, dir+"/scripts/run.py")
		require.Equal(t, "/bin/sh", cmd)
		require.Len(t, args, 3)
		require.Equal(t, "-c", args[0])
		require.Contains(t, args[1], dir+"/.venv/bin/python",
			"a skill with its own venv must not be run by the system interpreter")
		require.Contains(t, args[1], "else", "there must be a fallback when the venv is absent")
		require.Equal(t, "weknora-skill", args[2])
	})

	t.Run("javascript uses node", func(t *testing.T) {
		cmd, args := SkillInterpreterCommand(dir, dir+"/scripts/run.js")
		require.Equal(t, "node", cmd)
		require.Equal(t, []string{dir + "/scripts/run.js"}, args)
	})

	t.Run("shell scripts run with sh", func(t *testing.T) {
		cmd, args := SkillInterpreterCommand(dir, dir+"/scripts/run.sh")
		require.Equal(t, "/bin/sh", cmd)
		require.Equal(t, []string{dir + "/scripts/run.sh"}, args)
	})

	t.Run("unknown extension falls back to sh", func(t *testing.T) {
		cmd, args := SkillInterpreterCommand(dir, dir+"/scripts/run")
		require.Equal(t, "/bin/sh", cmd)
		require.Equal(t, []string{dir + "/scripts/run"}, args)
	})
}

func TestSkillInterpreterCommandPythonForwardsAllCallerArgs(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skipf("shell is not available: %v", err)
	}

	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "run.py")
	require.NoError(t, os.WriteFile(scriptPath, []byte(`import sys
print("\n".join(sys.argv[1:]))
`), 0o644))

	cmd, baseArgs := SkillInterpreterCommand(dir, scriptPath)
	require.Equal(t, "/bin/sh", cmd)

	args := append(append([]string{}, baseArgs...), "--first", "value", "--third")
	output, err := exec.Command(cmd, args...).CombinedOutput()
	require.NoError(t, err, string(output))
	require.Equal(t, "--first\nvalue\n--third\n", string(output))
}
