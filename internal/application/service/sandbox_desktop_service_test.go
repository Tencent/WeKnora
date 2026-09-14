package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/sandbox"
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
	got, err := readDesktopSecret(context.Background(), f, "sess-1")
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
	_, err := readDesktopSecret(context.Background(), f, "sess-1")
	require.ErrorIs(t, err, ErrDesktopUnsupported)
	require.Len(t, f.cmds, 1)
}

func TestReadDesktopSecretBareExit2IsStartFailed(t *testing.T) {
	f := &fakeDesktopShell{results: []*sandbox.ExecuteResult{
		{ExitCode: 2, Stderr: "Syntax error: end of file unexpected\n"},
		{ExitCode: 0},
		{ExitCode: 2, Stderr: "Syntax error: end of file unexpected\n"},
	}}
	_, err := readDesktopSecret(context.Background(), f, "sess-1")
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
	got, err := readDesktopSecret(context.Background(), f, "sess-1")
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
	_, err := readDesktopSecret(context.Background(), f, "sess-1")
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
	_, err := readDesktopSecret(context.Background(), f, "sess-1")
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
