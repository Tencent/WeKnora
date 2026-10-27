//go:build desktop

package service

import (
	"context"
	"io"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/background"
	"github.com/Tencent/WeKnora/internal/localsandbox"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type drainingShellProcess struct {
	cmd            *exec.Cmd
	stdout, stderr io.ReadCloser
}

func (p *drainingShellProcess) Stdout() io.Reader { return p.stdout }
func (p *drainingShellProcess) Stderr() io.Reader { return p.stderr }
func (p *drainingShellProcess) PID() int          { return p.cmd.Process.Pid }
func (p *drainingShellProcess) Kill() error       { return p.cmd.Process.Kill() }
func (p *drainingShellProcess) Wait(context.Context) (localsandbox.ExitStatus, error) {
	err := p.cmd.Wait()
	return localsandbox.ExitStatus{Code: p.cmd.ProcessState.ExitCode()}, err
}

type drainingShellStarter struct{ root string }

func (s drainingShellStarter) GuardForSession(
	context.Context,
	string,
) (*localsandbox.PathGuard, localsandbox.Workspace, error) {
	return nil, localsandbox.Workspace{Root: s.root}, nil
}

func (s drainingShellStarter) StartForSession(
	ctx context.Context,
	r localsandbox.RunRequest,
) (localsandbox.Process, error) {
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", r.Command)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	return &drainingShellProcess{cmd: cmd, stdout: stdout, stderr: stderr}, nil
}

func TestLocalShellJobDrainsPipesBeforeWait(t *testing.T) {
	base := t.TempDir()
	bg := NewBackgroundWork(background.NewMemoryActivityRegistry(nil), background.NewMemoryNoticeInbox(nil))
	svc, err := newLocalShellJobService(drainingShellStarter{root: base}, bg, nil, filepath.Join(base, "jobs"),
		func(context.Context, int) (int64, error) { return 1, nil })
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, svc.Shutdown(context.Background())) })
	result, err := svc.SubmitShellJob(
		context.Background(),
		interfaces.SessionRef{TenantID: 7, SessionID: "s"},
		tools.ShellJobSubmitRequest{
			Command:   "printf BEGIN; printf BEGIN-ERR >&2; sleep 0.1; printf FINAL-DIAGNOSTIC; printf FINAL-ERROR >&2",
			YieldTime: 2 * time.Second,
			OnOutput:  func(string, []byte) { time.Sleep(400 * time.Millisecond) },
		},
	)
	require.NoError(t, err)
	p := result.Result.Payload.(ShellJobPayload)
	require.Equal(t, interfaces.WorkStateSucceeded, p.State)
	require.Contains(t, p.Stderr, "FINAL-ERROR")
	require.Contains(
		t,
		p.Stdout,
		"FINAL-DIAGNOSTIC",
		"Wait closes the pipes while the output copier is still forwarding an earlier chunk",
	)
}

func TestLocalShellJobFailedDeletionReopensSubmissions(t *testing.T) {
	svc, proc, _ := localFixture(t)
	ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
	require.NoError(t, svc.OnDelete(t.Context(), ref))
	_, err := svc.SubmitShellJob(t.Context(), ref, tools.ShellJobSubmitRequest{Command: "true"})
	require.Error(t, err)
	require.NoError(t, svc.OnDeleteAborted(t.Context(), ref))
	proc.finish()
	result, err := svc.SubmitShellJob(
		t.Context(),
		ref,
		tools.ShellJobSubmitRequest{Command: "true", YieldTime: time.Second},
	)
	require.NoError(t, err)
	require.Equal(t, interfaces.WorkStateSucceeded, result.Result.State)
}
