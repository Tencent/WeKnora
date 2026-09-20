package service

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func forkTestCommitSHA() string {
	return strings.Repeat("a", 40)
}

type fakeSnapshotDeleter struct {
	deleted []string
	err     error
}

func (f *fakeSnapshotDeleter) DeleteSnapshot(_ context.Context, id string) error {
	f.deleted = append(f.deleted, id)
	return f.err
}

type fakeHandle struct {
	id string
}

func (h fakeHandle) ID() string                       { return h.id }
func (h fakeHandle) Provider() sandbox.RemoteProvider { return sandbox.SandboxTypeCube }
func (h fakeHandle) Metadata() map[string]string      { return nil }

func pendingForkSession() *types.Session {
	return &types.Session{
		ID: "fork-1", TenantID: 1,
		ForkBootstrap: &types.ForkBootstrap{
			SnapshotID: "snap-1", CommitSHA: forkTestCommitSHA(),
			SourceSandboxID: "sbx-1", CreatedAt: time.Now().UTC(),
		},
	}
}

func forkKey() sandbox.SessionSandboxKey {
	return sandbox.SessionSandboxKey{TenantID: 1, SessionID: "fork-1"}
}

func TestTemplateOverrideReturnsSnapshotForPendingFork(t *testing.T) {
	sessions := newFakeSessionStore(pendingForkSession())
	b := NewForkBootstrapper(sessions, nil, nil, nil)

	got, err := b.TemplateOverride(context.Background(), forkKey())

	require.NoError(t, err)
	require.Equal(t, "snap-1", got)
}

func TestTemplateOverrideIsEmptyForOrdinarySession(t *testing.T) {
	sessions := newFakeSessionStore(&types.Session{ID: "fork-1", TenantID: 1})
	b := NewForkBootstrapper(sessions, nil, nil, nil)

	got, err := b.TemplateOverride(context.Background(), forkKey())

	require.NoError(t, err)
	require.Empty(t, got)
}

func TestTemplateOverrideIsEmptyForConsumedFork(t *testing.T) {
	s := pendingForkSession()
	at := time.Now().UTC()
	s.ForkBootstrap.ConsumedAt = &at
	sessions := newFakeSessionStore(s)
	b := NewForkBootstrapper(sessions, nil, nil, nil)

	got, err := b.TemplateOverride(context.Background(), forkKey())

	require.NoError(t, err)
	require.Empty(t, got)
}

func TestTemplateOverridePropagatesLookupError(t *testing.T) {
	sessions := newFakeSessionStore(nil)
	sessions.getErr = errors.New("db down")
	b := NewForkBootstrapper(sessions, nil, nil, nil)

	_, err := b.TemplateOverride(context.Background(), forkKey())

	require.Error(t, err)
}

func TestAfterCreateResetsWorkspaceThenConsumesAndDeletesSnapshot(t *testing.T) {
	sessions := newFakeSessionStore(pendingForkSession())
	runner := &fakeShellRunner{result: &sandbox.ExecuteResult{ExitCode: 0}}
	snapshots := &fakeSnapshotDeleter{}
	b := NewForkBootstrapper(sessions, newFakeMessageStore(nil), runner, snapshots)

	require.NoError(t, b.AfterCreate(context.Background(), forkKey(), fakeHandle{id: "sbx-2"}))

	require.Len(t, runner.calls, 1)
	script := runner.calls[0]
	require.Contains(t, script, "reset --hard "+forkTestCommitSHA())
	require.Contains(t, script, "reflog expire --expire=now --all")
	require.Contains(t, script, "gc --prune=now")
	require.Contains(t, script, "clean -fdx")
	require.Contains(t, script, "ORIG_HEAD")

	require.Equal(t, []string{"snap-1"}, snapshots.deleted)
	require.NotNil(t, sessions.updatedBootstrap)
	require.True(t, sessions.updatedBootstrap.Consumed())
}

func TestAfterCreateConsumesWhenSnapshotDeleteFailsAfterReset(t *testing.T) {
	sessions := newFakeSessionStore(pendingForkSession())
	runner := &fakeShellRunner{result: &sandbox.ExecuteResult{ExitCode: 0}}
	snapshots := &fakeSnapshotDeleter{err: sandbox.NewRemoteError(
		sandbox.SandboxTypeCube, "DeleteSnapshot", sandbox.RemoteErrorKindConflict,
		"CubeMaster returned error code 130409: template attempt is already in progress: "+
			"snapshot snap-1 still has 2 active runtime ref(s): src@host, fork@host (HTTP 500)",
		nil,
	)}
	b := NewForkBootstrapper(sessions, newFakeMessageStore(nil), runner, snapshots)

	err := b.AfterCreate(context.Background(), forkKey(), fakeHandle{id: "sbx-2"})

	require.NoError(t, err, "git reset succeeded; an in-use Cube snapshot must not destroy the sandbox")
	require.Equal(t, []string{"snap-1"}, snapshots.deleted)
	require.False(t, sessions.bootstrapCleared)
	require.True(t, sessions.updatedBootstrap.Consumed())
	require.Equal(t, "snap-1", sessions.updatedBootstrap.SnapshotID)

	got, overrideErr := b.TemplateOverride(context.Background(), forkKey())
	require.NoError(t, overrideErr)
	require.Empty(t, got, "retry must not boot another sandbox from the same snapshot")
}

func TestAfterCreateSkipsDeleteWhenSiblingStillNeedsSnapshot(t *testing.T) {
	sessions := newFakeSessionStore(pendingForkSession())
	sessions.unconsumed = []*types.Session{{
		ID: "fork-2", TenantID: 1,
		ForkBootstrap: &types.ForkBootstrap{
			SnapshotID: "snap-1", CreatedAt: time.Now().UTC(),
		},
	}}
	runner := &fakeShellRunner{result: &sandbox.ExecuteResult{ExitCode: 0}}
	snapshots := &fakeSnapshotDeleter{}
	b := NewForkBootstrapper(sessions, newFakeMessageStore(nil), runner, snapshots)

	require.NoError(t, b.AfterCreate(context.Background(), forkKey(), fakeHandle{id: "sbx-2"}))

	require.Empty(t, snapshots.deleted, "a sibling still boots from this snapshot")
	require.True(t, sessions.updatedBootstrap.Consumed())
	require.Equal(t, "snap-1", sessions.updatedBootstrap.SnapshotID)
}

func TestAfterCreateResetFailKeepsSnapshotWhenClearFails(t *testing.T) {
	sessions := newFakeSessionStore(pendingForkSession())
	sessions.clearErr = errors.New("db down")
	runner := &fakeShellRunner{result: &sandbox.ExecuteResult{
		ExitCode: 128, Stderr: "fatal: bad object " + forkTestCommitSHA(),
	}}
	snapshots := &fakeSnapshotDeleter{}
	b := NewForkBootstrapper(sessions, newFakeMessageStore(nil), runner, snapshots)

	err := b.AfterCreate(context.Background(), forkKey(), fakeHandle{id: "sbx-2"})

	require.Error(t, err)
	require.False(t, sessions.bootstrapCleared)
	require.Empty(t, snapshots.deleted)
	require.NotNil(t, sessions.source.ForkBootstrap)
}

func TestAfterCreateRewritesCopiedCheckpointsToNewSandboxID(t *testing.T) {
	sessions := newFakeSessionStore(pendingForkSession())
	runner := &fakeShellRunner{result: &sandbox.ExecuteResult{ExitCode: 0}}
	committedAt := time.Date(2026, 9, 10, 9, 0, 1, 0, time.UTC)
	messages := newFakeMessageStore([]*types.Message{{
		ID: "a1", SessionID: "fork-1", Role: "assistant",
		SandboxCheckpoint: &types.SandboxCheckpoint{
			SandboxID: "sbx-1", CommitSHA: forkTestCommitSHA(), CommittedAt: committedAt,
		},
	}})
	b := NewForkBootstrapper(sessions, messages, runner, &fakeSnapshotDeleter{})

	require.NoError(t, b.AfterCreate(context.Background(), forkKey(), fakeHandle{id: "sbx-2"}))

	require.Equal(t, "sbx-2", messages.messages[0].SandboxCheckpoint.SandboxID)
	require.Equal(t, forkTestCommitSHA(), messages.messages[0].SandboxCheckpoint.CommitSHA)
	require.Equal(t, committedAt, messages.messages[0].SandboxCheckpoint.CommittedAt)
	require.True(t, sessions.updatedBootstrap.Consumed())
}

func TestAfterCreateRewriteFailureDoesNotConsumeBootstrap(t *testing.T) {
	sessions := newFakeSessionStore(pendingForkSession())
	runner := &fakeShellRunner{result: &sandbox.ExecuteResult{ExitCode: 0}}
	snapshots := &fakeSnapshotDeleter{}
	messages := newFakeMessageStore([]*types.Message{{
		ID: "a1", SessionID: "fork-1", Role: "assistant",
		SandboxCheckpoint: &types.SandboxCheckpoint{
			SandboxID: "sbx-1", CommitSHA: "abc123",
		},
	}})
	messages.rewriteErr = errors.New("db down")
	b := NewForkBootstrapper(sessions, messages, runner, snapshots)

	err := b.AfterCreate(context.Background(), forkKey(), fakeHandle{id: "sbx-2"})

	require.Error(t, err)
	require.False(t, sessions.bootstrapCleared)
	require.Nil(t, sessions.updatedBootstrap)
	require.False(t, sessions.source.ForkBootstrap.Consumed())
	require.Empty(t, snapshots.deleted)
	require.Equal(t, "sbx-1", messages.messages[0].SandboxCheckpoint.SandboxID)

	got, overrideErr := b.TemplateOverride(context.Background(), forkKey())
	require.NoError(t, overrideErr)
	require.Equal(t, "snap-1", got, "retry must boot from the same snapshot")
}

func TestAfterCreateIsNoOpForOrdinarySession(t *testing.T) {
	sessions := newFakeSessionStore(&types.Session{ID: "fork-1", TenantID: 1})
	runner := &fakeShellRunner{result: &sandbox.ExecuteResult{ExitCode: 0}}
	b := NewForkBootstrapper(sessions, newFakeMessageStore(nil), runner, &fakeSnapshotDeleter{})

	require.NoError(t, b.AfterCreate(context.Background(), forkKey(), fakeHandle{id: "sbx-2"}))
	require.Empty(t, runner.calls)
}

// 全有或全无：reset 失败必须返回 error，让 lifecycle 销毁这个沙箱。
func TestAfterCreateFailsWhenResetFails(t *testing.T) {
	sessions := newFakeSessionStore(pendingForkSession())
	runner := &fakeShellRunner{result: &sandbox.ExecuteResult{
		ExitCode: 128, Stderr: "fatal: bad object " + forkTestCommitSHA(),
	}}
	snapshots := &fakeSnapshotDeleter{}
	b := NewForkBootstrapper(sessions, newFakeMessageStore(nil), runner, snapshots)

	err := b.AfterCreate(context.Background(), forkKey(), fakeHandle{id: "sbx-2"})

	require.Error(t, err)
	// 引导失败时清空 bootstrap 并回收快照：下次 resolve 走全新沙箱路径。
	require.Equal(t, []string{"snap-1"}, snapshots.deleted)
	require.True(t, sessions.bootstrapCleared)
}

func TestAfterCreateRejectsInvalidCommitSHAWithoutExec(t *testing.T) {
	sessions := newFakeSessionStore(pendingForkSession())
	sessions.source.ForkBootstrap.CommitSHA = "HEAD; echo pwned"
	runner := &fakeShellRunner{result: &sandbox.ExecuteResult{ExitCode: 0}}
	snapshots := &fakeSnapshotDeleter{}
	b := NewForkBootstrapper(sessions, newFakeMessageStore(nil), runner, snapshots)

	err := b.AfterCreate(context.Background(), forkKey(), fakeHandle{id: "sbx-2"})

	require.Error(t, err)
	require.Empty(t, runner.calls)
	require.Contains(t, err.Error(), "invalid commit sha")
}

func TestForkResetScriptRejectsNonHexSHA(t *testing.T) {
	_, err := forkResetScript(sandbox.SessionWorkspaceRoot, "abc123")
	require.Error(t, err)
	_, err = forkResetScript(sandbox.SessionWorkspaceRoot, "")
	require.Error(t, err)
	_, err = forkResetScript(sandbox.SessionWorkspaceRoot, forkTestCommitSHA()+";rm")
	require.Error(t, err)
}

func TestForkResetScriptPrunesLaterCommits(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	// Git hooks export repository overrides. Use disposable paths to reproduce
	// that environment without risking the repository running this test.
	inheritedRepo := t.TempDir()
	t.Setenv("GIT_DIR", filepath.Join(inheritedRepo, "repo.git"))
	t.Setenv("GIT_WORK_TREE", inheritedRepo)
	t.Setenv("GIT_INDEX_FILE", filepath.Join(inheritedRepo, "index"))
	env := isolatedGitEnv(t)
	runGit := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
		return strings.TrimSpace(string(out))
	}

	runGit("init")
	_, err := os.Stat(filepath.Join(dir, ".git"))
	require.NoError(t, err, "git init must use the test directory")
	runGit("config", "user.email", "agent@weknora.local")
	runGit("config", "user.name", "WeKnora Agent")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("early"), 0o644))
	runGit("add", "-A")
	runGit("commit", "-m", "early")
	early := runGit("rev-parse", "HEAD")
	if !gitSHAPattern.MatchString(early) {
		t.Skip("host git is not using SHA-1 object names")
	}

	require.NoError(t, os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("later-secret"), 0o644))
	runGit("add", "-A")
	runGit("commit", "-m", "later")
	later := runGit("rev-parse", "HEAD")
	require.NotEqual(t, early, later)

	script, err := forkResetScript(dir, early)
	require.NoError(t, err)
	cmd := exec.Command("bash", "-c", script)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))

	cat := exec.Command("git", "-C", dir, "cat-file", "-t", later)
	cat.Env = env
	require.Error(t, cat.Run(), "later-turn commit must be unreachable after prune")
	_, err = os.Stat(filepath.Join(dir, "secret.txt"))
	require.True(t, os.IsNotExist(err))
	body, err := os.ReadFile(filepath.Join(dir, "keep.txt"))
	require.NoError(t, err)
	require.Equal(t, "early", string(body))
}

func isolatedGitEnv(t *testing.T) []string {
	t.Helper()
	// Neither the temporary repository commands nor the reset script may
	// inherit the caller's repository, index, object store or config overrides.
	env := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GIT_") {
			env = append(env, entry)
		}
	}
	return append(env,
		"GIT_CONFIG_GLOBAL="+filepath.Join(t.TempDir(), "gitconfig"),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=WeKnora Agent",
		"GIT_AUTHOR_EMAIL=agent@weknora.local",
		"GIT_COMMITTER_NAME=WeKnora Agent",
		"GIT_COMMITTER_EMAIL=agent@weknora.local",
	)
}

func TestWithClientNilReceiver(t *testing.T) {
	var b *ForkBootstrapper
	require.Nil(t, b.WithClient(&recordingRemoteClient{}))
}

func TestOnCreateFailedAbandonsWhenSnapshotIsGone(t *testing.T) {
	sessions := newFakeSessionStore(pendingForkSession())
	snapshots := &fakeSnapshotDeleter{}
	b := NewForkBootstrapper(sessions, nil, nil, snapshots)

	b.OnCreateFailed(context.Background(), forkKey(), sandbox.NewRemoteError(
		sandbox.SandboxTypeCube, "Create", sandbox.RemoteErrorKindNotFound, "template gone", nil,
	))

	require.True(t, sessions.bootstrapCleared)
	require.Equal(t, []string{"snap-1"}, snapshots.deleted)

	got, err := b.TemplateOverride(context.Background(), forkKey())
	require.NoError(t, err)
	require.Empty(t, got, "retry must boot an ordinary sandbox, not the dead snapshot")
}

func TestOnCreateFailedAbandonsWhenTemplateIsInvalid(t *testing.T) {
	sessions := newFakeSessionStore(pendingForkSession())
	snapshots := &fakeSnapshotDeleter{}
	b := NewForkBootstrapper(sessions, nil, nil, snapshots)

	// Create HTTP 404 is classified as invalid_request, not not_found.
	b.OnCreateFailed(context.Background(), forkKey(), sandbox.NewRemoteError(
		sandbox.SandboxTypeCube, "Create", sandbox.RemoteErrorKindInvalidRequest, "unknown template", nil,
	))

	require.True(t, sessions.bootstrapCleared)
	require.Equal(t, []string{"snap-1"}, snapshots.deleted)
}

func TestOnCreateFailedKeepsBootstrapOnTransientError(t *testing.T) {
	sessions := newFakeSessionStore(pendingForkSession())
	snapshots := &fakeSnapshotDeleter{}
	b := NewForkBootstrapper(sessions, nil, nil, snapshots)

	b.OnCreateFailed(context.Background(), forkKey(), sandbox.NewRemoteError(
		sandbox.SandboxTypeCube, "Create", sandbox.RemoteErrorKindUnavailable, "provider down", nil,
	))

	require.False(t, sessions.bootstrapCleared)
	require.Empty(t, snapshots.deleted)

	got, err := b.TemplateOverride(context.Background(), forkKey())
	require.NoError(t, err)
	require.Equal(t, "snap-1", got)
}

func TestOnCreateFailedIsNoOpWithoutPendingBootstrap(t *testing.T) {
	sessions := newFakeSessionStore(&types.Session{ID: "fork-1", TenantID: 1})
	snapshots := &fakeSnapshotDeleter{}
	b := NewForkBootstrapper(sessions, nil, nil, snapshots)

	b.OnCreateFailed(context.Background(), forkKey(), sandbox.NewRemoteError(
		sandbox.SandboxTypeCube, "Create", sandbox.RemoteErrorKindNotFound, "template gone", nil,
	))

	require.False(t, sessions.bootstrapCleared)
	require.Empty(t, snapshots.deleted)
}

func TestAfterCreateWithClientUsesHandleNotRunner(t *testing.T) {
	sessions := newFakeSessionStore(pendingForkSession())
	runner := &fakeShellRunner{result: &sandbox.ExecuteResult{ExitCode: 0}}
	client := &recordingRemoteClient{execResult: &sandbox.RemoteExecResult{ExitCode: 0}}
	snapshots := &fakeSnapshotDeleter{}
	b := NewForkBootstrapper(sessions, newFakeMessageStore(nil), runner, snapshots)

	bound, ok := b.WithClient(client).(*ForkBootstrapper)
	require.True(t, ok)
	require.Same(t, client, bound.client)

	require.NoError(t, bound.AfterCreate(context.Background(), forkKey(), fakeHandle{id: "sbx-2"}))

	require.Empty(t, runner.calls, "AfterCreate must not Resolve via the session runner")
	require.Len(t, client.execs, 1)
	require.True(t, client.execs[0].Shell)
	require.Equal(t, sandbox.SessionWorkspaceRoot, client.execs[0].WorkDir)
	require.Equal(t, sandbox.DefaultSandboxExecUser, client.execs[0].User)
	require.Equal(t, forkResetTimeout, client.execs[0].Timeout)
	require.Contains(t, client.execs[0].Command, "reset --hard "+forkTestCommitSHA())
	require.Contains(t, client.execs[0].Command, "gc --prune=now")
	require.Equal(t, []string{"snap-1"}, snapshots.deleted)
	require.True(t, sessions.updatedBootstrap.Consumed())
}

type recordingRemoteClient struct {
	execs      []sandbox.RemoteExecRequest
	execResult *sandbox.RemoteExecResult
	execErr    error
}

func (c *recordingRemoteClient) Provider() sandbox.RemoteProvider { return sandbox.SandboxTypeCube }
func (c *recordingRemoteClient) Capabilities() sandbox.RemoteSandboxCapabilities {
	return sandbox.RemoteSandboxCapabilities{}
}
func (c *recordingRemoteClient) Health(context.Context) error { return nil }
func (c *recordingRemoteClient) Create(
	context.Context, sandbox.RemoteCreateRequest,
) (sandbox.RemoteSandboxHandle, error) {
	panic("Create should not be called from AfterCreate")
}

func (c *recordingRemoteClient) Connect(
	context.Context, sandbox.RemoteConnectRequest,
) (sandbox.RemoteSandboxHandle, error) {
	panic("Connect should not be called from AfterCreate")
}

func (c *recordingRemoteClient) Get(context.Context, string) (*sandbox.RemoteSandboxSummary, error) {
	panic("Get should not be called from AfterCreate")
}

func (c *recordingRemoteClient) List(
	context.Context, sandbox.RemoteListFilter,
) ([]sandbox.RemoteSandboxSummary, error) {
	panic("List should not be called from AfterCreate")
}

func (c *recordingRemoteClient) Delete(context.Context, string) error {
	panic("Delete should not be called from AfterCreate")
}

func (c *recordingRemoteClient) Exec(
	_ context.Context, _ sandbox.RemoteSandboxHandle, req sandbox.RemoteExecRequest,
) (*sandbox.RemoteExecResult, error) {
	c.execs = append(c.execs, req)
	if c.execErr != nil {
		return nil, c.execErr
	}
	if c.execResult != nil {
		return c.execResult, nil
	}
	return &sandbox.RemoteExecResult{ExitCode: 0}, nil
}

func (c *recordingRemoteClient) WriteFile(context.Context, sandbox.RemoteSandboxHandle, string, []byte) error {
	panic("WriteFile should not be called from AfterCreate")
}

func (c *recordingRemoteClient) ReadFile(context.Context, sandbox.RemoteSandboxHandle, string) ([]byte, error) {
	panic("ReadFile should not be called from AfterCreate")
}

func (c *recordingRemoteClient) ListDir(
	context.Context, sandbox.RemoteSandboxHandle, string,
) ([]sandbox.RemoteDirEntry, error) {
	panic("ListDir should not be called from AfterCreate")
}

func (c *recordingRemoteClient) MakeDir(context.Context, sandbox.RemoteSandboxHandle, string) error {
	panic("MakeDir should not be called from AfterCreate")
}

func (c *recordingRemoteClient) Remove(context.Context, sandbox.RemoteSandboxHandle, string) error {
	panic("Remove should not be called from AfterCreate")
}

func (c *recordingRemoteClient) Stat(
	context.Context, sandbox.RemoteSandboxHandle, string,
) (*sandbox.RemoteStatEntry, error) {
	panic("Stat should not be called from AfterCreate")
}

var _ sandbox.RemoteSandboxClient = (*recordingRemoteClient)(nil)
