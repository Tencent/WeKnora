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
