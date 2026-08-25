package sandbox

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestExecutionOutputDir(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  *ExecuteConfig
		want string
	}{
		{
			name: "default when env missing",
			cfg:  &ExecuteConfig{},
			want: SessionOutputRoot,
		},
		{
			name: "uses env override under workspace",
			cfg: &ExecuteConfig{
				Env: map[string]string{
					skillOutputEnvVar: "/workspace/custom-output",
				},
			},
			want: "/workspace/custom-output",
		},
		{
			name: "rejects path outside workspace",
			cfg: &ExecuteConfig{
				Env: map[string]string{
					skillOutputEnvVar: "/tmp/weknora-skill-output",
				},
			},
			want: SessionOutputRoot,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, executionOutputDir(tt.cfg))
		})
	}
}

func TestSessionBoundManagerExecuteEnsuresOutputDir(t *testing.T) {
	client := newFakeRemoteClient(SandboxTypeCube)
	checker := &fakeSessionExistenceChecker{exists: true}
	// DefaultConfig carries no Cube template on purpose; the deployment baseline
	// or the named config supplies it.
	cfg := DefaultConfig()
	cfg.CubeTemplate = "tpl-test"
	mgr, err := NewSessionBoundManager(SessionBoundManagerConfig{
		Config:          cfg,
		Client:          client,
		Store:           NewMemorySessionSandboxBindingStore(),
		Checker:         checker,
		SkipHealthProbe: true,
	})
	require.NoError(t, err)

	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10000))
	_, err = mgr.Execute(ctx, &ExecuteConfig{
		SessionID:      "session-a",
		SkipValidation: true,
		ScriptContent:  "print('ok')\n",
		Script:         "hello.py",
		Env: map[string]string{
			skillOutputEnvVar: SessionOutputRoot,
		},
	})
	require.NoError(t, err)

	client.mu.Lock()
	paths := append([]string(nil), client.makeDirPaths...)
	execs := append([]RemoteExecRequest(nil), client.execRequests...)
	client.mu.Unlock()
	require.Contains(t, paths, SessionOutputRoot)
	require.NotEmpty(t, execs)
	require.True(t, execs[0].Shell)
	require.Contains(t, execs[0].Command, "chown user:user")
	require.Contains(t, execs[0].Command, SessionOutputRoot)
	require.Equal(t, DefaultSandboxExecUser, execs[0].User,
		"chown follows symlinks, so a root-run bootstrap can be aimed at /etc by "+
			"a session that swaps its artifact directory for a link; running as the "+
			"sandbox account is what makes that attempt fail")
}

// shell_exec carries a command line the model wrote, which makes it the exec
// path an injected prompt reaches most directly. The account it runs as is
// pinned here rather than left to each adapter, so that reading this call site
// answers "as whom does model-authored input run" without having to trust that
// all three adapters agree on what a blank user means.
func TestSessionBoundManagerShellExecRunsAsSandboxUser(t *testing.T) {
	client := newFakeRemoteClient(SandboxTypeCube)
	cfg := DefaultConfig()
	cfg.CubeTemplate = "tpl-test"
	mgr, err := NewSessionBoundManager(SessionBoundManagerConfig{
		Config:          cfg,
		Client:          client,
		Store:           NewMemorySessionSandboxBindingStore(),
		Checker:         &fakeSessionExistenceChecker{exists: true},
		SkipHealthProbe: true,
	})
	require.NoError(t, err)

	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10000))
	_, err = mgr.ExecShellCommand(
		ctx, "session-shell", "id -un", SessionWorkspaceRoot, time.Minute, nil,
	)
	require.NoError(t, err)

	client.mu.Lock()
	execs := append([]RemoteExecRequest(nil), client.execRequests...)
	client.mu.Unlock()

	var shell []RemoteExecRequest
	for _, req := range execs {
		if req.Shell && req.Command == "id -un" {
			shell = append(shell, req)
		}
	}
	require.Len(t, shell, 1)
	require.Equal(t, DefaultSandboxExecUser, shell[0].User)
}

func TestCleanSessionWorkDirRejectsSkillRootByDefault(t *testing.T) {
	skillDir := mustSkillDir(t, "sk-1")
	_, err := cleanSessionWorkDir(skillDir, false)
	require.Error(t, err, "ordinary sessions must stay inside /workspace")

	got, err := cleanSessionWorkDir(skillDir, true)
	require.NoError(t, err, "install sessions need to work inside the skills root")
	require.Equal(t, skillDir, got)
}

func TestCleanSessionWorkDirStillRejectsArbitraryPathsInInstallMode(t *testing.T) {
	_, err := cleanSessionWorkDir("/etc", true)
	require.Error(t, err, "install mode widens the allowlist, it does not remove it")
}

func TestExecShellCommandWithOptionsRunsAsRootOnlyWhenAsked(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10000))
	mgr, client := newSessionManagerExecTestHarness(t)

	_, err := mgr.ExecShellCommandWithOptions(ctx, "sess-1", "echo hi", ShellExecOptions{})
	require.NoError(t, err)
	last := lastExecRequest(t, client)
	require.Equal(t, DefaultSandboxExecUser, last.User,
		"ordinary shell_exec must stay on the non-root sandbox account")

	skillDir := mustSkillDir(t, "sk-1")
	_, err = mgr.ExecShellCommandWithOptions(ctx, "sess-1", "echo hi", ShellExecOptions{
		AsRoot:          true,
		AllowSkillsRoot: true,
		WorkDir:         skillDir,
	})
	require.NoError(t, err)
	last = lastExecRequest(t, client)
	require.Equal(t, "root", last.User)
	require.Equal(t, skillDir, last.WorkDir)
}

func TestExecShellCommandKeepsOrdinaryRemoteRequest(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10000))
	mgr, client := newSessionManagerExecTestHarness(t)
	env := map[string]string{"A": "B"}

	_, err := mgr.ExecShellCommand(ctx, "sess-1", "echo hi", "/workspace/project", time.Second, env)
	require.NoError(t, err)

	last := lastExecRequest(t, client)
	require.Equal(t, RemoteExecRequest{
		Command: "echo hi",
		Shell:   true,
		Env:     env,
		WorkDir: "/workspace/project",
		User:    DefaultSandboxExecUser,
		Timeout: time.Second,
	}, last)
}

func TestExecShellCommandEmptyWorkDirLeavesRemoteRequestUnset(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10000))
	mgr, client := newSessionManagerExecTestHarness(t)

	_, err := mgr.ExecShellCommand(ctx, "sess-1", "echo hi", "", time.Second, nil)
	require.NoError(t, err)

	last := lastExecRequest(t, client)
	require.Empty(t, last.WorkDir)
	require.Equal(t, DefaultSandboxExecUser, last.User)
}

func TestExecShellCommandRejectsInvalidWorkDir(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10000))
	mgr, _ := newSessionManagerExecTestHarness(t)

	_, err := mgr.ExecShellCommand(ctx, "sess-1", "echo hi", "/etc", time.Second, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "outside allowed roots")
	require.Contains(t, err.Error(), SessionWorkspaceRoot)
}

// The manager is what the skill install flow holds, so the path from "the
// image changed" to "this session runs on a new sandbox" has to work through
// it, not only through the lifecycle it wraps.
func TestSessionBoundManagerInvalidateConfigSandboxesRebuildsOnNextUse(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10000))
	client := newFakeRemoteClient(SandboxTypeCube)
	store := NewMemorySessionSandboxBindingStore()
	cfg := DefaultConfig()
	cfg.CubeTemplate = "tpl-test"
	mgr, err := NewSessionBoundManager(SessionBoundManagerConfig{
		Config:          cfg,
		Client:          client,
		Store:           store,
		Checker:         &fakeSessionExistenceChecker{exists: true},
		SkipHealthProbe: true,
		ConfigID:        "cfg-1",
	})
	require.NoError(t, err)

	_, err = mgr.ExecShellCommand(ctx, "sess-1", "echo hi", "", time.Second, nil)
	require.NoError(t, err)
	before, err := store.Get(ctx, SessionSandboxKey{TenantID: 10000, SessionID: "sess-1"})
	require.NoError(t, err)
	require.NotNil(t, before)

	marked, err := mgr.InvalidateConfigSandboxes(ctx, 10000, "cfg-1")
	require.NoError(t, err)
	require.Equal(t, 1, marked)

	_, err = mgr.ExecShellCommand(ctx, "sess-1", "echo hi", "", time.Second, nil)
	require.NoError(t, err)
	after, err := store.Get(ctx, SessionSandboxKey{TenantID: 10000, SessionID: "sess-1"})
	require.NoError(t, err)
	require.NotEqual(t, before.SandboxID, after.SandboxID)
	require.False(t, client.hasSandbox(before.SandboxID),
		"the sandbox on the old image must be released, not left billing")
}

func TestSessionBoundManagerEndSessionTurnIgnoresCancel(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10000))
	store := NewMemorySessionSandboxBindingStore()
	cfg := DefaultConfig()
	cfg.CubeTemplate = "tpl-test"
	mgr, err := NewSessionBoundManager(SessionBoundManagerConfig{
		Config:          cfg,
		Client:          newFakeRemoteClient(SandboxTypeCube),
		Store:           store,
		Checker:         &fakeSessionExistenceChecker{exists: true},
		SkipHealthProbe: true,
	})
	require.NoError(t, err)

	cancelled, cancel := context.WithCancel(ctx)
	require.NoError(t, mgr.BeginSessionTurn(cancelled, "sess-1"))
	cancel()
	require.NoError(t, mgr.EndSessionTurn(cancelled, "sess-1"))

	active, _, err := store.TurnState(ctx, SessionSandboxKey{TenantID: 10000, SessionID: "sess-1"})
	require.NoError(t, err)
	require.False(t, active)
}

func TestWriteSessionFileSucceedsWhenInstallDirectoryAlreadyExists(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10000))
	mgr, client := newSessionManagerExecTestHarness(t)
	client.failMakeDirIfExists = true

	skillDir, err := SkillDirFor("0d3390ab-6fba-4c8f-8571-30076da51010")
	require.NoError(t, err)
	require.NoError(t, client.MakeDir(ctx, nil, skillDir),
		"resetSkillDir has already created this directory via mkdir -p")

	require.NoError(t,
		mgr.WriteSessionFile(ctx, "sess-1", skillDir+"/SKILL.md", []byte("---\nname: pptx\n")),
		"seeding SKILL.md must not fail just because the skill dir exists")

	client.mu.Lock()
	writes := append([]fakeRemoteWriteFile(nil), client.writeFiles...)
	client.mu.Unlock()
	require.Len(t, writes, 1)
	require.Equal(t, skillDir+"/SKILL.md", writes[0].path)
}

func newSessionManagerExecTestHarness(t *testing.T) (*SessionBoundManager, *fakeRemoteClient) {
	t.Helper()

	client := newFakeRemoteClient(SandboxTypeCube)
	cfg := DefaultConfig()
	cfg.CubeTemplate = "tpl-test"
	mgr, err := NewSessionBoundManager(SessionBoundManagerConfig{
		Config:          cfg,
		Client:          client,
		Store:           NewMemorySessionSandboxBindingStore(),
		Checker:         &fakeSessionExistenceChecker{exists: true},
		SkipHealthProbe: true,
	})
	require.NoError(t, err)
	return mgr, client
}

func lastExecRequest(t *testing.T, client *fakeRemoteClient) RemoteExecRequest {
	t.Helper()

	client.mu.Lock()
	defer client.mu.Unlock()
	require.NotEmpty(t, client.execRequests)
	return client.execRequests[len(client.execRequests)-1]
}
