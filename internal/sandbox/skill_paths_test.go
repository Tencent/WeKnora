package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSkillDirFor(t *testing.T) {
	dir, err := SkillDirFor("sk-1")
	require.NoError(t, err)
	require.Equal(t, "/opt/weknora/tenant/skills/sk-1", dir)
}

func TestSessionSkillPackageDir(t *testing.T) {
	require.Equal(t, "/workspace/.skill-packages/律师助手", SessionSkillPackageDir("律师助手"))
	require.Equal(t, "/workspace/.skill-packages", SessionSkillPackageDir("../escape"))
}

func TestSkillDirForRejectsPathEscape(t *testing.T) {
	for _, name := range []string{"", ".", "..", "../x", "foo/bar", `foo\bar`, "foo/../bar"} {
		_, err := SkillDirFor(name)
		require.ErrorIs(t, err, ErrInvalidSkillName, "name %q must not resolve under the skills root", name)
	}
}

func TestSkillNameFromImagePath(t *testing.T) {
	name, ok := SkillNameFromImagePath(SkillsImageRoot)
	require.True(t, ok)
	require.Empty(t, name)

	name, ok = SkillNameFromImagePath(SkillsImageRoot + "/ppt-generator")
	require.True(t, ok)
	require.Equal(t, "ppt-generator", name)

	name, ok = SkillNameFromImagePath(SkillsImageRoot + "/ppt-generator/scripts/generate_ppt.py")
	require.True(t, ok)
	require.Equal(t, "ppt-generator", name)

	_, ok = SkillNameFromImagePath("/workspace/output/x.py")
	require.False(t, ok)

	_, ok = SkillNameFromImagePath("/etc/passwd")
	require.False(t, ok)
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

	t.Run("skill directory itself is not an image script", func(t *testing.T) {
		skillDir, ok := SkillDirForImageScript(SkillsImageRoot + "/sk-1")
		require.False(t, ok)
		require.Empty(t, skillDir)
	})

	t.Run("dot-dot after clean that leaves the skills root is rejected", func(t *testing.T) {
		skillDir, ok := SkillDirForImageScript(SkillsImageRoot + "/../workspace/run.py")
		require.False(t, ok)
		require.Empty(t, skillDir)
	})
}

func TestRunnableWorkspaceScript(t *testing.T) {
	okPath, ok := RunnableWorkspaceScript("/workspace/output/generate_ppt.py")
	require.True(t, ok)
	require.Equal(t, "/workspace/output/generate_ppt.py", okPath)

	scratch, ok := RunnableWorkspaceScript("/workspace/scratch.py")
	require.True(t, ok)
	require.Equal(t, "/workspace/scratch.py", scratch)

	for _, p := range []string{
		"/workspace",
		"/workspace/output",
		"/workspace/input",
		"/workspace/input/upload.py",
		"/opt/weknora/tenant/skills/pdf/scripts/run.py",
		"/etc/passwd",
		"",
	} {
		_, ok := RunnableWorkspaceScript(p)
		require.False(t, ok, "path %q must not be a runnable workspace script", p)
	}
}

func TestValidatedImageSkillDir(t *testing.T) {
	dir, ok := ValidatedImageSkillDir(SkillsImageRoot + "/pdf")
	require.True(t, ok)
	require.Equal(t, SkillsImageRoot+"/pdf", dir)

	for _, p := range []string{
		SkillsImageRoot,
		SkillsImageRoot + "/pdf/scripts",
		"/workspace/output",
		"/opt/weknora/tenant/skills/../skills/pdf/x",
		"",
	} {
		_, ok := ValidatedImageSkillDir(p)
		require.False(t, ok, "dir %q must not validate as an image skill directory", p)
	}
}

func TestInterpreterSkillDir(t *testing.T) {
	imageDir, ok := InterpreterSkillDir(SkillsImageRoot+"/pdf/scripts/run.py", SkillsImageRoot+"/other")
	require.True(t, ok)
	require.Equal(t, SkillsImageRoot+"/pdf", imageDir,
		"an image script must derive SkillDir from the path, not the caller field")

	workspaceDir, ok := InterpreterSkillDir("/workspace/output/foo.py", SkillsImageRoot+"/pdf")
	require.True(t, ok)
	require.Equal(t, SkillsImageRoot+"/pdf", workspaceDir)

	_, ok = InterpreterSkillDir("/workspace/output/foo.py", "")
	require.False(t, ok, "workspace scripts require an explicit skill directory")

	_, ok = InterpreterSkillDir("/workspace/input/x.py", SkillsImageRoot+"/pdf")
	require.False(t, ok)

	_, ok = InterpreterSkillDir("/etc/passwd", SkillsImageRoot+"/pdf")
	require.False(t, ok)
}

func TestSkillInterpreterCommand(t *testing.T) {
	dir := mustSkillDir(t, "sk-1")

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
		for _, name := range []string{"run.js", "run.mjs", "run.cjs"} {
			cmd, args := SkillInterpreterCommand(dir, dir+"/scripts/"+name)
			require.Equal(t, "node", cmd, name)
			require.Equal(t, []string{dir + "/scripts/" + name}, args, name)
		}
	})

	// Skills ship `#!/bin/bash` almost exclusively, and on Debian /bin/sh is
	// dash: an array literal, `function f()`, a C-style for loop and process
	// substitution are syntax errors there. Running these files with sh broke
	// scripts that are perfectly valid, and made the install-time `sh -n` check
	// refuse them on the way in.
	t.Run("shell scripts prefer bash", func(t *testing.T) {
		cmd, args := SkillInterpreterCommand(dir, dir+"/scripts/run.sh")
		require.Equal(t, "/bin/sh", cmd)
		require.Len(t, args, 3)
		require.Equal(t, "-c", args[0])
		require.Contains(t, args[1], "exec bash "+dir+"/scripts/run.sh")
		require.Contains(t, args[1], "else", "there must be a fallback when bash is absent")
		require.Equal(t, "weknora-skill", args[2])
	})

	t.Run("a shell script receives the caller's arguments", func(t *testing.T) {
		if _, err := os.Stat("/bin/sh"); err != nil {
			t.Skipf("shell is not available: %v", err)
		}
		scriptDir := t.TempDir()
		script := filepath.Join(scriptDir, "echo-args.sh")
		require.NoError(t, os.WriteFile(script, []byte("printf '%s\\n' \"$@\"\n"), 0o755))

		cmd, baseArgs := SkillInterpreterCommand(scriptDir, script)
		out, err := exec.Command(cmd, append(append([]string{}, baseArgs...),
			"--first", "value")...).CombinedOutput()
		require.NoError(t, err, string(out))
		require.Equal(t, "--first\nvalue\n", string(out))
	})

	t.Run("unknown extension falls back to sh", func(t *testing.T) {
		cmd, args := SkillInterpreterCommand(dir, dir+"/scripts/run")
		require.Equal(t, "/bin/sh", cmd)
		require.Equal(t, []string{dir + "/scripts/run"}, args)
	})

	t.Run("uppercase python extension still uses the venv", func(t *testing.T) {
		cmd, args := SkillInterpreterCommand(dir, dir+"/scripts/run.PY")
		require.Equal(t, "/bin/sh", cmd)
		require.Contains(t, args[1], dir+"/.venv/bin/python")
	})
}

func mustSkillDir(t *testing.T, name string) string {
	t.Helper()
	dir, err := SkillDirFor(name)
	require.NoError(t, err)
	return dir
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
