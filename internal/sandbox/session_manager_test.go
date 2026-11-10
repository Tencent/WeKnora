package sandbox

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/sandbox/admission"
	"github.com/Tencent/WeKnora/internal/sandbox/allocation"
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
	execs := append([]RemoteExecRequest(nil), client.execRequests...)
	client.mu.Unlock()
	require.NotEmpty(t, execs)
	require.True(t, execs[0].Shell)
	require.Contains(t, execs[0].Command, SessionOutputRoot)
	require.Contains(t, execs[0].Command, SessionInputRoot,
		"the attachment directory is prepared alongside the artifact one; a "+
			"snapshot-derived image carries neither")
	require.Equal(t, DefaultSandboxExecUser, execs[0].User,
		"the bootstrap names its account like every other caller, so the directories "+
			"it creates belong to whoever the execs that follow will run as")
}

func TestWorkspaceBootstrapPreservesExistingData(t *testing.T) {
	cmd := workspaceBootstrapCommand(SessionInputRoot, SessionOutputRoot)
	require.Contains(t, cmd, "for d in /workspace/input /workspace/output")
	require.Contains(t, cmd, `mkdir -p -- "$d"`)
	require.Contains(t, cmd, `[ -L "$d" ]`)
	for _, destructive := range []string{"mv ", "rm ", "chown ", "chmod "} {
		require.NotContains(t, cmd, destructive)
	}
}

// The agent can delete /workspace/output between turns. Preparing only once
// per process would leave later writes failing until WeKnora restarted.
func TestSessionBoundManagerPreparesWorkspaceOnEveryCall(t *testing.T) {
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
	for i := 0; i < 3; i++ {
		_, err := mgr.ExecShellCommand(ctx, "session-a", "echo hi", "", time.Second, nil)
		require.NoError(t, err)
	}

	client.mu.Lock()
	execs := append([]RemoteExecRequest(nil), client.execRequests...)
	client.mu.Unlock()

	bootstraps := 0
	for _, exec := range execs {
		if strings.Contains(exec.Command, SessionInputRoot) {
			bootstraps++
		}
	}
	require.Equal(t, 3, bootstraps)
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

func TestCleanSessionWorkDirAcceptsSandboxSkillRoot(t *testing.T) {
	skillDir := mustSkillDir(t, "sk-1")
	_, err := cleanSessionWorkDir(skillDir, false)
	require.NoError(t, err, "ordinary sessions may use any directory in their sandbox")

	got, err := cleanSessionWorkDir(skillDir, true)
	require.NoError(t, err, "install sessions need to work inside the skills root")
	require.Equal(t, skillDir, got)
}

func TestCleanSessionWorkDirStillRejectsArbitraryPathsInInstallMode(t *testing.T) {
	_, err := cleanSessionWorkDir("/etc", true)
	require.Error(t, err, "install mode widens the allowlist, it does not remove it")
}

func TestExecShellCommandWithOptionsSelectsMaintenanceBootstrap(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10000))
	mgr, client := newSessionManagerExecTestHarness(t)

	_, err := mgr.ExecShellCommandWithOptions(ctx, "sess-1", "echo hi", ShellExecOptions{})
	require.NoError(t, err)
	last := lastExecRequest(t, client)
	require.Equal(t, DefaultSandboxExecUser, last.User,
		"ordinary shell_exec must stay on the default sandbox account rather than "+
			"taking the install-mode escape")

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
	client.mu.Lock()
	execs := append([]RemoteExecRequest(nil), client.execRequests...)
	client.mu.Unlock()
	require.Len(t, execs, 4, "each command has one bootstrap and one execution")
	ordinaryBootstrap := workspaceBootstrapCommand(SessionInputRoot, SessionOutputRoot, SessionWorkspaceRoot)
	require.Equal(t, ordinaryBootstrap, execs[0].Command)
	require.Equal(t, workspaceBootstrapCommand(skillDir), execs[2].Command)
}

func TestExecShellCommandSkipWorkspacePrepOmitsBootstrap(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10000))
	mgr, client := newSessionManagerExecTestHarness(t)

	_, err := mgr.ExecShellCommandWithOptions(ctx, "sess-1", "echo hi", ShellExecOptions{
		SkipWorkspacePrep: true,
		Timeout:           time.Second,
	})
	require.NoError(t, err)

	client.mu.Lock()
	execs := append([]RemoteExecRequest(nil), client.execRequests...)
	client.mu.Unlock()
	require.Len(t, execs, 1, "desktop-style exec must not mkdir /workspace first")
	require.Equal(t, "echo hi", execs[0].Command)
	require.Equal(t, SessionWorkspaceRoot, execs[0].WorkDir)
	require.Equal(t, DefaultSandboxExecUser, execs[0].User)
}

func TestExecShellCommandSkipWorkspacePrepStillRejectsRelativeWorkDir(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10000))
	mgr, _ := newSessionManagerExecTestHarness(t)

	_, err := mgr.ExecShellCommandWithOptions(ctx, "sess-1", "echo hi", ShellExecOptions{
		SkipWorkspacePrep: true,
		WorkDir:           "relative",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "must be absolute")
}

func TestExecShellCommandWithoutSkipStillBootstraps(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10000))
	mgr, client := newSessionManagerExecTestHarness(t)

	_, err := mgr.ExecShellCommandWithOptions(ctx, "sess-1", "echo hi", ShellExecOptions{})
	require.NoError(t, err)

	client.mu.Lock()
	execs := append([]RemoteExecRequest(nil), client.execRequests...)
	client.mu.Unlock()
	require.Len(t, execs, 2)
	require.Equal(t,
		workspaceBootstrapCommand(SessionInputRoot, SessionOutputRoot, SessionWorkspaceRoot),
		execs[0].Command)
	require.Equal(t, "echo hi", execs[1].Command)
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
		Env: map[string]string{
			"A":                    "B",
			"WEKNORA_SESSION_ID":   "sess-1",
			"WEKNORA_BROWSER_ROOT": "/var/lib/weknora/browser/session",
		},
		WorkDir: "/workspace/project",
		User:    DefaultSandboxExecUser,
		Timeout: time.Second,
	}, last)
	require.Equal(t, map[string]string{"A": "B"}, env, "caller map must not be mutated")
	require.Equal(t, "/var/lib/weknora/browser/session", last.Env["WEKNORA_BROWSER_ROOT"])
	require.NotEmpty(t, last.Env["WEKNORA_SESSION_ID"])
}

func TestExecShellCommandEmptyWorkDirUsesWorkspace(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10000))
	mgr, client := newSessionManagerExecTestHarness(t)

	_, err := mgr.ExecShellCommand(ctx, "sess-1", "echo hi", "", time.Second, nil)
	require.NoError(t, err)

	last := lastExecRequest(t, client)
	require.Equal(t, SessionWorkspaceRoot, last.WorkDir)
	require.Equal(t, DefaultSandboxExecUser, last.User)
}

func TestExecShellCommandAllowsTemporaryWorkDir(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10000))
	mgr, client := newSessionManagerExecTestHarness(t)

	_, err := mgr.ExecShellCommand(ctx, "sess-1", "echo hi", "/tmp/task", time.Second, nil)
	require.NoError(t, err)
	require.Equal(t, "/tmp/task", lastExecRequest(t, client).WorkDir)
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
	_, end, err := mgr.BeginSessionTurn(cancelled, "sess-1")
	require.NoError(t, err)
	cancel()
	end()

	active, _, err := store.TurnState(ctx, SessionSandboxKey{TenantID: 10000, SessionID: "sess-1"})
	require.NoError(t, err)
	require.False(t, active)
}

func TestCleanSessionWorkspaceWritePathAcceptsWorkspaceAndRefusesInput(t *testing.T) {
	got, err := cleanSessionWorkspaceWritePath("/workspace/output/generate_ppt.py")
	require.NoError(t, err)
	require.Equal(t, "/workspace/output/generate_ppt.py", got)

	got, err = cleanSessionWorkspaceWritePath("/workspace/scratch/gen.py")
	require.NoError(t, err)
	require.Equal(t, "/workspace/scratch/gen.py", got)

	_, err = cleanSessionWorkspaceWritePath("/workspace/input/report.txt")
	require.Error(t, err)
	_, err = cleanSessionWorkspaceWritePath("/workspace/output")
	require.Error(t, err)
	got, err = cleanSessionWorkspaceWritePath("/tmp/task/check.txt")
	require.NoError(t, err)
	require.Equal(t, "/tmp/task/check.txt", got)
	_, err = cleanSessionWorkspaceWritePath("/tmp/../workspace/input/report.txt")
	require.Error(t, err)
	got, err = cleanSessionWorkspaceWritePath("relative.py")
	require.NoError(t, err)
	require.Equal(t, "/workspace/relative.py", got)
}

func TestWriteSessionWorkspaceFileWritesSandboxPaths(t *testing.T) {
	for _, filePath := range []string{"/workspace/output/generate_ppt.py", "/tmp/task/generate_ppt.py"} {
		t.Run(filePath, func(t *testing.T) {
			ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10000))
			mgr, client := newSessionManagerExecTestHarness(t)

			require.NoError(t, mgr.WriteSessionWorkspaceFile(
				ctx, "sess-1", filePath, []byte("print(1)\n"),
			))

			client.mu.Lock()
			writes := append([]fakeRemoteWriteFile(nil), client.writeFiles...)
			execs := len(client.execRequests)
			client.mu.Unlock()
			require.Len(t, writes, 1)
			require.Equal(t, filePath, writes[0].path)
			require.Equal(t, []byte("print(1)\n"), writes[0].content)
			require.Equal(t, 1, execs)
		})
	}
}

func TestWriteSessionWorkspaceFilesPreparesLayoutOnce(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10000))
	mgr, client := newSessionManagerExecTestHarness(t)

	require.NoError(t, mgr.WriteSessionWorkspaceFiles(ctx, "sess-1", []SessionWorkspaceFile{
		{Path: "/workspace/.skills/host/rev/SKILL.md", Content: []byte("skill")},
		{Path: "/workspace/.skills/host/rev/scripts/a.py", Content: []byte("a")},
		{Path: "/workspace/.skills/host/rev/scripts/b.py", Content: []byte("b")},
	}))

	client.mu.Lock()
	writes := append([]fakeRemoteWriteFile(nil), client.writeFiles...)
	execs := append([]RemoteExecRequest(nil), client.execRequests...)
	dirs := append([]string(nil), client.makeDirPaths...)
	client.mu.Unlock()
	require.Len(t, writes, 3)
	require.Len(t, execs, 1, "workspace bootstrap must run once for the whole tree")
	require.Contains(t, execs[0].Command, "mkdir -p")
	require.ElementsMatch(t, []string{
		"/workspace/.skills/host/rev",
		"/workspace/.skills/host/rev/scripts",
	}, dirs)
}

func TestWriteSessionWorkspaceFileRefusesSessionInput(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10000))
	mgr, client := newSessionManagerExecTestHarness(t)

	err := mgr.WriteSessionWorkspaceFile(
		ctx, "sess-1", "/workspace/input/secret.txt", []byte("nope"),
	)
	require.Error(t, err)
	client.mu.Lock()
	n := len(client.writeFiles)
	client.mu.Unlock()
	require.Zero(t, n)
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

// The whole feature is inert without this: RemoteNetworkPolicy already existed
// and both adapters already forwarded it, but nothing ever filled it in.
func TestBuildSessionCreateRequestCarriesNetworkPolicy(t *testing.T) {
	denied := false
	cfg := DefaultConfig()
	cfg.CubeTemplate = "tpl-1"
	cfg.E2BTemplate = "tpl-1"
	cfg.DockerImage = "img-1"
	cfg.Network = RemoteNetworkPolicy{
		AllowInternetAccess: &denied,
		AllowOut:            []string{"api.example.com"},
		DenyOut:             []string{"0.0.0.0/0"},
	}

	for _, provider := range []RemoteProvider{
		SandboxTypeCube, SandboxTypeE2B, SandboxTypeDocker,
	} {
		request, err := buildSessionCreateRequest(provider, cfg)
		require.NoError(t, err, "provider %s", provider)
		require.NotNil(t, request.Network.AllowInternetAccess, "provider %s", provider)
		require.False(t, *request.Network.AllowInternetAccess, "provider %s", provider)
		require.Equal(t, []string{"api.example.com"}, request.Network.AllowOut,
			"provider %s", provider)
		require.Equal(t, []string{"0.0.0.0/0"}, request.Network.DenyOut,
			"provider %s", provider)
	}
}

func TestBuildSessionCreateRequestMarksSkillSnapshot(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Type = SandboxTypeE2B
	cfg.E2BTemplate = "snap-1"
	cfg.TemplateFromSnapshot = true
	cfg.EnvVars = map[string]string{"HF_TOKEN": "secret"}

	request, err := buildSessionCreateRequest(SandboxTypeE2B, cfg)

	require.NoError(t, err)
	require.True(t, request.FromSnapshot)
	require.Equal(t, "secret", request.EnvVars["HF_TOKEN"])
	require.NotEmpty(t, request.EnvVars["WEKNORA_SKILL_OUTPUT_DIR"])
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

// Direct callers of OpenSessionTerminal (tests, a future handler that skips
// the service layer) must not see "no live sandbox" when the backend simply
// cannot stream PTYs. The service layer already maps this, but the manager
// is the source of truth.
func TestSessionBoundManagerOpenSessionTerminalUnsupportedBackend(t *testing.T) {
	mgr, _ := newSessionManagerExecTestHarness(t)
	_, err := mgr.OpenSessionTerminal(context.Background(), "session-a", RemoteTerminalOptions{})
	require.ErrorIs(t, err, ErrTerminalUnsupported)
	require.NotErrorIs(t, err, ErrNoLiveSessionSandbox)
}

func TestSessionDesktopManagerNilWhenBackendLacksCapability(t *testing.T) {
	// A backend that cannot relay desktops must not advertise the capability;
	// the frontend greys the tab out from this signal alone, before any
	// sandbox exists.
	mgr, _ := newSessionManagerExecTestHarness(t)
	require.Nil(t, mgr.SessionDesktopManager())
}

func TestOpenSessionDesktopUnsupportedBackend(t *testing.T) {
	mgr, _ := newSessionManagerExecTestHarness(t)
	_, err := mgr.OpenSessionDesktop(context.Background(), "sess-1", RemoteDesktopOptions{})
	require.ErrorIs(t, err, ErrDesktopUnsupported)
}

func TestBoundSandboxIDReadsBindingWithoutConnect(t *testing.T) {
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
	})
	require.NoError(t, err)

	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10000))
	key := SessionSandboxKey{TenantID: 10000, SessionID: "session-a"}
	created, err := store.Create(ctx, key, validSessionSandboxBinding(key, "sbx-existing"))
	require.NoError(t, err)
	require.True(t, created)

	id, ok := mgr.BoundSandboxID(ctx, "session-a")
	require.True(t, ok)
	require.Equal(t, "sbx-existing", id)

	client.mu.Lock()
	connects := append([]string(nil), client.connectIDs...)
	client.mu.Unlock()
	require.Empty(t, connects, "BoundSandboxID must not Connect")
}

func TestBoundSandboxIDReportsUnbound(t *testing.T) {
	mgr, client := newSessionManagerExecTestHarness(t)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10000))

	id, ok := mgr.BoundSandboxID(ctx, "session-a")
	require.False(t, ok)
	require.Empty(t, id)

	client.mu.Lock()
	connects := append([]string(nil), client.connectIDs...)
	client.mu.Unlock()
	require.Empty(t, connects)
}

func TestHasActiveTurnReportsLeaseState(t *testing.T) {
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

	busy, err := mgr.HasActiveTurn(ctx, "sess-1")
	require.NoError(t, err)
	require.False(t, busy)

	_, endTurn, err := mgr.BeginSessionTurn(ctx, "sess-1")
	require.NoError(t, err)
	defer endTurn()
	busy, err = mgr.HasActiveTurn(ctx, "sess-1")
	require.NoError(t, err)
	require.True(t, busy)
}

func TestTryLockRewindIsExclusive(t *testing.T) {
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

	unlock, err := mgr.TryLockRewind(ctx, "sess-1")
	require.NoError(t, err)
	_, err = mgr.TryLockRewind(ctx, "sess-1")
	require.ErrorIs(t, err, ErrSessionRewindLocked)
	unlock()
}

func TestBeginSessionTurnFailsWhenRewindLocked(t *testing.T) {
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

	unlock, err := mgr.TryLockRewind(ctx, "sess-1")
	require.NoError(t, err)
	held, err := mgr.HasRewindLock(ctx, "sess-1")
	require.NoError(t, err)
	require.True(t, held)
	_, _, err = mgr.BeginSessionTurn(ctx, "sess-1")
	require.ErrorIs(t, err, ErrSessionRewindLocked)
	unlock()
	_, endTurn, err := mgr.BeginSessionTurn(ctx, "sess-1")
	require.NoError(t, err)
	endTurn()
}

func TestTryLockRewindFailsWhenTurnActive(t *testing.T) {
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

	_, endTurn, err := mgr.BeginSessionTurn(ctx, "sess-1")
	require.NoError(t, err)
	_, err = mgr.TryLockRewind(ctx, "sess-1")
	require.ErrorIs(t, err, ErrSessionTurnActive)
	endTurn()
	unlock, err := mgr.TryLockRewind(ctx, "sess-1")
	require.NoError(t, err)
	unlock()
}

func TestHasActiveTurnWithoutLeaseStoreIsNotBusy(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10000))
	store := leaseFreeBindingStore{SessionSandboxBindingStore: NewMemorySessionSandboxBindingStore()}
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

	busy, err := mgr.HasActiveTurn(ctx, "sess-1")
	require.NoError(t, err)
	require.False(t, busy)
}

func TestCreateForkSnapshotSnapshotsBoundSandboxWithoutProvisioning(t *testing.T) {
	client := newFakeRemoteClient(SandboxTypeCube)
	client.capabilities.SupportsSnapshots = true
	store := NewMemorySessionSandboxBindingStore()
	cfg := DefaultConfig()
	cfg.CubeTemplate = "tpl-test"
	mgr, err := NewSessionBoundManager(SessionBoundManagerConfig{
		Config:          cfg,
		Client:          client,
		Store:           store,
		Checker:         &fakeSessionExistenceChecker{exists: true},
		SkipHealthProbe: true,
	})
	require.NoError(t, err)

	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10000))
	key := SessionSandboxKey{TenantID: 10000, SessionID: "session-a"}
	created, err := store.Create(ctx, key, validSessionSandboxBinding(key, "sbx-existing"))
	require.NoError(t, err)
	require.True(t, created)

	id, err := mgr.CreateForkSnapshot(ctx, "session-a", "fork-session-a-1")
	require.NoError(t, err)
	require.Equal(t, "snap-1", id)

	creates, connects, _, _, _ := client.counts()
	require.Zero(t, creates, "CreateForkSnapshot must not provision")
	require.Zero(t, connects, "CreateForkSnapshot must not Connect")
	client.mu.Lock()
	src := client.snapshots[id]
	client.mu.Unlock()
	require.Equal(t, "sbx-existing", src)
}

func TestCreateForkSnapshotErrorsWhenUnbound(t *testing.T) {
	client := newFakeRemoteClient(SandboxTypeCube)
	client.capabilities.SupportsSnapshots = true
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
	_, err = mgr.CreateForkSnapshot(ctx, "session-a", "fork-session-a-1")
	require.Error(t, err)

	creates, connects, _, _, _ := client.counts()
	require.Zero(t, creates)
	require.Zero(t, connects)
}

func TestCreateForkSnapshotErrorsWhenUnsupported(t *testing.T) {
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
	})
	require.NoError(t, err)

	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10000))
	key := SessionSandboxKey{TenantID: 10000, SessionID: "session-a"}
	created, err := store.Create(ctx, key, validSessionSandboxBinding(key, "sbx-existing"))
	require.NoError(t, err)
	require.True(t, created)

	_, err = mgr.CreateForkSnapshot(ctx, "session-a", "fork-session-a-1")
	require.Error(t, err)

	creates, connects, _, _, _ := client.counts()
	require.Zero(t, creates)
	require.Zero(t, connects)
}

type recordingForkSnapshotClient struct {
	*fakeRemoteClient
	forkCalls int
	forkName  string
	forkSrc   string
}

func (c *recordingForkSnapshotClient) CreateForkSnapshot(
	_ context.Context, sandboxID, name string,
) (RemoteSnapshotRef, error) {
	c.forkCalls++
	c.forkName = name
	c.forkSrc = sandboxID
	return RemoteSnapshotRef{ID: "fork-" + sandboxID, Names: []string{name}}, nil
}

func TestCreateForkSnapshotPrefersClientCreateForkSnapshot(t *testing.T) {
	inner := newFakeRemoteClient(SandboxTypeCube)
	inner.capabilities.SupportsSnapshots = true
	client := &recordingForkSnapshotClient{fakeRemoteClient: inner}
	store := NewMemorySessionSandboxBindingStore()
	cfg := DefaultConfig()
	cfg.CubeTemplate = "tpl-test"
	mgr, err := NewSessionBoundManager(SessionBoundManagerConfig{
		Config:          cfg,
		Client:          client,
		Store:           store,
		Checker:         &fakeSessionExistenceChecker{exists: true},
		SkipHealthProbe: true,
	})
	require.NoError(t, err)

	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10000))
	key := SessionSandboxKey{TenantID: 10000, SessionID: "session-a"}
	created, err := store.Create(ctx, key, validSessionSandboxBinding(key, "sbx-existing"))
	require.NoError(t, err)
	require.True(t, created)

	id, err := mgr.CreateForkSnapshot(ctx, "session-a", "fork-session-a-1")
	require.NoError(t, err)
	require.Equal(t, "fork-sbx-existing", id)
	require.Equal(t, 1, client.forkCalls)
	require.Equal(t, "fork-session-a-1", client.forkName)
	require.Equal(t, "sbx-existing", client.forkSrc)

	creates, connects, _, _, _ := inner.counts()
	require.Zero(t, creates, "CreateForkSnapshot must not provision")
	require.Zero(t, connects, "CreateForkSnapshot must not Connect")
	inner.mu.Lock()
	_, usedCreateSnapshot := inner.snapshots["snap-1"]
	inner.mu.Unlock()
	require.False(t, usedCreateSnapshot, "Docker must not fall back to skill CreateSnapshot")
}

type clientBoundRecordingState struct {
	withClientCalls int
	afterExecs      int
}

type clientBoundRecordingBootstrapper struct {
	state  *clientBoundRecordingState
	client RemoteSandboxClient
}

func (b *clientBoundRecordingBootstrapper) WithClient(client RemoteSandboxClient) SessionBootstrapper {
	b.state.withClientCalls++
	cp := *b
	cp.client = client
	return &cp
}

func (b *clientBoundRecordingBootstrapper) TemplateOverride(
	context.Context, SessionSandboxKey,
) (string, error) {
	return "", nil
}

func (b *clientBoundRecordingBootstrapper) AfterCreate(
	ctx context.Context, _ SessionSandboxKey, handle RemoteSandboxHandle,
) error {
	if b.client == nil || handle == nil {
		return errors.New("AfterCreate must use the bound remote client")
	}
	_, err := b.client.Exec(ctx, handle, RemoteExecRequest{
		Command: "echo weknora-fork-after-create",
		Shell:   true,
		WorkDir: SessionWorkspaceRoot,
		Timeout: time.Second,
		User:    DefaultSandboxExecUser,
	})
	if err != nil {
		return err
	}
	b.state.afterExecs++
	return nil
}

var _ SessionBootstrapper = (*clientBoundRecordingBootstrapper)(nil)

var _ SessionBootstrapperWithClient = (*clientBoundRecordingBootstrapper)(nil)

func TestNewSessionBoundManagerBindsBootstrapperToClient(t *testing.T) {
	client := newFakeRemoteClient(SandboxTypeCube)
	state := &clientBoundRecordingState{}
	boot := &clientBoundRecordingBootstrapper{state: state}
	cfg := DefaultConfig()
	cfg.CubeTemplate = "tpl-test"
	mgr, err := NewSessionBoundManager(SessionBoundManagerConfig{
		Config:          cfg,
		Client:          client,
		Store:           NewMemorySessionSandboxBindingStore(),
		Checker:         &fakeSessionExistenceChecker{exists: true},
		SkipHealthProbe: true,
		Bootstrapper:    boot,
	})
	require.NoError(t, err)

	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10000))
	_, err = mgr.Execute(ctx, &ExecuteConfig{
		SessionID:      "session-fork-boot",
		SkipValidation: true,
		ScriptContent:  "print('ok')\n",
		Script:         "hello.py",
	})
	require.NoError(t, err)
	require.Equal(t, 1, state.withClientCalls)
	require.Equal(t, 1, state.afterExecs)

	found := false
	client.mu.Lock()
	for _, req := range client.execRequests {
		if strings.Contains(req.Command, "weknora-fork-after-create") {
			found = true
			require.True(t, req.Shell)
			require.Equal(t, SessionWorkspaceRoot, req.WorkDir)
			require.Equal(t, DefaultSandboxExecUser, req.User)
		}
	}
	client.mu.Unlock()
	require.True(t, found, "AfterCreate must Exec on the handle's client")
}

// leaseFreeBindingStore implements only SessionSandboxBindingStore so the
// optional turn-lease type assert fails.
type leaseFreeBindingStore struct {
	SessionSandboxBindingStore
}

type fakeAllocations struct {
	mu            sync.Mutex
	byID          map[string]allocation.Assignment
	ownerOverride map[string]string // session id -> the session's current owner
	ineligible    map[string]bool   // session id -> its current auth source no longer qualifies
	machines      []allocation.UserMachine
}

func (f *fakeAllocations) SessionAllocation(_ context.Context, id string) (allocation.Assignment, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.byID[id]
	return a, ok, nil
}

// VerifySessionAllocation mirrors the sandboxalloc check: same assignment,
// still live. Tests change the owner with setOwner to exercise C15.
func (f *fakeAllocations) VerifySessionAllocation(
	_ context.Context, _ uint64, id string, expected allocation.Assignment,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.byID[id]
	switch {
	case !ok || a.Released():
		return ErrSandboxSessionDeleted
	case f.ownerOverride[id] != "" && f.ownerOverride[id] != expected.Machine.OwnerID:
		return ErrUserAllocationRejected
	case a.Machine != expected.Machine || a.ConfigTenantID != expected.ConfigTenantID ||
		a.ConfigID != expected.ConfigID:
		return ErrUserAllocationRejected
	case a.IsUser() && f.ineligible[id]:
		return ErrUserAllocationRejected
	}
	return nil
}

func (f *fakeAllocations) setIneligible(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ineligible == nil {
		f.ineligible = map[string]bool{}
	}
	f.ineligible[id] = true
}

func (f *fakeAllocations) setOwner(id, owner string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ownerOverride == nil {
		f.ownerOverride = map[string]string{}
	}
	f.ownerOverride[id] = owner
}

func (f *fakeAllocations) UserMachinesByConfig(context.Context, uint64, string) ([]allocation.UserMachine, error) {
	return f.machines, nil
}

func (f *fakeAllocations) release(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a := f.byID[id]
	now := time.Now()
	a.ReleasedAt = &now
	f.byID[id] = a
}

func userAssignmentFor(sessionID string) allocation.Assignment {
	return allocation.Assignment{
		SessionID: sessionID, ConfigTenantID: 42, ConfigID: "cfg-1",
		Machine: allocation.UserMachine{OwnerTenantID: 42, OwnerID: "u-1", ConfigTenantID: 42, ConfigID: "cfg-1"},
	}
}

type userHarness struct {
	mgr       *SessionBoundManager
	client    *fakeRemoteClient
	allocs    *fakeAllocations
	admission *admission.MemoryRegistry
}

func newUserManagerHarness(t *testing.T, assignments ...allocation.Assignment) *userHarness {
	t.Helper()
	client := newFakeRemoteClient(SandboxTypeCube)
	// The shared fake does not advertise PTYs; session-mode tests rely on that.
	// User-mode terminal coverage needs the flag without changing those tests.
	client.capabilities.SupportsTerminals = true
	return newUserManagerHarnessWith(t, client, NewMemorySessionSandboxBindingStore(),
		admission.NewMemory(), assignments...)
}

func newFakeAllocations(assignments ...allocation.Assignment) *fakeAllocations {
	allocs := &fakeAllocations{byID: map[string]allocation.Assignment{}}
	for _, a := range assignments {
		allocs.byID[a.SessionID] = a
	}
	return allocs
}

func newUserManagerHarnessWith(
	t *testing.T, client *fakeRemoteClient, store SessionSandboxBindingStore, reg admission.Registry,
	assignments ...allocation.Assignment,
) *userHarness {
	t.Helper()
	return newUserManagerHarnessWithDirectory(t, client, store, reg, newFakeAllocations(assignments...), "weknora")
}

// newUserManagerHarnessWithDirectory is newUserManagerHarnessWith with the
// allocation directory and Redis namespace passed in, so two replicas can
// share one directory the way production replicas share one database.
func newUserManagerHarnessWithDirectory(
	t *testing.T, client *fakeRemoteClient, store SessionSandboxBindingStore, reg admission.Registry,
	allocs *fakeAllocations, namespace string,
) *userHarness {
	t.Helper()
	cfg := DefaultConfig()
	cfg.CubeTemplate = "tpl-test"
	mgr, err := NewSessionBoundManager(SessionBoundManagerConfig{
		Config: cfg, Client: client, Store: store,
		Checker: &fakeSessionExistenceChecker{exists: true}, SkipHealthProbe: true,
		ConfigID: "cfg-1", Allocations: allocs, Admission: reg, Namespace: namespace,
	})
	require.NoError(t, err)
	mgr.admissionWait = 200 * time.Millisecond
	mem, _ := reg.(*admission.MemoryRegistry)
	return &userHarness{mgr: mgr, client: client, allocs: allocs, admission: mem}
}

// integrationEnv returns an integration dependency's address. Missing, the
// test is skipped — unless WEKNORA_REQUIRE_INTEGRATION=1, which turns the
// skip into a failure so an enablement run cannot pass by skipping.
func integrationEnv(t *testing.T, name string) string {
	t.Helper()
	v := strings.TrimSpace(os.Getenv(name))
	if v != "" {
		return v
	}
	if os.Getenv("WEKNORA_REQUIRE_INTEGRATION") == "1" {
		t.Fatalf("%s is required when WEKNORA_REQUIRE_INTEGRATION=1", name)
	}
	t.Skipf("set %s to run against a real dependency", name)
	return ""
}

// multiReplicaPair builds two managers that share one provider and one
// allocation directory. An empty redisAddr starts miniredis; otherwise both
// replicas dial that address. Each run uses its own namespace so a shared
// Redis needs no cleanup.
func multiReplicaPair(
	t *testing.T, redisAddr string, assignments ...allocation.Assignment,
) (*userHarness, *userHarness, *redis.Client) {
	t.Helper()
	if redisAddr == "" {
		redisAddr = miniredis.RunT(t).Addr()
	}
	ns := "weknora-it-" + uuid.NewString()[:8]
	client := newFakeRemoteClient(SandboxTypeCube)
	allocs := newFakeAllocations(assignments...)
	replica := func() *userHarness {
		rc := redis.NewClient(&redis.Options{Addr: redisAddr})
		t.Cleanup(func() { _ = rc.Close() })
		store, err := NewRedisSessionSandboxBindingStore(rc, ns)
		require.NoError(t, err)
		return newUserManagerHarnessWithDirectory(t, client, store,
			admission.NewRedis(rc, ns+":sandbox:user:"), allocs, ns)
	}
	raw := redis.NewClient(&redis.Options{Addr: redisAddr})
	t.Cleanup(func() { _ = raw.Close() })
	return replica(), replica(), raw
}

func ownerCtx() context.Context {
	return types.WithSandboxTenantID(context.Background(), 42)
}

func machineDigest(t *testing.T, sessionID string) string {
	t.Helper()
	m, ok := userAssignmentFor(sessionID).UserMachine()
	require.True(t, ok)
	return m.Digest("weknora")
}

func TestUserSessionsShareOneMachineWithSeparateDirectories(t *testing.T) {
	h := newUserManagerHarness(t, userAssignmentFor(userSessionA), userAssignmentFor(userSessionB))
	ctx := ownerCtx()

	_, err := h.mgr.ExecShellCommand(ctx, userSessionA, "true", "", time.Second, nil)
	require.NoError(t, err)
	reqA := lastExecRequest(t, h.client)
	_, err = h.mgr.ExecShellCommand(ctx, userSessionB, "true", "", time.Second, map[string]string{
		"WEKNORA_SKILL_OUTPUT_DIR": "/workspace/output", // a config value must not win
	})
	require.NoError(t, err)
	reqB := lastExecRequest(t, h.client)

	creates, _, _, _, _ := h.client.counts()
	require.Equal(t, 1, creates)
	require.Equal(t, userSessionRoot(userSessionA), reqA.WorkDir)
	require.Equal(t, userSessionRoot(userSessionB), reqB.WorkDir)
	require.Equal(t, userSessionRoot(userSessionB)+"/output", reqB.Env["WEKNORA_SKILL_OUTPUT_DIR"])
	require.Equal(t, userSessionRoot(userSessionB)+"/input", reqB.Env["WEKNORA_SESSION_INPUT_DIR"])

	idA, ok := h.mgr.BoundSandboxID(ctx, userSessionA)
	require.True(t, ok)
	idB, _ := h.mgr.BoundSandboxID(ctx, userSessionB)
	require.Equal(t, idA, idB)

	layout, err := h.mgr.SessionWorkspaceLayout(context.Background(), userSessionA)
	require.NoError(t, err, "layout lookups run without a tenant (tool Description)")
	require.True(t, layout.IsSharedRemote())

	n, err := h.admission.ActiveExcept(ctx, admission.MachineScope(machineDigest(t, userSessionA)))
	require.NoError(t, err)
	require.Zero(t, n, "every operation released its leases")
}

func TestUserSessionMayUseTheWholeMachine(t *testing.T) {
	h := newUserManagerHarness(t, userAssignmentFor(userSessionA))
	ctx := ownerCtx()
	_, err := h.mgr.ExecShellCommand(ctx, userSessionA, "true", "/workspace", time.Second, nil)
	require.NoError(t, err, "work_dir is not clamped to the session directory")
	err = h.mgr.WriteSessionWorkspaceFile(ctx, userSessionA, userSessionRoot(userSessionB)+"/x", []byte("x"))
	require.NoError(t, err, "a sibling session's directory is not refused any more")
	err = h.mgr.WriteSessionInputFile(ctx, userSessionA, "/workspace/input/a.pdf", []byte("x"))
	require.Error(t, err, "the legacy attachment root stays reserved")
	_, err = h.mgr.ReadSessionFile(ctx, userSessionA, userSessionRoot(userSessionB)+"/x")
	require.NoError(t, err, "reads of a sibling's tree go through to the provider")
}

func TestUserSessionWorkspaceWritesPreserveSiblingInputs(t *testing.T) {
	h := newUserManagerHarness(t, userAssignmentFor(userSessionA), userAssignmentFor(userSessionB))
	ctx := ownerCtx()
	input := userSessionRoot(userSessionB) + "/input/original.txt"
	require.NoError(t, h.mgr.WriteSessionInputFile(ctx, userSessionB, input, []byte("original")),
		"application attachment staging remains allowed")
	writesBefore := len(h.client.writeFiles)
	err := h.mgr.WriteSessionWorkspaceFile(ctx, userSessionA, input, []byte("modified"))
	require.ErrorContains(t, err, "read-only")
	err = h.mgr.WriteSessionWorkspaceFiles(ctx, userSessionA, []SessionWorkspaceFile{
		{Path: userSessionRoot(userSessionB) + "/output/report.txt", Content: []byte("report")},
		{Path: userSessionRoot(userSessionB) + "/output/../input/original.txt", Content: []byte("modified")},
	})
	require.ErrorContains(t, err, "read-only")
	require.Len(t, h.client.writeFiles, writesBefore, "reject the entire batch before writing any files")
	_, err = h.mgr.ReadSessionFile(ctx, userSessionA, input)
	require.NoError(t, err, "sibling inputs remain readable")
}

func TestValidityIsReadFreshNotCached(t *testing.T) {
	h := newUserManagerHarness(t, userAssignmentFor(userSessionA))
	ctx := ownerCtx()
	_, err := h.mgr.ExecShellCommand(ctx, userSessionA, "true", "", time.Second, nil)
	require.NoError(t, err)

	h.allocs.release(userSessionA) // deleted by another replica after this manager cached the address
	_, err = h.mgr.ExecShellCommand(ctx, userSessionA, "true", "", time.Second, nil)
	require.ErrorIs(t, err, ErrSandboxSessionDeleted)
	_, err = h.mgr.ListSessionFiles(ctx, userSessionA, userSessionRoot(userSessionA)+"/output")
	require.ErrorIs(t, err, ErrSandboxSessionDeleted, "lookup-only reads are admitted the same way")
}

func TestUserSessionDestroyIsRefused(t *testing.T) {
	h := newUserManagerHarness(t, userAssignmentFor(userSessionA))
	ctx := ownerCtx()
	_, err := h.mgr.ExecShellCommand(ctx, userSessionA, "true", "", time.Second, nil)
	require.NoError(t, err)
	require.ErrorIs(t, h.mgr.DestroySession(ctx, userSessionA), ErrSharedSandbox)
	_, _, _, _, deletes := h.client.counts()
	require.Zero(t, deletes)
}

func TestIneligibleStoredUserAssignmentIsRejected(t *testing.T) {
	h := newUserManagerHarness(t, userAssignmentFor(userSessionA))
	// The session's current auth source no longer qualifies (C15, C17).
	h.allocs.setIneligible(userSessionA)
	_, err := h.mgr.ExecShellCommand(ownerCtx(), userSessionA, "true", "", time.Second, nil)
	require.ErrorIs(t, err, ErrUserAllocationRejected)
}

func TestSessionModeAssignmentKeepsHistoricalPath(t *testing.T) {
	a := userAssignmentFor(userSessionA)
	a.Machine = allocation.UserMachine{} // no machine: a machine of its own
	h := newUserManagerHarness(t, a)
	ctx := ownerCtx()
	_, err := h.mgr.ExecShellCommand(ctx, userSessionA, "true", "", time.Second, nil)
	require.NoError(t, err)
	req := lastExecRequest(t, h.client)
	require.Equal(t, SessionWorkspaceRoot, req.WorkDir)
	require.Equal(t, userSessionA, h.client.lastCreateRequest.Metadata[remoteMetadataSessionID])
}

func TestUserTurnHoldsAreIndependentAndTravelInTheirContext(t *testing.T) {
	h := newUserManagerHarness(t, userAssignmentFor(userSessionA))
	ctx := ownerCtx()
	scope := admission.MachineScope(machineDigest(t, userSessionA))

	outerCtx, endOuter, err := h.mgr.BeginSessionTurn(ctx, userSessionA)
	require.NoError(t, err)
	innerCtx, endInner, err := h.mgr.BeginSessionTurn(outerCtx, userSessionA) // nested send hold
	require.NoError(t, err)
	n, _ := h.admission.ActiveExcept(ctx, scope)
	require.Equal(t, 2, n, "each hold registers its own member")

	n, _ = h.admission.ActiveExcept(ctx, scope, admissionExemptFrom(innerCtx)...)
	require.Zero(t, n, "a tool call of this turn exempts exactly the turn's own holds")
	n, _ = h.admission.ActiveExcept(ctx, scope, admissionExemptFrom(ctx)...)
	require.Equal(t, 2, n, "a request outside the turn exempts nothing")

	endOuter()
	n, _ = h.admission.ActiveExcept(ctx, scope)
	require.Equal(t, 1, n, "ending one hold never removes another")
	endInner()
	endInner() // idempotent
	n, _ = h.admission.ActiveExcept(ctx, scope)
	require.Zero(t, n)
}

func TestUserTurnExemptionSurvivesCloneContext(t *testing.T) {
	h := newUserManagerHarness(t, userAssignmentFor(userSessionA))
	turnCtx, end, err := h.mgr.BeginSessionTurn(ownerCtx(), userSessionA)
	require.NoError(t, err)
	defer end()
	detached := logger.CloneContext(turnCtx) // how setupSSEStream builds the async QA context
	require.Equal(t, admissionExemptFrom(turnCtx), admissionExemptFrom(detached))
}

func TestUserTurnContextEndsWhenItsLeaseIsLost(t *testing.T) {
	h := newUserManagerHarness(t, userAssignmentFor(userSessionA))
	ctx := ownerCtx()
	turnCtx, end, err := h.mgr.BeginSessionTurn(ctx, userSessionA)
	require.NoError(t, err)
	defer end()
	scope := admission.MachineScope(machineDigest(t, userSessionA))
	h.admission.ExpireMember(scope, admissionExemptFrom(turnCtx)[0])
	select {
	case <-turnCtx.Done():
		require.ErrorIs(t, context.Cause(turnCtx), admission.ErrLeaseLost)
	case <-time.After(time.Second):
		t.Fatal("the turn kept running after its registration was lost")
	}
}

func TestVerifyRejectsASessionWhoseOwnerChanged(t *testing.T) {
	h := newUserManagerHarness(t, userAssignmentFor(userSessionA))
	ctx := ownerCtx()
	_, err := h.mgr.ExecShellCommand(ctx, userSessionA, "true", "", time.Second, nil)
	require.NoError(t, err)
	h.allocs.setOwner(userSessionA, "u-2")
	_, err = h.mgr.ExecShellCommand(ctx, userSessionA, "true", "", time.Second, nil)
	require.ErrorIs(t, err, ErrUserAllocationRejected, "the assignment snapshot is not enough (C15)")
}

func TestUserTerminalDialStopsWhenTheLeaseIsLost(t *testing.T) {
	h := newUserManagerHarness(t, userAssignmentFor(userSessionA))
	ctx := ownerCtx()
	_, err := h.mgr.ExecShellCommand(ctx, userSessionA, "true", "", time.Second, nil)
	require.NoError(t, err)
	started := make(chan struct{})
	h.client.openTerminalHook = func(dialCtx context.Context) error {
		close(started)
		<-dialCtx.Done()
		return context.Cause(dialCtx)
	}
	errCh := make(chan error, 1)
	go func() {
		_, openErr := h.mgr.OpenSessionTerminal(ctx, userSessionA, RemoteTerminalOptions{})
		errCh <- openErr
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("dial did not start")
	}
	h.admission.ExpireScope(admission.MachineScope(machineDigest(t, userSessionA)))
	select {
	case err := <-errCh:
		require.ErrorIs(t, err, admission.ErrLeaseLost)
	case <-time.After(2 * time.Second):
		t.Fatal("dial kept running after its lease was lost")
	}
}

func TestTerminalIsClosedWhenItsLeaseIsLost(t *testing.T) {
	h := newUserManagerHarness(t, userAssignmentFor(userSessionA))
	ctx := ownerCtx()
	_, err := h.mgr.ExecShellCommand(ctx, userSessionA, "true", "", time.Second, nil)
	require.NoError(t, err)
	term, err := h.mgr.OpenSessionTerminal(ctx, userSessionA, RemoteTerminalOptions{})
	require.NoError(t, err)
	defer func() { _ = term.Close() }()
	scope := admission.MachineScope(machineDigest(t, userSessionA))
	member := term.(*heldTerminalSession).member
	h.admission.ExpireMember(scope, member)
	require.Eventually(t, func() bool { return h.client.terminalClosed() }, time.Second, 10*time.Millisecond,
		"a PTY must not keep running on a machine another replica may now rebuild")
}

func TestLostLeaseCancelsTheOperation(t *testing.T) {
	h := newUserManagerHarness(t, userAssignmentFor(userSessionA))
	ctx := ownerCtx()
	opCtx, release, err := h.mgr.HoldSessionOperation(ctx, userSessionA, "desktop")
	require.NoError(t, err)
	defer release()
	members := admissionExemptFrom(opCtx)
	require.NotEmpty(t, members)
	h.admission.ExpireMember(admission.MachineScope(machineDigest(t, userSessionA)), members[0])
	select {
	case <-opCtx.Done():
		require.ErrorIs(t, context.Cause(opCtx), admission.ErrLeaseLost)
	case <-time.After(time.Second):
		t.Fatal("an operation whose lease is lost must be cancelled")
	}
}

func TestSessionExclusiveWaitsAndOwnerKeepsWorking(t *testing.T) {
	h := newUserManagerHarness(t, userAssignmentFor(userSessionA))
	ctx := ownerCtx()
	_, release, err := h.mgr.HoldSessionOperation(ctx, userSessionA, "pty")
	require.NoError(t, err)

	_, _, err = h.mgr.AcquireSessionExclusive(ctx, userSessionA)
	require.ErrorIs(t, err, ErrSessionWorkspaceBusy, "an attached terminal blocks exclusive work")
	release()

	exCtx, unlock, err := h.mgr.AcquireSessionExclusive(ctx, userSessionA)
	require.NoError(t, err)
	defer unlock()
	_, err = h.mgr.ExecShellCommand(exCtx, userSessionA, "true", "", time.Second, nil)
	require.NoError(t, err, "the exclusive owner's own commands do not lock themselves out")
	_, err = h.mgr.ExecShellCommand(ctx, userSessionA, "true", "", time.Second, nil)
	require.ErrorContains(t, err, "busy", "everyone else waits for the exclusive window")
}

func TestExclusiveWindowIsAMachineMemberAndKeepsSharedSandbox(t *testing.T) {
	h := newUserManagerHarness(t, userAssignmentFor(userSessionA), userAssignmentFor(userSessionB))
	ctx := ownerCtx()
	_, err := h.mgr.ExecShellCommand(ctx, userSessionA, "true", "", time.Second, nil)
	require.NoError(t, err)
	first, _ := h.mgr.BoundSandboxID(ctx, userSessionA)
	_, _, _, _, deletesBefore := h.client.counts()

	_, unlock, err := h.mgr.AcquireSessionExclusive(ctx, userSessionA)
	require.NoError(t, err)

	scope := admission.MachineScope(machineDigest(t, userSessionA))
	n, err := h.admission.ActiveExcept(ctx, scope)
	require.NoError(t, err)
	require.Equal(t, 1, n, "rewind and directory delete must be visible to machine reclaim")

	m, _ := userAssignmentFor(userSessionA).UserMachine()
	_, err = h.mgr.InvalidateConfigSandboxesForMachines(ctx, "cfg-1", []allocation.UserMachine{m})
	require.NoError(t, err)
	_, err = h.mgr.ExecShellCommand(ctx, userSessionB, "true", "", time.Second, nil)
	require.NoError(t, err)
	kept, _ := h.mgr.BoundSandboxID(ctx, userSessionB)
	require.Equal(t, first, kept, "image changes must preserve the shared machine during the exclusive window")
	_, _, _, _, deletesAfter := h.client.counts()
	require.Equal(t, deletesBefore, deletesAfter)

	unlock()
	n, err = h.admission.ActiveExcept(ctx, scope)
	require.NoError(t, err)
	require.Zero(t, n, "ending the window releases the machine member")
}

func TestLockRewindSessionIsExclusiveForTheSessionOnly(t *testing.T) {
	h := newUserManagerHarness(t, userAssignmentFor(userSessionA), userAssignmentFor(userSessionB))
	ctx := ownerCtx()
	h.mgr.admissionWait = 50 * time.Millisecond
	h.mgr.rewindWait = 50 * time.Millisecond

	_, closePTY, err := h.mgr.HoldSessionOperation(ctx, userSessionA, "pty")
	require.NoError(t, err)
	_, _, err = h.mgr.LockRewindSession(ctx, userSessionA)
	require.ErrorIs(t, err, ErrSessionWorkspaceBusy, "A's open terminal blocks A's rewind")
	locked, err := h.mgr.HasRewindLock(ctx, userSessionA)
	require.NoError(t, err)
	require.False(t, locked, "a failed exclusive window releases the rewind lock too")
	closePTY()

	exCtx, unlock, err := h.mgr.LockRewindSession(ctx, userSessionA)
	require.NoError(t, err)
	_, err = h.mgr.ExecShellCommand(exCtx, userSessionA, "true", "", time.Second, nil)
	require.NoError(t, err, "the rewind's own reset runs inside its window")
	_, err = h.mgr.ExecShellCommand(ctx, userSessionA, "true", "", time.Second, nil)
	require.ErrorIs(t, err, ErrSessionWorkspaceBusy, "anything else of A waits for the rewind")
	_, err = h.mgr.ExecShellCommand(ctx, userSessionB, "true", "", time.Second, nil)
	require.NoError(t, err, "B on the same machine is unaffected")
	unlock()

	_, err = h.mgr.ExecShellCommand(ctx, userSessionA, "true", "", time.Second, nil)
	require.NoError(t, err)
}

func TestLockRewindSessionOnAPerSessionSandboxKeepsTheContext(t *testing.T) {
	mgr, _ := newSessionManagerExecTestHarness(t)
	// sessionKey refuses a context with no tenant; the marker still has to
	// come back on the same context session mode returns unchanged.
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10000))
	ctx = context.WithValue(ctx, struct{ k string }{"marker"}, 1)
	got, unlock, err := mgr.LockRewindSession(ctx, "sess-1")
	require.NoError(t, err)
	defer unlock()
	require.Equal(t, ctx, got)
	require.Equal(t, 1, got.Value(struct{ k string }{"marker"}))
}

func userSessionRoot(id string) string {
	l, err := UserWorkspaceLayout(id)
	if err != nil {
		panic(err)
	}
	return l.Root
}

func TestUploadedScriptsStayInTheSessionDirectory(t *testing.T) {
	h := newUserManagerHarness(t, userAssignmentFor(userSessionA))
	script := filepath.Join(t.TempDir(), "run.py")
	require.NoError(t, os.WriteFile(script, []byte("print(1)"), 0o600))
	_, err := h.mgr.Execute(ownerCtx(), &ExecuteConfig{SessionID: userSessionA, Script: script, SkipValidation: true})
	require.NoError(t, err)
	h.client.mu.Lock()
	defer h.client.mu.Unlock()
	require.NotEmpty(t, h.client.writeFiles)
	require.Equal(t, userSessionRoot(userSessionA)+"/.weknora/scripts/run.py",
		h.client.writeFiles[len(h.client.writeFiles)-1].path)
}

func TestMultiReplicaSharedMachineSurvivesDesktopDisconnect(t *testing.T) {
	r1, r2, _ := multiReplicaPair(t, "",
		userAssignmentFor(userSessionA), userAssignmentFor(userSessionB))
	ctx := ownerCtx()

	_, err := r1.mgr.ExecShellCommand(ctx, userSessionA, "true", "", time.Second, nil)
	require.NoError(t, err)
	first, _ := r1.mgr.BoundSandboxID(ctx, userSessionA)

	_, release, err := r2.mgr.HoldSessionOperation(ctx, userSessionB, "desktop")
	require.NoError(t, err)
	m, _ := userAssignmentFor(userSessionA).UserMachine()
	_, err = r1.mgr.InvalidateConfigSandboxesForMachines(ctx, "cfg-1", []allocation.UserMachine{m})
	require.NoError(t, err)

	_, err = r1.mgr.ExecShellCommand(ctx, userSessionA, "true", "", time.Second, nil)
	require.NoError(t, err)
	kept, _ := r1.mgr.BoundSandboxID(ctx, userSessionA)
	require.Equal(t, first, kept, "a skill image change keeps the machine while another replica uses its desktop")

	release()
	_, err = r1.mgr.ExecShellCommand(ctx, userSessionA, "true", "", time.Second, nil)
	require.NoError(t, err)
	kept, _ = r1.mgr.BoundSandboxID(ctx, userSessionA)
	require.Equal(t, first, kept, "disconnecting must not trigger a deferred rebuild")
}

func TestDeleteSessionWorkspacesWaitsForTheSessionAndRemovesOnlyIt(t *testing.T) {
	h := newUserManagerHarness(t, userAssignmentFor(userSessionA), userAssignmentFor(userSessionB))
	ctx := ownerCtx()
	_, err := h.mgr.ExecShellCommand(ctx, userSessionA, "true", "", time.Second, nil)
	require.NoError(t, err)
	_, connectsBefore, _, _, _ := h.client.counts()
	m, _ := userAssignmentFor(userSessionA).UserMachine()

	_, releasePTY, err := h.mgr.HoldSessionOperation(ctx, userSessionA, "pty")
	require.NoError(t, err)
	deleted, err := h.mgr.DeleteSessionWorkspaces(ctx, m, []string{userSessionA})
	require.NoError(t, err)
	require.Empty(t, deleted, "an attached terminal of A keeps A's directory until it leaves")
	releasePTY()

	deleted, err = h.mgr.DeleteSessionWorkspaces(ctx, m, []string{userSessionA})
	require.NoError(t, err)
	require.Equal(t, []string{userSessionA}, deleted)
	_, connectsAfter, _, _, deletes := h.client.counts()
	require.Greater(t, connectsAfter, connectsBefore, "deleting a session connects (and resumes) the machine")
	require.Zero(t, deletes, "the shared machine itself stays")
	cmd := lastExecRequest(t, h.client).Command
	layout, err := h.mgr.SessionWorkspaceLayout(context.Background(), userSessionA)
	require.NoError(t, err)
	require.Contains(t, cmd, layout.Root)
	require.Contains(t, cmd, layout.GitDir)
	require.NotContains(t, cmd, userSessionB)
	require.Contains(t, cmd, "-L", "symlinked parents are refused, not followed")
}

func TestDeleteSessionWorkspacesAlsoRemovesTheBrowserRoot(t *testing.T) {
	h := newUserManagerHarness(t, userAssignmentFor(userSessionA))
	ctx := ownerCtx()
	_, err := h.mgr.ExecShellCommand(ctx, userSessionA, "true", "", time.Second, nil)
	require.NoError(t, err)
	m, ok := userAssignmentFor(userSessionA).UserMachine()
	require.True(t, ok)

	_, err = h.mgr.DeleteSessionWorkspaces(ctx, m, []string{userSessionA})
	require.NoError(t, err)

	layout, err := UserWorkspaceLayout(userSessionA)
	require.NoError(t, err)
	script := lastExecRequest(t, h.client).Command
	require.Contains(t, script, ShellQuote(layout.BrowserRoot))
	require.Contains(t, script, ShellQuote(layout.GitDir), "existing targets stay")
	require.Contains(t, script, "command -v wk-display")
	releaseAt := strings.Index(script, "wk-display release "+ShellQuote(userSessionA))
	removeAt := strings.Index(script, ShellQuote(layout.BrowserRoot))
	require.GreaterOrEqual(t, releaseAt, 0)
	require.Less(t, removeAt, releaseAt, "release runs after the browser directory is removed")
	require.NotContains(t, script, "wk-display release "+ShellQuote(userSessionB),
		"deleting A must not release B's seat")
}

func TestDeleteSessionWorkspacesCompletesOnlyWhenTheMachineIsProvablyGone(t *testing.T) {
	h := newUserManagerHarness(t)
	m, _ := userAssignmentFor(userSessionA).UserMachine()
	deleted, err := h.mgr.DeleteSessionWorkspaces(ownerCtx(), m, []string{userSessionA})
	require.NoError(t, err)
	require.Equal(t, []string{userSessionA}, deleted, "the provider lists nothing with this identity")
}

func TestDeleteSessionWorkspacesReadoptsAMachineWhoseBindingWasLost(t *testing.T) {
	h := newUserManagerHarness(t, userAssignmentFor(userSessionA), userAssignmentFor(userSessionB))
	// DefaultConfig closes inbound. This case is the provider that does not
	// require a token, so re-adoption restores the same sandbox. A closed
	// inbound policy must not bind the empty token; that is covered separately.
	h.mgr.lifecycle.createRequest.Network.AllowPublicTraffic = nil
	ctx := ownerCtx()
	_, err := h.mgr.ExecShellCommand(ctx, userSessionA, "true", "", time.Second, nil)
	require.NoError(t, err)
	m, _ := userAssignmentFor(userSessionA).UserMachine()
	sandboxID, _ := h.mgr.BoundSandboxID(ctx, userSessionA)
	_, err = h.mgr.bindings.DeleteIfMatch(ctx, h.mgr.UserMachineKey(m), h.client.provider, sandboxID) // Redis lost it
	require.NoError(t, err)

	deleted, err := h.mgr.DeleteSessionWorkspaces(ctx, m, []string{userSessionA})
	require.NoError(t, err)
	require.Equal(t, []string{userSessionA}, deleted)
	require.Contains(t, lastExecRequest(t, h.client).Command, userSessionRoot(userSessionA),
		"the files are really deleted on the machine B will keep using")
	rebound, _ := h.mgr.BoundSandboxID(ctx, userSessionB)
	require.Equal(t, sandboxID, rebound, "re-adopted, not recreated")
}

func TestDeleteSessionWorkspacesKeepsTheTaskWhenAbsenceCannotBeProven(t *testing.T) {
	h := newUserManagerHarness(t)
	h.client.capabilities.SupportsListSandboxes = false
	m, _ := userAssignmentFor(userSessionA).UserMachine()
	deleted, err := h.mgr.DeleteSessionWorkspaces(ownerCtx(), m, []string{userSessionA})
	require.ErrorIs(t, err, ErrMachineAbsenceUnprovable)
	require.Empty(t, deleted)
}

func TestDeleteSessionWorkspacesStopsTheCommandWhenALeaseIsLost(t *testing.T) {
	h := newUserManagerHarness(t, userAssignmentFor(userSessionA))
	ctx := ownerCtx()
	_, err := h.mgr.ExecShellCommand(ctx, userSessionA, "true", "", time.Second, nil)
	require.NoError(t, err)
	m, _ := userAssignmentFor(userSessionA).UserMachine()
	h.client.execHook = func(req RemoteExecRequest) (*RemoteExecResult, error) {
		if strings.Contains(req.Command, "rm -rf") {
			h.admission.LoseBarrier(admission.SessionScope(m.Digest("weknora"), userSessionA))
			time.Sleep(50 * time.Millisecond) // the cancellation reaches the call
			return nil, context.Canceled
		}
		return &RemoteExecResult{}, nil
	}
	deleted, err := h.mgr.DeleteSessionWorkspaces(ctx, m, []string{userSessionA})
	require.ErrorIs(t, err, admission.ErrLeaseLost)
	require.Empty(t, deleted, "a deletion that may not have run is retried, not completed")
}

func TestInvalidateConfigSandboxesMarksUserMachines(t *testing.T) {
	h := newUserManagerHarness(t, userAssignmentFor(userSessionA))
	ctx := ownerCtx()
	_, err := h.mgr.ExecShellCommand(ctx, userSessionA, "true", "", time.Second, nil)
	require.NoError(t, err)
	m, _ := userAssignmentFor(userSessionA).UserMachine()
	h.allocs.machines = []allocation.UserMachine{m}

	n, err := h.mgr.InvalidateConfigSandboxes(ctx, 42, "cfg-1")
	require.NoError(t, err)
	require.Equal(t, 1, n)
	b, err := h.mgr.bindings.Get(ctx, h.mgr.UserMachineKey(m))
	require.NoError(t, err)
	require.NotNil(t, b.StaleAt)
}

func TestWorkspaceForkHoldsLeasesAndCopiesWithOneGuardedScript(t *testing.T) {
	h := newUserManagerHarness(t, userAssignmentFor(userSessionA))
	ctx := ownerCtx()
	_, err := h.mgr.ExecShellCommand(ctx, userSessionA, "true", "", time.Second, nil)
	require.NoError(t, err)
	sha, older, foreign := strings.Repeat("c", 40), strings.Repeat("b", 40), strings.Repeat("e", 40)
	h.client.execHook = func(req RemoteExecRequest) (*RemoteExecResult, error) {
		if strings.Contains(req.Command, "uploadpack.allowAnySHA1InWant") {
			return &RemoteExecResult{Stdout: "R " + sha + "\nR " + older + "\n"}, nil
		}
		return &RemoteExecResult{}, nil
	}

	fork, err := h.mgr.BeginWorkspaceFork(ctx, userSessionA)
	require.NoError(t, err)
	require.NotEmpty(t, fork.SandboxID())
	machine, _ := userAssignmentFor(userSessionA).UserMachine()
	active, err := h.admission.ActiveExcept(ctx, admission.MachineScope(machine.Digest("weknora")))
	require.NoError(t, err)
	require.Equal(t, 1, active, "the fork holds the machine until Release")

	reachable, err := fork.Copy(userSessionB, sha, []string{sha, older, foreign})
	require.NoError(t, err)
	require.Equal(t, map[string]bool{sha: true, older: true}, reachable)
	req := lastExecRequest(t, h.client)
	require.Equal(t, "root", req.User)
	require.Contains(t, req.Command, "/var/lib/weknora/sessions/c812e1edb644.git")
	require.Contains(t, req.Command, userSessionRoot(userSessionB))
	require.Contains(t, req.Command, "core.hooksPath=/dev/null")
	require.Contains(t, req.Command, `"160000"`, "gitlinks are refused")
	require.Contains(t, req.Command, "merge-base --is-ancestor")

	_, err = fork.Copy("not-a-uuid", sha, nil)
	require.Error(t, err)
	_, err = fork.Copy(userSessionB, "abc", nil)
	require.Error(t, err)

	require.NoError(t, fork.Remove(userSessionB))
	require.Contains(t, lastExecRequest(t, h.client).Command, userSessionRoot(userSessionB))
	fork.Release()
	active, _ = h.admission.ActiveExcept(ctx, admission.MachineScope(machine.Digest("weknora")))
	require.Zero(t, active)
}

func TestWorkspaceForkStopsAfterTheLeaseIsLost(t *testing.T) {
	h := newUserManagerHarness(t, userAssignmentFor(userSessionA))
	ctx := ownerCtx()
	_, err := h.mgr.ExecShellCommand(ctx, userSessionA, "true", "", time.Second, nil)
	require.NoError(t, err)
	fork, err := h.mgr.BeginWorkspaceFork(ctx, userSessionA)
	require.NoError(t, err)
	defer fork.Release()
	machine, _ := userAssignmentFor(userSessionA).UserMachine()
	h.admission.ExpireMember(admission.MachineScope(machine.Digest("weknora")),
		fork.(interface{ Member() string }).Member())

	require.Eventually(t, func() bool {
		_, err := fork.Copy(userSessionB, strings.Repeat("c", 40), nil)
		return errors.Is(err, admission.ErrLeaseLost)
	}, time.Second, 10*time.Millisecond)
}

func TestWorkspaceForkWithoutAMachineReportsNoLiveSandbox(t *testing.T) {
	h := newUserManagerHarness(t, userAssignmentFor(userSessionA))
	_, err := h.mgr.BeginWorkspaceFork(ownerCtx(), userSessionA)
	require.ErrorIs(t, err, ErrNoLiveSessionSandbox, "fork never creates the source's machine")
}

func TestUserModeTerminalCreatesTheSessionDirectoryFirst(t *testing.T) {
	h := newUserManagerHarness(t, userAssignmentFor(userSessionA), userAssignmentFor(userSessionB))
	ctx := ownerCtx()
	_, err := h.mgr.ExecShellCommand(ctx, userSessionA, "true", "", time.Second, nil)
	require.NoError(t, err)

	term, err := h.mgr.OpenSessionTerminal(ctx, userSessionB, RemoteTerminalOptions{})
	require.NoError(t, err)
	defer func() { _ = term.Close() }()

	cmd := lastExecRequest(t, h.client).Command
	require.Contains(t, cmd, "mkdir -p")
	require.Contains(t, cmd, userSessionRoot(userSessionB))
	require.Equal(t, userSessionRoot(userSessionB), h.client.lastTerminalOptions().Cwd)
}

func TestUserModeTerminalNeverReattachesAClientPID(t *testing.T) {
	h := newUserManagerHarness(t, userAssignmentFor(userSessionA))
	ctx := ownerCtx()
	_, err := h.mgr.ExecShellCommand(ctx, userSessionA, "true", "", time.Second, nil)
	require.NoError(t, err)
	term, err := h.mgr.OpenSessionTerminal(ctx, userSessionA, RemoteTerminalOptions{AttachPID: 4242})
	require.NoError(t, err)
	defer func() { _ = term.Close() }()
	opts := h.client.lastTerminalOptions()
	require.Zero(t, opts.AttachPID, "a PID on a shared machine may belong to another session")
	require.Equal(t, userSessionRoot(userSessionA), opts.Cwd)
}

func TestMultiReplicaRealRedisFirstAllocationCreatesOneMachine(t *testing.T) {
	r1, r2, _ := multiReplicaPair(t, integrationEnv(t, "WEKNORA_TEST_REDIS_ADDR"),
		userAssignmentFor(userSessionA), userAssignmentFor(userSessionB))
	ctx := ownerCtx()
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			h, sid := r1, userSessionA
			if i%2 == 1 {
				h, sid = r2, userSessionB
			}
			_, err := h.mgr.ExecShellCommand(ctx, sid, "true", "", time.Second, nil)
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	creates, _, _, _, _ := r1.client.counts()
	require.Equal(t, 1, creates, "two replicas racing the first exec create one shared machine")
	a, okA := r1.mgr.BoundSandboxID(ctx, userSessionA)
	b, okB := r2.mgr.BoundSandboxID(ctx, userSessionB)
	require.True(t, okA)
	require.True(t, okB)
	require.NotEmpty(t, a)
	require.Equal(t, a, b)
}

func TestMultiReplicaRealRedisLeaseBlocksReclaimAndKeepsSharedSandbox(t *testing.T) {
	r1, r2, _ := multiReplicaPair(t, integrationEnv(t, "WEKNORA_TEST_REDIS_ADDR"),
		userAssignmentFor(userSessionA), userAssignmentFor(userSessionB))
	ctx := ownerCtx()
	_, err := r1.mgr.ExecShellCommand(ctx, userSessionA, "true", "", time.Second, nil)
	require.NoError(t, err)
	first, _ := r1.mgr.BoundSandboxID(ctx, userSessionA)
	m, _ := userAssignmentFor(userSessionA).UserMachine()

	_, closePTY, err := r2.mgr.HoldSessionOperation(ctx, userSessionB, "pty")
	require.NoError(t, err)

	_, err = r1.mgr.InvalidateConfigSandboxesForMachines(ctx, "cfg-1", []allocation.UserMachine{m})
	require.NoError(t, err)
	_, err = r1.mgr.ExecShellCommand(ctx, userSessionA, "true", "", time.Second, nil)
	require.NoError(t, err)
	kept, _ := r1.mgr.BoundSandboxID(ctx, userSessionA)
	require.Equal(t, first, kept, "image changes must preserve the shared machine across replicas")

	ok, err := r1.mgr.ReclaimUserMachine(ctx, m, func(context.Context) (bool, error) { return true, nil })
	require.NoError(t, err)
	require.False(t, ok, "and blocks reclaim")

	closePTY()
	_, err = r1.mgr.ExecShellCommand(ctx, userSessionA, "true", "", time.Second, nil)
	require.NoError(t, err)
	kept, _ = r1.mgr.BoundSandboxID(ctx, userSessionA)
	require.Equal(t, first, kept, "disconnecting must not trigger a deferred rebuild")
}

func TestMultiReplicaRealRedisLostLeaseStopsTheHolderAndFreesTheMachine(t *testing.T) {
	r1, r2, raw := multiReplicaPair(t, integrationEnv(t, "WEKNORA_TEST_REDIS_ADDR"),
		userAssignmentFor(userSessionA), userAssignmentFor(userSessionB))
	ctx := ownerCtx()
	_, err := r1.mgr.ExecShellCommand(ctx, userSessionA, "true", "", time.Second, nil)
	require.NoError(t, err)
	m, _ := userAssignmentFor(userSessionA).UserMachine()

	opCtx, release, err := r2.mgr.HoldSessionOperation(ctx, userSessionB, "desktop")
	require.NoError(t, err)
	defer release()

	// Fault injection: the admission keys vanish (failover to a replica that
	// had not received them, or an operator FLUSHDB).
	keys, err := raw.Keys(ctx, "*"+m.Digest(r1.mgr.Namespace())+"*:ops").Result()
	require.NoError(t, err)
	require.NotEmpty(t, keys)
	require.NoError(t, raw.Del(ctx, keys...).Err())

	// HoldSessionOperation uses admission.DefaultHoldTTL. Renewal is ttl/3 and
	// is not configurable from the test, so the wait is bounded by that TTL:
	// a failed confirmation inside the margin reports the lease lost.
	select {
	case <-opCtx.Done():
		require.ErrorIs(t, context.Cause(opCtx), admission.ErrLeaseLost)
	case <-time.After(admission.DefaultHoldTTL):
		t.Fatal("the holder kept running after its lease was gone")
	}
	n, err := raw.Exists(ctx, keys...).Result()
	require.NoError(t, err)
	require.Zero(t, n, "renewal must not resurrect a lost member")

	ok, err := r1.mgr.ReclaimUserMachine(ctx, m, func(context.Context) (bool, error) { return true, nil })
	require.NoError(t, err)
	require.True(t, ok, "with the holder gone the machine can be reclaimed")
}

func TestWithBrowserEnvOverridesCallerValues(t *testing.T) {
	in := map[string]string{
		"WEKNORA_SESSION_ID":   "spoofed",
		"WEKNORA_BROWSER_ROOT": "/tmp/spoofed",
		"KEEP":                 "1",
	}
	out := withBrowserEnv(in, "sid-1", RemoteWorkspaceLayout())
	require.Equal(t, "sid-1", out["WEKNORA_SESSION_ID"])
	require.Equal(t, "/var/lib/weknora/browser/session", out["WEKNORA_BROWSER_ROOT"])
	require.Equal(t, "1", out["KEEP"])
	require.Equal(t, "spoofed", in["WEKNORA_SESSION_ID"], "input map must not be mutated")
}

func TestWithBrowserEnvLeavesLayoutsWithoutBrowserRootAlone(t *testing.T) {
	out := withBrowserEnv(map[string]string{"A": "b"}, "sid-1", WorkspaceLayout{})
	require.Equal(t, map[string]string{"A": "b"}, out)
}

func TestUserSessionExecGetsItsOwnBrowserEnv(t *testing.T) {
	h := newUserManagerHarness(t, userAssignmentFor(userSessionA), userAssignmentFor(userSessionB))
	ctx := ownerCtx()
	_, err := h.mgr.ExecShellCommand(ctx, userSessionB, "true", "", time.Second, map[string]string{
		"WEKNORA_SESSION_ID": userSessionA, // a model-supplied value must not win
	})
	require.NoError(t, err)
	req := lastExecRequest(t, h.client)
	require.Equal(t, userSessionB, req.Env["WEKNORA_SESSION_ID"])
	require.Equal(t, "/var/lib/weknora/sessions/"+userSessionB+".browser", req.Env["WEKNORA_BROWSER_ROOT"])
}

func TestDesktopEnsureStyleExecCarriesTheSessionIdentity(t *testing.T) {
	h := newUserManagerHarness(t, userAssignmentFor(userSessionB))
	_, err := h.mgr.ExecShellCommandWithOptions(ownerCtx(), userSessionB, "true", ShellExecOptions{
		Timeout: time.Second, SkipWorkspacePrep: true,
	})
	require.NoError(t, err)
	req := lastExecRequest(t, h.client)
	require.Equal(t, userSessionB, req.Env["WEKNORA_SESSION_ID"],
		"ensure.sh reads it to choose this session's workspace")
}
