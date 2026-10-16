package localsandbox

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type fakeBackend struct {
	mu          sync.Mutex
	prepared    []string
	spawnCmd    Command
	stdout      string
	stderr      string
	exit        ExitStatus
	spawnErr    error
	available   error
	ensureReady error
	calls       []string
	proc        Process
	onSpawn     func()
}

func (f *fakeBackend) Name() string { return "fake" }
func (f *fakeBackend) Available() error {
	f.mu.Lock()
	f.calls = append(f.calls, "available")
	err := f.available
	f.mu.Unlock()
	return err
}

func (f *fakeBackend) EnsureReady(context.Context) error {
	f.mu.Lock()
	f.calls = append(f.calls, "ready")
	err := f.ensureReady
	f.mu.Unlock()
	return err
}
func (f *fakeBackend) TearDown(context.Context) error { return nil }

type fakePrepared struct{ fp string }

func (p fakePrepared) Fingerprint() string { return p.fp }
func (p fakePrepared) Close() error        { return nil }

func (f *fakeBackend) Prepare(_ context.Context, p Policy) (Prepared, error) {
	f.mu.Lock()
	f.calls = append(f.calls, "prepare")
	f.prepared = append(f.prepared, p.Fingerprint())
	f.mu.Unlock()
	return fakePrepared{fp: p.Fingerprint()}, nil
}

func (f *fakeBackend) Spawn(_ context.Context, _ Prepared, cmd Command) (Process, error) {
	if f.onSpawn != nil {
		f.onSpawn()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.spawnErr != nil {
		return nil, f.spawnErr
	}
	f.spawnCmd = cmd
	if f.proc != nil {
		return f.proc, nil
	}
	return &fakeProcess{
		stdout: io.NopCloser(bytes.NewBufferString(f.stdout)),
		stderr: io.NopCloser(bytes.NewBufferString(f.stderr)),
		exit:   f.exit,
	}, nil
}

type fakeProcess struct {
	stdout io.ReadCloser
	stderr io.ReadCloser
	exit   ExitStatus
}

func (p *fakeProcess) Stdout() io.Reader                        { return p.stdout }
func (p *fakeProcess) Stderr() io.Reader                        { return p.stderr }
func (p *fakeProcess) Kill() error                              { return nil }
func (p *fakeProcess) PID() int                                 { return 1 }
func (p *fakeProcess) Wait(context.Context) (ExitStatus, error) { return p.exit, nil }

type fakeProjects struct{}

func (fakeProjects) ProjectDirForSession(context.Context, string) (string, bool, error) {
	return "", false, nil
}

type fixedProject string

func (p fixedProject) ProjectDirForSession(context.Context, string) (string, bool, error) {
	return string(p), true, nil
}

type fixedMode ApprovalMode

func (m fixedMode) ModeForSession(context.Context, string) ApprovalMode {
	return ApprovalMode(m)
}

func serviceFixture(t *testing.T, backend *fakeBackend, mode ApprovalMode) *Service {
	t.Helper()
	return serviceFixtureWithProjects(t, backend, mode, fakeProjects{})
}

func serviceFixtureWithProjects(
	t *testing.T, backend *fakeBackend, mode ApprovalMode, projects ProjectLookup,
) *Service {
	t.Helper()
	base := t.TempDir()
	home := filepath.Join(base, "home")
	appData := filepath.Join(home, "App Support", "WeKnora Lite")
	resolver := NewWorkspaceResolver(DirLayout{
		SessionRoot: filepath.Join(home, "Documents", "WeKnora"),
	}, projects)
	return NewService(backend, resolver, NewPolicyBuilder(home, appData), fixedMode(mode))
}

func TestServiceRunReturnsOutput(t *testing.T) {
	backend := &fakeBackend{stdout: "hello\n", exit: ExitStatus{Code: 0}}
	svc := serviceFixture(t, backend, ModeAuto)

	res, err := svc.Run(context.Background(), RunRequest{SessionID: "s1", Command: "echo hello"})
	require.NoError(t, err)
	require.Equal(t, "hello\n", res.Stdout)
	require.Equal(t, 0, res.Exit.Code)
	require.False(t, res.Denial.IsDenied())
}

// shell_exec caps the model-visible slice, but only after the executor has
// already returned the whole string. On host the producer is a local process
// and the consumer is the desktop app itself, so the cap has to be here.
func TestServiceRunCapsHugeStdout(t *testing.T) {
	huge := strings.Repeat("a", maxCapturedStreamBytes*3)
	backend := &fakeBackend{stdout: huge, exit: ExitStatus{Code: 0}}
	svc := serviceFixture(t, backend, ModeAuto)

	res, err := svc.Run(context.Background(), RunRequest{SessionID: "s1", Command: "cat big"})
	require.NoError(t, err)
	require.Less(t, len(res.Stdout), len(huge))
	require.LessOrEqual(t, len(res.Stdout), maxCapturedStreamBytes+128)
	require.Contains(t, res.Stdout, "truncated")
}

func TestServiceRunCapsHugeStderr(t *testing.T) {
	huge := strings.Repeat("e", maxCapturedStreamBytes*2)
	backend := &fakeBackend{stderr: huge, exit: ExitStatus{Code: 1}}
	svc := serviceFixture(t, backend, ModeAuto)

	res, err := svc.Run(context.Background(), RunRequest{SessionID: "s1", Command: "noisy"})
	require.NoError(t, err)
	require.LessOrEqual(t, len(res.Stderr), maxCapturedStreamBytes+128)
}

// Truncating must not stop reading: a child blocked writing into a full pipe
// never exits, and Wait would hang behind it.
func TestServiceRunDrainsPipeAfterCap(t *testing.T) {
	huge := strings.Repeat("a", maxCapturedStreamBytes*2)
	counter := &countingReader{inner: strings.NewReader(huge)}
	backend := &fakeBackend{proc: &fakeProcess{
		stdout: io.NopCloser(counter),
		stderr: io.NopCloser(strings.NewReader("")),
	}}
	svc := serviceFixture(t, backend, ModeAuto)

	_, err := svc.Run(context.Background(), RunRequest{SessionID: "s1", Command: "cat big"})
	require.NoError(t, err)
	require.Equal(t, len(huge), counter.read, "the pipe must be drained to EOF")
}

type countingReader struct {
	inner io.Reader
	read  int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.inner.Read(p)
	c.read += n
	return n, err
}

func TestServiceRunClassifiesDenial(t *testing.T) {
	backend := &fakeBackend{
		stderr: "touch: /etc/x: Operation not permitted",
		exit:   ExitStatus{Code: 1},
	}
	svc := serviceFixture(t, backend, ModeAuto)

	res, err := svc.Run(context.Background(), RunRequest{SessionID: "s1", Command: "touch /etc/x"})
	require.NoError(t, err)
	require.True(t, res.Denial.IsDenied())
	require.Equal(t, DenialOperationNotPermitted, res.Denial.Reason)
}

// Full access is the one mode that must never reach Prepare.
func TestServiceRunRejectsFullAccessUntilImplemented(t *testing.T) {
	backend := &fakeBackend{}
	svc := serviceFixture(t, backend, ModeFull)

	_, err := svc.Run(context.Background(), RunRequest{SessionID: "s1", Command: "echo hi"})
	require.ErrorIs(t, err, ErrApprovalModeNotShipped)
	require.Empty(t, backend.prepared)
}

// Ask compiles to a perfectly valid policy with nothing writable, so without
// the approval loop above it every write fails with a bare path-denied error
// and no explanation. Refuse the turn instead of running it half-disabled.
func TestServiceRunRefusesModeItCannotEnforce(t *testing.T) {
	backend := &fakeBackend{}
	svc := serviceFixture(t, backend, ModeAsk)

	_, err := svc.Run(context.Background(), RunRequest{SessionID: "s1", Command: "echo hi"})
	require.ErrorIs(t, err, ErrApprovalModeNotShipped)
	require.Empty(t, backend.prepared)
}

// Substituting a mode the caller did not ask for is the failure this guards:
// ask is stricter than auto, so quietly running auto would widen access.
func TestServiceRunDoesNotSubstituteAutoForAsk(t *testing.T) {
	backend := &fakeBackend{exit: ExitStatus{Code: 0}}
	svc := serviceFixture(t, backend, ModeAsk)

	res, err := svc.Run(context.Background(), RunRequest{SessionID: "s1", Command: "echo hi"})
	require.Error(t, err)
	require.Nil(t, res)
}

func TestServiceRunRunsInWorkspace(t *testing.T) {
	backend := &fakeBackend{exit: ExitStatus{Code: 0}}
	svc := serviceFixture(t, backend, ModeAuto)

	_, err := svc.Run(context.Background(), RunRequest{SessionID: "s1", Command: "pwd"})
	require.NoError(t, err)

	ws, _, err := svc.workspaceFor(context.Background(), "s1")
	require.NoError(t, err)
	require.Equal(t, ws.Root, backend.spawnCmd.Cwd)
}

// work_dir is a convenience, not a privilege boundary: it must stay inside
// the workspace or the call fails before anything is spawned.
func TestServiceRunRejectsWorkDirOutsideWorkspace(t *testing.T) {
	backend := &fakeBackend{}
	svc := serviceFixture(t, backend, ModeAuto)

	_, err := svc.Run(context.Background(), RunRequest{
		SessionID: "s1", Command: "ls", WorkDir: "/etc",
	})
	require.ErrorIs(t, err, ErrPathDenied)
}

func TestServiceRunAppliesDefaultTimeout(t *testing.T) {
	backend := &fakeBackend{exit: ExitStatus{Code: 0}}
	svc := serviceFixture(t, backend, ModeAuto)

	res, err := svc.Run(context.Background(), RunRequest{
		SessionID: "s1", Command: "sleep 0", Timeout: 0,
	})
	require.NoError(t, err)
	require.NotNil(t, res)
}

func TestServiceGuardMatchesPolicy(t *testing.T) {
	backend := &fakeBackend{}
	svc := serviceFixture(t, backend, ModeAuto)

	guard, ws, err := svc.GuardForSession(context.Background(), "s1")
	require.NoError(t, err)

	_, err = guard.CheckWrite(filepath.Join(ws.Root, "file.txt"))
	require.NoError(t, err)
	_, err = guard.CheckWrite(filepath.Join(filepath.Dir(ws.Root), "outside.txt"))
	require.ErrorIs(t, err, ErrPathDenied)
}

type recordingProcess struct {
	stdout         io.ReadCloser
	stderr         io.ReadCloser
	exit           ExitStatus
	blockUntilKill bool

	mu        sync.Mutex
	kills     int
	killed    chan struct{}
	closeOnce sync.Once
}

func newRecordingProcess(exit ExitStatus) *recordingProcess {
	return &recordingProcess{
		stdout: io.NopCloser(bytes.NewBuffer(nil)),
		stderr: io.NopCloser(bytes.NewBuffer(nil)),
		exit:   exit,
		killed: make(chan struct{}),
	}
}

func (p *recordingProcess) Stdout() io.Reader { return p.stdout }
func (p *recordingProcess) Stderr() io.Reader { return p.stderr }
func (p *recordingProcess) PID() int          { return 1 }

func (p *recordingProcess) Kill() error {
	p.mu.Lock()
	p.kills++
	p.mu.Unlock()
	p.closeOnce.Do(func() { close(p.killed) })
	return nil
}

func (p *recordingProcess) killCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.kills
}

func (p *recordingProcess) Wait(context.Context) (ExitStatus, error) {
	if !p.blockUntilKill {
		return p.exit, nil
	}
	<-p.killed
	return ExitStatus{Code: -1, Killed: true}, nil
}

func TestServiceRunDoesNotKillAfterSuccess(t *testing.T) {
	proc := newRecordingProcess(ExitStatus{Code: 0})
	backend := &fakeBackend{proc: proc}
	svc := serviceFixture(t, backend, ModeAuto)

	res, err := svc.Run(context.Background(), RunRequest{SessionID: "s1", Command: "echo hi"})
	require.NoError(t, err)
	require.Equal(t, 0, res.Exit.Code)

	// The deferred timeout cancel must not race a leftover watcher into Kill.
	time.Sleep(20 * time.Millisecond)
	require.Equal(t, 0, proc.killCount())
}

func TestWatchKillSkipsWhenDoneAlreadyClosed(t *testing.T) {
	proc := newRecordingProcess(ExitStatus{Code: 0})
	done := make(chan struct{})
	close(done)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	watchKill(ctx, done, proc)
	require.Equal(t, 0, proc.killCount())
}

func TestServiceRunCallsEnsureReadyBeforePrepare(t *testing.T) {
	backend := &fakeBackend{exit: ExitStatus{Code: 0}}
	svc := serviceFixture(t, backend, ModeAuto)

	_, err := svc.Run(context.Background(), RunRequest{SessionID: "s1", Command: "echo hi"})
	require.NoError(t, err)
	require.Equal(t, []string{"available", "ready", "prepare"}, backend.calls)
}

func TestServiceRunKillsOnParentCancel(t *testing.T) {
	proc := newRecordingProcess(ExitStatus{Code: 0})
	proc.blockUntilKill = true
	backend := &fakeBackend{proc: proc}
	svc := serviceFixture(t, backend, ModeAuto)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	res, err := svc.Run(ctx, RunRequest{SessionID: "s1", Command: "sleep 60"})
	require.NoError(t, err)
	require.True(t, res.Exit.Killed)
	require.Greater(t, proc.killCount(), 0)
}

func TestServiceRunFailsWhenBackendUnavailable(t *testing.T) {
	backend := &fakeBackend{available: ErrUnsupportedPlatform}
	svc := serviceFixture(t, backend, ModeAuto)

	_, err := svc.Run(context.Background(), RunRequest{SessionID: "s1", Command: "echo hi"})
	require.ErrorIs(t, err, ErrUnsupportedPlatform)
}

func TestServiceRunUsesNonLoginShell(t *testing.T) {
	backend := &fakeBackend{exit: ExitStatus{Code: 0}}
	svc := serviceFixture(t, backend, ModeAuto)
	_, err := svc.Run(context.Background(), RunRequest{SessionID: "s1", Command: "echo hi"})
	require.NoError(t, err)
	require.Equal(t, []string{"/bin/bash", "--noprofile", "--norc", "-c", "echo hi"}, backend.spawnCmd.Argv)
}

func TestServiceRunDoesNotPassHostSecrets(t *testing.T) {
	t.Setenv("AWS_SECRET_ACCESS_KEY", "super-secret")
	t.Setenv("GITHUB_TOKEN", "gho_secret")
	backend := &fakeBackend{exit: ExitStatus{Code: 0}}
	svc := serviceFixture(t, backend, ModeAuto)

	_, err := svc.Run(context.Background(), RunRequest{
		SessionID: "s1",
		Command:   "printenv",
		Env:       map[string]string{"FOO": "bar"},
	})
	require.NoError(t, err)
	joined := strings.Join(envMapToSlice(backend.spawnCmd.Env), "\n")
	require.Contains(t, joined, "FOO=bar")
	require.NotContains(t, joined, "super-secret")
	require.NotContains(t, joined, "GITHUB_TOKEN")
}

func envMapToSlice(env map[string]string) []string {
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}

func TestPreviewCommandMasksInlineAssignments(t *testing.T) {
	got := previewCommand(`export TOKEN="sk-secret"; FOO=bare ./run --model cogview-4`)
	require.NotContains(t, got, "sk-secret")
	require.NotContains(t, got, "bare")
	require.Contains(t, got, "TOKEN=***")
	require.Contains(t, got, "FOO=***")
	require.Contains(t, got, "--model cogview-4")
}

// Two sessions on the same project share one directory. Overlapping Run
// would let them stomp each other's files and git state.
func TestServiceRunSerializesSharedProjectRoot(t *testing.T) {
	project := t.TempDir()
	require.NoError(t, os.MkdirAll(project, 0o755))

	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	backend := &fakeBackend{
		exit: ExitStatus{Code: 0},
		onSpawn: func() {
			entered <- struct{}{}
			<-release
		},
	}
	svc := serviceFixtureWithProjects(t, backend, ModeAuto, fixedProject(project))

	errCh := make(chan error, 2)
	for _, id := range []string{"s1", "s2"} {
		go func(sessionID string) {
			_, err := svc.Run(context.Background(), RunRequest{SessionID: sessionID, Command: "pwd"})
			errCh <- err
		}(id)
	}

	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first command never reached Spawn")
	}
	select {
	case <-entered:
		t.Fatal("second command entered Spawn while the first still held the workspace")
	case <-time.After(80 * time.Millisecond):
	}
	close(release)

	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("second command never reached Spawn after the first released")
	}
	require.NoError(t, <-errCh)
	require.NoError(t, <-errCh)
}

// Distinct session workspaces must not wait on each other.
func TestServiceRunAllowsConcurrentDistinctRoots(t *testing.T) {
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	backend := &fakeBackend{
		exit: ExitStatus{Code: 0},
		onSpawn: func() {
			entered <- struct{}{}
			<-release
		},
	}
	svc := serviceFixture(t, backend, ModeAuto)

	errCh := make(chan error, 2)
	for _, id := range []string{"s1", "s2"} {
		go func(sessionID string) {
			_, err := svc.Run(context.Background(), RunRequest{SessionID: sessionID, Command: "pwd"})
			errCh <- err
		}(id)
	}

	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatalf("command %d never reached Spawn concurrently", i+1)
		}
	}
	close(release)
	require.NoError(t, <-errCh)
	require.NoError(t, <-errCh)
}

func installPolicyFixture(t *testing.T) Policy {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "skills", ".versions", "pdf-1")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	return Policy{
		Cwd:           dir,
		WritableRoots: []WritableRoot{{Path: dir}},
		Network:       NetworkUnrestricted,
	}
}

func TestServiceRunWithPolicyUsesCallerPolicy(t *testing.T) {
	backend := &fakeBackend{stdout: "ok", exit: ExitStatus{Code: 0}}
	svc := serviceFixture(t, backend, ModeAuto)
	p := installPolicyFixture(t)

	res, err := svc.RunWithPolicy(context.Background(), p, RunRequest{SessionID: "install-1", Command: "uv --version"})
	require.NoError(t, err)
	require.Equal(t, "ok", res.Stdout)
	require.Equal(t, p.Cwd, backend.spawnCmd.Cwd)
	require.Equal(t, []string{p.Fingerprint()}, backend.prepared)
}

func TestServiceRunWithPolicyKeepsWorkDirInsidePolicy(t *testing.T) {
	backend := &fakeBackend{}
	svc := serviceFixture(t, backend, ModeAuto)
	_, err := svc.RunWithPolicy(context.Background(), installPolicyFixture(t),
		RunRequest{Command: "ls", WorkDir: "/etc"})
	require.ErrorIs(t, err, ErrPathDenied)
	require.Empty(t, backend.prepared)
}

func TestServiceRunWithPolicyRejectsInvalidPolicy(t *testing.T) {
	backend := &fakeBackend{}
	svc := serviceFixture(t, backend, ModeAuto)
	_, err := svc.RunWithPolicy(context.Background(), Policy{Cwd: "relative"}, RunRequest{Command: "ls"})
	require.Error(t, err)
	require.Empty(t, backend.prepared)
}
