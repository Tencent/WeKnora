package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
)

type fakeDesktopShell struct {
	results []*sandbox.ExecuteResult
	errs    []error
	calls   []sandbox.ShellExecOptions
	cmds    []string
}

func (f *fakeDesktopShell) ExecShellCommandWithOptions(
	_ context.Context, _ string, command string, opts sandbox.ShellExecOptions,
) (*sandbox.ExecuteResult, error) {
	f.cmds = append(f.cmds, command)
	f.calls = append(f.calls, opts)
	i := len(f.cmds) - 1
	var err error
	if i < len(f.errs) {
		err = f.errs[i]
	}
	var res *sandbox.ExecuteResult
	if i < len(f.results) {
		res = f.results[i]
	}
	return res, err
}

func TestReadDesktopSecretHappyPathSingleExec(t *testing.T) {
	secret := "abcdefghijklmnopqrstuvwxyz012345"
	f := &fakeDesktopShell{results: []*sandbox.ExecuteResult{{
		Stdout: "READY " + secret + "\n", ExitCode: 0,
	}}}
	got, err := readDesktopSecret(context.Background(), f, "sess-1", "")
	require.NoError(t, err)
	require.Equal(t, secret, got)
	require.Len(t, f.cmds, 1)
	require.Equal(t, sandbox.DesktopEnsureCmd(), f.cmds[0])
	require.True(t, f.calls[0].SkipWorkspacePrep)
	require.Equal(t, desktopStartTimeout, f.calls[0].Timeout)
	require.False(t, f.calls[0].AsRoot)
	require.False(t, f.calls[0].AllowSkillsRoot)
}

func TestReadDesktopSecretMissingScriptDoesNotReset(t *testing.T) {
	f := &fakeDesktopShell{results: []*sandbox.ExecuteResult{{
		ExitCode: sandbox.DesktopEnsureUnsupportedExit,
		Stderr:   sandbox.DesktopEnsureUnsupportedMarker + "\n",
	}}}
	_, err := readDesktopSecret(context.Background(), f, "sess-1", "")
	require.ErrorIs(t, err, ErrDesktopUnsupported)
	require.Len(t, f.cmds, 1)
}

func TestReadDesktopSecretBareExit2IsStartFailed(t *testing.T) {
	f := &fakeDesktopShell{results: []*sandbox.ExecuteResult{
		{ExitCode: 2, Stderr: "Syntax error: end of file unexpected\n"},
		{ExitCode: 0},
		{ExitCode: 2, Stderr: "Syntax error: end of file unexpected\n"},
	}}
	_, err := readDesktopSecret(context.Background(), f, "sess-1", "")
	require.ErrorIs(t, err, ErrDesktopStartFailed)
	require.Equal(t, sandbox.DesktopResetListenersCmd(), f.cmds[1])
	require.Len(t, f.cmds, 3)
}

func TestReadDesktopSecretTimeoutResetsThenRetries(t *testing.T) {
	secret := "abcdefghijklmnopqrstuvwxyz012345"
	f := &fakeDesktopShell{
		results: []*sandbox.ExecuteResult{
			{ExitCode: -1, Killed: true, Error: "timeout"},
			{ExitCode: 0},
			{Stdout: "READY " + secret + "\n", ExitCode: 0},
		},
	}
	got, err := readDesktopSecret(context.Background(), f, "sess-1", "")
	require.NoError(t, err)
	require.Equal(t, secret, got)
	require.Equal(t, sandbox.DesktopEnsureCmd(), f.cmds[0])
	require.Equal(t, sandbox.DesktopResetListenersCmd(), f.cmds[1])
	require.Equal(t, sandbox.DesktopEnsureCmd(), f.cmds[2])
	require.True(t, f.calls[0].SkipWorkspacePrep)
	require.True(t, f.calls[1].SkipWorkspacePrep)
	require.Equal(t, desktopProbeTimeout, f.calls[1].Timeout)
	require.Equal(t, desktopStartTimeout, f.calls[2].Timeout)
}

func TestReadDesktopSecretResetExecError(t *testing.T) {
	f := &fakeDesktopShell{
		results: []*sandbox.ExecuteResult{{ExitCode: 1, Stderr: "boom"}},
		errs:    []error{nil, errors.New("reset failed")},
	}
	_, err := readDesktopSecret(context.Background(), f, "sess-1", "")
	require.ErrorIs(t, err, ErrDesktopStartFailed)
	require.Len(t, f.cmds, 2)
}

func TestDesktopExecDiagnosticOmitsStdout(t *testing.T) {
	got := desktopExecDiagnostic(&sandbox.ExecuteResult{
		ExitCode: 1,
		Stdout:   "READY abcdefghijklmnopqrstuvwxyz012345\n",
		Stderr:   "x11vnc dead",
	})
	require.Contains(t, got, "x11vnc dead")
	require.NotContains(t, got, "READY")
	require.NotContains(t, got, "abcdefghijklmnopqrstuvwxyz012345")

	got = desktopExecDiagnostic(&sandbox.ExecuteResult{
		ExitCode: 1,
		Stdout:   "READY abcdefghijklmnopqrstuvwxyz012345\n",
	})
	require.NotContains(t, got, "READY")
	require.True(t, strings.HasPrefix(got, "exit=1"))
}

func TestDesktopExecDiagnosticKilledUsesError(t *testing.T) {
	got := desktopExecDiagnostic(&sandbox.ExecuteResult{
		ExitCode: -1,
		Killed:   true,
		Error:    sandbox.ErrTimeout.Error(),
		Stdout:   "READY abcdefghijklmnopqrstuvwxyz012345\n",
	})
	require.Contains(t, got, "killed=true")
	require.Contains(t, got, sandbox.ErrTimeout.Error())
	require.NotContains(t, got, "READY")
	require.NotContains(t, got, "abcdefghijklmnopqrstuvwxyz012345")
}

func TestReadDesktopSecretRejectsUnparseableSuccess(t *testing.T) {
	f := &fakeDesktopShell{results: []*sandbox.ExecuteResult{
		{ExitCode: 0, Stdout: "started\n"},
		{ExitCode: 0},
		{ExitCode: 0, Stdout: "started\n"},
	}}
	_, err := readDesktopSecret(context.Background(), f, "sess-1", "")
	require.ErrorIs(t, err, ErrDesktopStartFailed)
	require.Equal(t, sandbox.DesktopResetListenersCmd(), f.cmds[1])
}

type stubDesktopProvider struct {
	advertised bool
}

func (s stubDesktopProvider) SessionDesktopManager() sandbox.SessionDesktopManager {
	if !s.advertised {
		return nil
	}
	return stubDesktopManager{}
}

type stubDesktopManager struct{}

func (stubDesktopManager) OpenSessionDesktop(
	context.Context, string, sandbox.RemoteDesktopOptions,
) (*sandbox.SessionDesktopConn, error) {
	return nil, sandbox.ErrDesktopUnsupported
}

func TestRequireDesktopCapableRejectsWhenNotAdvertised(t *testing.T) {
	require.ErrorIs(t, requireDesktopCapable(struct{}{}), ErrDesktopUnsupported)
	require.ErrorIs(t, requireDesktopCapable(stubDesktopProvider{}), ErrDesktopUnsupported)
}

func TestRequireDesktopCapableAcceptsAdvertisedManager(t *testing.T) {
	require.NoError(t, requireDesktopCapable(stubDesktopProvider{advertised: true}))
}

const desktopTestSecret = "abcdefghijklmnopqrstuvwxyz012345"

func desktopReadyResult() *sandbox.ExecuteResult {
	return &sandbox.ExecuteResult{Stdout: "READY " + desktopTestSecret + "\n", ExitCode: 0}
}

// orderDesktopManager records the Ensure path. HoldSessionOperation's release
// is part of the order: it must run after the dial, not before the machine
// is named.
type orderDesktopManager struct {
	mu    sync.Mutex
	order []string
}

func (m *orderDesktopManager) note(step string) {
	m.mu.Lock()
	m.order = append(m.order, step)
	m.mu.Unlock()
}

func (m *orderDesktopManager) steps() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.order))
	copy(out, m.order)
	return out
}

func (m *orderDesktopManager) Execute(context.Context, *sandbox.ExecuteConfig) (*sandbox.ExecuteResult, error) {
	return nil, nil
}
func (m *orderDesktopManager) Cleanup(context.Context) error { return nil }
func (m *orderDesktopManager) GetSandbox() sandbox.Sandbox   { return nil }
func (m *orderDesktopManager) GetType() sandbox.SandboxType  { return sandbox.SandboxTypeCube }

func (m *orderDesktopManager) SessionDesktopManager() sandbox.SessionDesktopManager { return m }

func (m *orderDesktopManager) HoldSessionOperation(ctx context.Context, _, _ string) (context.Context, func(), error) {
	m.note("HoldSessionOperation")
	return ctx, func() { m.note("release") }, nil
}

func (m *orderDesktopManager) DesktopMachine(context.Context, string) (string, string, error) {
	m.note("DesktopMachine")
	return "machine-1", "sb-1", nil
}

func (m *orderDesktopManager) ExecShellCommandWithOptions(
	context.Context, string, string, sandbox.ShellExecOptions,
) (*sandbox.ExecuteResult, error) {
	m.note("ExecShellCommandWithOptions")
	return desktopReadyResult(), nil
}

func (m *orderDesktopManager) OpenSessionDesktop(
	context.Context, string, sandbox.RemoteDesktopOptions,
) (*sandbox.SessionDesktopConn, error) {
	m.note("OpenSessionDesktop")
	return &sandbox.SessionDesktopConn{SandboxID: "sb-1"}, nil
}

type fixedDesktopResolver struct {
	mgr sandbox.Manager
}

func (r *fixedDesktopResolver) Resolve(context.Context, uint64, string) (sandbox.Manager, error) {
	return r.mgr, nil
}

func newEnsuredDesktopService(t *testing.T, mgr sandbox.Manager) *SandboxDesktopService {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.Session{}))
	require.NoError(t, db.Model(&types.Session{}).Create(map[string]any{
		"id": "sess-1", "tenant_id": uint64(42),
	}).Error)
	pinner := NewSessionSandboxPinner(db)
	_, err = pinner.Pin(context.Background(), "sess-1", SandboxPin{ConfigID: "cfg-desk", TenantID: 42})
	require.NoError(t, err)
	return NewSandboxDesktopService(NewSandboxTerminalService(
		pinner, &fixedDesktopResolver{mgr: mgr}, nil, nil, HostSandboxManager{},
	))
}

func TestEnsureSessionDesktopHoldsTheOperationBeforeReadingTheMachine(t *testing.T) {
	mgr := &orderDesktopManager{}
	svc := newEnsuredDesktopService(t, mgr)
	_, err := svc.EnsureSessionDesktop(context.Background(), "sess-1", SandboxPin{})
	require.NoError(t, err)
	require.Equal(t, []string{
		"HoldSessionOperation",
		"DesktopMachine",
		"ExecShellCommandWithOptions",
		"OpenSessionDesktop",
		"release",
	}, mgr.steps())
}

// pinnedDesktopManager is a located target's manager. It records every shell
// option and dials whatever sandbox the test names.
type pinnedDesktopManager struct {
	calls         []sandbox.ShellExecOptions
	dialSandboxID string
	conn          *websocket.Conn
	results       []*sandbox.ExecuteResult
}

func (m *pinnedDesktopManager) Execute(context.Context, *sandbox.ExecuteConfig) (*sandbox.ExecuteResult, error) {
	return nil, nil
}
func (m *pinnedDesktopManager) Cleanup(context.Context) error { return nil }
func (m *pinnedDesktopManager) GetSandbox() sandbox.Sandbox   { return nil }
func (m *pinnedDesktopManager) GetType() sandbox.SandboxType  { return sandbox.SandboxTypeCube }
func (m *pinnedDesktopManager) SessionDesktopManager() sandbox.SessionDesktopManager {
	return m
}

func (m *pinnedDesktopManager) ExecShellCommandWithOptions(
	_ context.Context, _ string, _ string, opts sandbox.ShellExecOptions,
) (*sandbox.ExecuteResult, error) {
	m.calls = append(m.calls, opts)
	i := len(m.calls) - 1
	if i < len(m.results) && m.results[i] != nil {
		return m.results[i], nil
	}
	return desktopReadyResult(), nil
}

func (m *pinnedDesktopManager) OpenSessionDesktop(
	context.Context, string, sandbox.RemoteDesktopOptions,
) (*sandbox.SessionDesktopConn, error) {
	return &sandbox.SessionDesktopConn{Conn: m.conn, SandboxID: m.dialSandboxID}, nil
}

func TestStartSessionDesktopPinsEveryStepToTheFixedSandbox(t *testing.T) {
	mgr := &pinnedDesktopManager{
		dialSandboxID: "sb-1",
		results: []*sandbox.ExecuteResult{
			{ExitCode: 1, Stderr: "x11vnc dead"},
			{ExitCode: 0},
			desktopReadyResult(),
		},
	}
	svc := &SandboxDesktopService{}
	_, err := svc.StartSessionDesktop(context.Background(), &DesktopTarget{
		SessionID: "sess-1", SandboxID: "sb-1", mgr: mgr,
	})
	require.NoError(t, err)
	require.NotEmpty(t, mgr.calls)
	for _, opts := range mgr.calls {
		require.Equal(t, "sb-1", opts.ExpectedSandboxID)
	}
}

func TestStartSessionDesktopRejectsADialToAnotherSandbox(t *testing.T) {
	client, server := desktopTestConn(t)
	mgr := &pinnedDesktopManager{dialSandboxID: "sb-2", conn: server}
	svc := &SandboxDesktopService{}
	_, err := svc.StartSessionDesktop(context.Background(), &DesktopTarget{
		SessionID: "sess-1", SandboxID: "sb-1", mgr: mgr,
	})
	require.ErrorIs(t, err, sandbox.ErrNoLiveSessionSandbox)
	require.NoError(t, client.SetReadDeadline(time.Now().Add(time.Second)))
	_, _, readErr := client.ReadMessage()
	var netErr interface{ Timeout() bool }
	require.False(t, errors.As(readErr, &netErr) && netErr.Timeout(),
		"a dial to another sandbox must close the connection, not leave it open until the deadline")
	require.Error(t, readErr, "a dial to another sandbox must close the connection")
}

func desktopTestConn(t *testing.T) (client, server *websocket.Conn) {
	t.Helper()
	accepted := make(chan *websocket.Conn, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		accepted <- conn
	}))
	t.Cleanup(srv.Close)
	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	server = <-accepted
	t.Cleanup(func() { _ = server.Close() })
	return client, server
}
