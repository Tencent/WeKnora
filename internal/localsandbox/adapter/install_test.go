package adapter

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/internal/localsandbox"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/stretchr/testify/require"
)

type recordingInstallRunner struct {
	policy localsandbox.Policy
	req    localsandbox.RunRequest
}

func (r *recordingInstallRunner) RunWithPolicy(
	_ context.Context, p localsandbox.Policy, req localsandbox.RunRequest,
) (*localsandbox.RunResult, error) {
	r.policy, r.req = p, req
	return &localsandbox.RunResult{Stdout: "ok"}, nil
}

type dirInstallPolicies struct{}

func (dirInstallPolicies) BuildInstall(dir string) (localsandbox.Policy, error) {
	return localsandbox.Policy{
		Cwd:           dir,
		WritableRoots: []localsandbox.WritableRoot{{Path: dir}},
		Network:       localsandbox.NetworkUnrestricted,
	}, nil
}

func installFixture(t *testing.T) (*InstallManager, *recordingInstallRunner, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "skills", ".versions", "pdf-1")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	runner := &recordingInstallRunner{}
	return NewInstallManager(runner, dirInstallPolicies{}), runner, dir
}

func TestInstallManagerRefusesUnboundSession(t *testing.T) {
	m, _, _ := installFixture(t)
	_, err := m.ExecShellCommandWithOptions(context.Background(), "s1", "ls", sandbox.ShellExecOptions{})
	require.Error(t, err)
	_, err = m.ReadSessionFile(context.Background(), "s1", "SKILL.md")
	require.Error(t, err)
}

func TestInstallManagerRunsInBoundVersionDir(t *testing.T) {
	m, runner, dir := installFixture(t)
	release := m.Bind("s1", dir)
	res, err := m.ExecShellCommandWithOptions(context.Background(), "s1", "uv venv --seed .venv",
		sandbox.ShellExecOptions{WorkDir: sandbox.SessionWorkspaceRoot, AsRoot: true, Env: map[string]string{"X": "1"}})
	require.NoError(t, err)
	require.Equal(t, "ok", res.Stdout)
	require.Equal(t, dir, runner.policy.Cwd)
	require.Empty(t, runner.req.WorkDir, "/workspace means the version dir on host")
	require.Equal(t, filepath.Join(dir, ".weknora", "cache", "uv"), runner.req.Env["UV_CACHE_DIR"])
	require.Equal(t, filepath.Join(dir, ".weknora", "cache", "pip"), runner.req.Env["PIP_CACHE_DIR"])
	require.Equal(t, filepath.Join(dir, ".weknora", "cache", "npm"), runner.req.Env["npm_config_cache"])
	require.Equal(t, dir, runner.req.Env["WEKNORA_SKILL_DIR"])
	require.Equal(t, "1", runner.req.Env["X"])

	release()
	_, err = m.ExecShellCommandWithOptions(context.Background(), "s1", "ls", sandbox.ShellExecOptions{})
	require.Error(t, err)
}

func TestInstallManagerFilesStayInsideVersionDir(t *testing.T) {
	m, _, dir := installFixture(t)
	defer m.Bind("s1", dir)()
	ctx := context.Background()

	require.NoError(t, m.WriteSessionFile(ctx, "s1", "scripts/run.py", []byte("print(1)")))
	got, err := m.ReadSessionFile(ctx, "s1", filepath.Join(dir, "scripts", "run.py"))
	require.NoError(t, err)
	require.Equal(t, "print(1)", string(got))
	st, err := m.StatSessionFile(ctx, "s1", "scripts/run.py")
	require.NoError(t, err)
	require.Equal(t, int64(len("print(1)")), st.Size)

	err = m.WriteSessionFile(ctx, "s1", filepath.Join(filepath.Dir(dir), "other-1", "x"), []byte("x"))
	require.ErrorIs(t, err, localsandbox.ErrPathDenied)
}

func TestInstallManagerIsNotAChatBackend(t *testing.T) {
	var m any = NewInstallManager(&recordingInstallRunner{}, dirInstallPolicies{})
	_, isChat := m.(sandbox.SessionCapabilityProvider)
	require.False(t, isChat, "the installer must never serve chat shell or file tools")
	_, isDestroyer := m.(sandbox.SessionDestroyer)
	require.False(t, isDestroyer)
	require.Equal(t, sandbox.SandboxTypeHost, m.(sandbox.Manager).GetType())
}
