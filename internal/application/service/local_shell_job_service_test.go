//go:build desktop

package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/background"
	"github.com/Tencent/WeKnora/internal/localsandbox"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type localTestProcess struct {
	outR, errR *io.PipeReader
	outW, errW *io.PipeWriter
	once       sync.Once
	exit       localsandbox.ExitStatus
	killCount  int
}

func newLocalTestProcess() *localTestProcess {
	outR, outW := io.Pipe()
	errR, errW := io.Pipe()
	return &localTestProcess{outR: outR, outW: outW, errR: errR, errW: errW}
}
func (p *localTestProcess) Stdout() io.Reader { return p.outR }
func (p *localTestProcess) Stderr() io.Reader { return p.errR }
func (p *localTestProcess) PID() int          { return 4321 }
func (p *localTestProcess) Kill() error {
	p.killCount++
	p.finish()
	return nil
}
func (p *localTestProcess) finish() { p.once.Do(func() { _ = p.outW.Close(); _ = p.errW.Close() }) }
func (p *localTestProcess) Wait(context.Context) (localsandbox.ExitStatus, error) {
	return p.exit, nil
}

type localTestStarter struct {
	proc  *localTestProcess
	root  string
	calls int
}

func (f *localTestStarter) StartForSession(context.Context, localsandbox.RunRequest) (localsandbox.Process, error) {
	f.calls++
	return f.proc, nil
}

func (f *localTestStarter) GuardForSession(
	context.Context, string,
) (*localsandbox.PathGuard, localsandbox.Workspace, error) {
	return nil, localsandbox.Workspace{Root: f.root}, nil
}

func localFixture(t *testing.T) (*LocalShellJobService, *localTestProcess, *BackgroundWork) {
	t.Helper()
	base := t.TempDir()
	p := newLocalTestProcess()
	starter := &localTestStarter{proc: p, root: filepath.Join(base, "project")}
	bg := NewBackgroundWork(background.NewMemoryActivityRegistry(nil), background.NewMemoryNoticeInbox(nil))
	svc, err := newLocalShellJobService(starter, bg, nil, filepath.Join(base, "jobs"),
		func(context.Context, int) (int64, error) { return 100, nil })
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = svc.Shutdown(ctx)
	})
	return svc, p, bg
}

func TestYieldFinishedCommandIsNotBackgroundedWhenTimerAlsoFires(t *testing.T) {
	done := make(chan struct{})
	close(done)
	timer := make(chan time.Time, 1)
	timer <- time.Now()
	stop := make(chan struct{})
	for range 40 {
		require.False(t, backgroundedAfterYield(done, stop, timer),
			"命令已经结束时，计时器同时就绪也不能把它登记成后台任务")
	}
}

func TestLocalShortResultDiscardedNotFinished(t *testing.T) {
	svc, p, bg := localFixture(t)
	p.finish()
	ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
	sub, err := svc.SubmitShellJob(context.Background(), ref, tools.ShellJobSubmitRequest{
		Command: "true", Description: "quick", YieldTime: time.Second, RunID: "m1",
	})
	require.NoError(t, err)
	require.False(t, sub.Background)
	require.Equal(t, interfaces.WorkStateSucceeded, sub.Result.State)
	require.Eventually(t, func() bool {
		items, _ := bg.Registry.List(context.Background(), ref)
		return len(items) == 0
	}, time.Second, 10*time.Millisecond, "Discard 移除登记")
	notices, err := bg.Inbox.Pending(context.Background(), ref)
	require.NoError(t, err)
	require.Empty(t, notices, "短结果不进信箱、不发事件")
}

func TestLocalRunningHandleFinishesOnce(t *testing.T) {
	svc, p, bg := localFixture(t)
	ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
	sub, err := svc.SubmitShellJob(context.Background(), ref, tools.ShellJobSubmitRequest{
		Command: "true", Description: "build", YieldTime: time.Millisecond, RunID: "m1",
	})
	require.NoError(t, err)
	require.True(t, sub.Background)
	require.Equal(t, interfaces.WorkStateRunning, sub.Result.State)
	p.finish()
	require.Eventually(t, func() bool {
		notices, _ := bg.Inbox.Pending(context.Background(), ref)
		return len(notices) == 1
	}, 2*time.Second, 10*time.Millisecond)
	notices, _ := bg.Inbox.Pending(context.Background(), ref)
	require.Equal(t, sub.Result.ID, notices[0].ID)
	require.Contains(t, notices[0].Text, "succeeded")
}

func TestLocalCrossSessionIDRejected(t *testing.T) {
	svc, _, _ := localFixture(t)
	ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
	sub, err := svc.SubmitShellJob(context.Background(), ref, tools.ShellJobSubmitRequest{
		Command: "true", YieldTime: time.Millisecond,
	})
	require.NoError(t, err)
	got, err := svc.Status(context.Background(), interfaces.SessionRef{TenantID: 8, SessionID: "s"},
		[]string{sub.Result.ID, "sj_abcd"}, interfaces.StatusOptions{Detailed: true})
	require.NoError(t, err)
	require.Equal(t, interfaces.WorkStateLost, got[0].State)
	require.Equal(t, sub.Result.ID, got[0].ID)
	require.Equal(t, interfaces.WorkStateLost, got[1].State)
	require.Equal(t, "sj_abcd", got[1].ID)
}

func TestLocalStatusAndCancelArePerID(t *testing.T) {
	svc, p, _ := localFixture(t)
	ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
	sub, err := svc.SubmitShellJob(context.Background(), ref, tools.ShellJobSubmitRequest{
		Command: "true", YieldTime: time.Millisecond,
	})
	require.NoError(t, err)
	require.True(t, sub.Background)
	statusPath := filepath.Join(svc.root, sub.Result.ID, "status.json")
	require.NoError(t, os.WriteFile(statusPath, []byte("{"), 0o600))

	got, err := svc.Status(context.Background(), ref, []string{sub.Result.ID, "sj_abcd"},
		interfaces.StatusOptions{Detailed: true})
	require.NoError(t, err)
	require.Equal(t, interfaces.WorkStateLost, got[0].State, "one unreadable job does not fail the batch")
	require.Equal(t, sub.Result.ID, got[0].ID)
	require.Equal(t, interfaces.WorkStateLost, got[1].State)
	require.Equal(t, "sj_abcd", got[1].ID)

	cancelled, err := svc.Cancel(context.Background(), ref, []string{"sj_abcd", sub.Result.ID})
	require.NoError(t, err)
	require.Equal(t, interfaces.WorkStateLost, cancelled[0].State)
	require.Equal(t, "sj_abcd", cancelled[0].ID)
	require.NotEqual(t, interfaces.WorkStateLost, cancelled[1].State)
	require.Eventually(t, func() bool { return p.killCount >= 1 }, time.Second, 10*time.Millisecond)
}

func TestLocalSubmitIgnoresUnsupportedProcessIdentity(t *testing.T) {
	base := t.TempDir()
	p := newLocalTestProcess()
	starter := &localTestStarter{proc: p, root: filepath.Join(base, "project")}
	bg := NewBackgroundWork(background.NewMemoryActivityRegistry(nil), background.NewMemoryNoticeInbox(nil))
	root := filepath.Join(base, "jobs")
	svc, err := newLocalShellJobService(starter, bg, nil, root,
		func(context.Context, int) (int64, error) { return 0, localsandbox.ErrUnsupportedPlatform })
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = svc.Shutdown(ctx)
	})
	ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
	sub, err := svc.SubmitShellJob(context.Background(), ref, tools.ShellJobSubmitRequest{
		Command: "true", YieldTime: time.Millisecond,
	})
	require.NoError(t, err)
	require.Zero(t, p.killCount)
	raw, err := os.ReadFile(filepath.Join(root, sub.Result.ID, "status.json"))
	require.NoError(t, err)
	var status sandbox.ShellJobStatus
	require.NoError(t, json.Unmarshal(raw, &status))
	require.Zero(t, status.ProcStartedAt)
}

func TestLocalSubmitStillFailsWhenProcessIdentityCannotBeRecorded(t *testing.T) {
	base := t.TempDir()
	p := newLocalTestProcess()
	starter := &localTestStarter{proc: p, root: filepath.Join(base, "project")}
	bg := NewBackgroundWork(background.NewMemoryActivityRegistry(nil), background.NewMemoryNoticeInbox(nil))
	svc, err := newLocalShellJobService(starter, bg, nil, filepath.Join(base, "jobs"),
		func(context.Context, int) (int64, error) { return 0, errors.New("boom") })
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = svc.Shutdown(ctx)
	})
	_, err = svc.SubmitShellJob(context.Background(), interfaces.SessionRef{TenantID: 7, SessionID: "s"},
		tools.ShellJobSubmitRequest{Command: "true", YieldTime: time.Millisecond})
	require.Error(t, err)
	require.GreaterOrEqual(t, p.killCount, 1)
}

func TestLocalDeleteBlocksNewSubmitsAndLateNotices(t *testing.T) {
	svc, _, bg := localFixture(t)
	ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
	sub, err := svc.SubmitShellJob(context.Background(), ref, tools.ShellJobSubmitRequest{
		Command: "true", YieldTime: time.Millisecond,
	})
	require.NoError(t, err)
	require.True(t, sub.Background)
	require.NoError(t, svc.OnDelete(context.Background(), ref))
	require.NoError(t, bg.Inbox.Clear(context.Background(), ref), "共享 listener 清空信箱")
	time.Sleep(50 * time.Millisecond) // 给看守终段机会：不允许 Clear 后再 Push
	notices, err := bg.Inbox.Pending(context.Background(), ref)
	require.NoError(t, err)
	require.Empty(t, notices, "suppress 生效：迟到的 Finish 不重建信箱")
	_, err = svc.SubmitShellJob(context.Background(), ref, tools.ShellJobSubmitRequest{Command: "true"})
	require.Error(t, err, "删除后该会话提交永久拒绝")
	status, err := svc.Status(context.Background(), ref, []string{sub.Result.ID},
		interfaces.StatusOptions{Detailed: true})
	require.NoError(t, err)
	require.Equal(t, interfaces.WorkStateCancelled, status[0].State)
}

func TestLocalCursorsIncrementalAndHTTPDoesNotMove(t *testing.T) {
	svc, p, _ := localFixture(t)
	ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
	sub, err := svc.SubmitShellJob(context.Background(), ref, tools.ShellJobSubmitRequest{
		Command: "true", YieldTime: time.Millisecond, RunID: "m1",
	})
	require.NoError(t, err)
	_, err = p.outW.Write([]byte("first"))
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		r, err := svc.Status(context.Background(), ref, []string{sub.Result.ID},
			interfaces.StatusOptions{Detailed: true, RunID: "m1"})
		return err == nil && r[0].Payload.(ShellJobPayload).Stdout == "first"
	}, time.Second, 10*time.Millisecond)
	// HTTP 读（RunID 空）：开头+结尾，不移动游标。
	_, err = p.outW.Write([]byte("second"))
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		r, err := svc.Status(context.Background(), ref, []string{sub.Result.ID},
			interfaces.StatusOptions{Detailed: true})
		return err == nil && r[0].Payload.(ShellJobPayload).Stdout == "firstsecond"
	}, time.Second, 10*time.Millisecond)
	// 同 run 第二次读：只剩 HTTP 读之后才出现的新输出。
	require.Eventually(t, func() bool {
		r, err := svc.Status(context.Background(), ref, []string{sub.Result.ID},
			interfaces.StatusOptions{Detailed: true, RunID: "m1"})
		return err == nil && r[0].Payload.(ShellJobPayload).Stdout == "second"
	}, time.Second, 10*time.Millisecond)
	svc.EndShellJobRun(ref, "m1")
	svc.mu.Lock()
	cursor := svc.cursors[localCursorKey(ref, "m1", sub.Result.ID)]
	svc.mu.Unlock()
	require.WithinDuration(t, time.Now().Add(time.Hour), cursor.expires, time.Second,
		"run 结束后一小时过期，而非最后读取后")
}

func TestLocalStopContextDoesNotKillChild(t *testing.T) {
	svc, p, _ := localFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
	_, err := svc.SubmitShellJob(ctx, ref, tools.ShellJobSubmitRequest{
		Command: "true", YieldTime: time.Millisecond,
	})
	require.NoError(t, err)
	cancel() // /stop 语义：只结束提交等待
	r, err := svc.Status(context.Background(), ref, []string{r0(t, svc, ref)},
		interfaces.StatusOptions{})
	require.NoError(t, err)
	require.Equal(t, interfaces.WorkStateRunning, r[0].State, "任务不受 run ctx 取消影响")
	require.Zero(t, p.killCount)
	p.finish()
}

func TestLocalShutdownKillsAndRecordsLost(t *testing.T) {
	svc, p, _ := localFixture(t)
	ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
	_, err := svc.SubmitShellJob(context.Background(), ref, tools.ShellJobSubmitRequest{
		Command: "true", YieldTime: time.Millisecond,
	})
	require.NoError(t, err)
	require.NoError(t, svc.Shutdown(context.Background()))
	require.GreaterOrEqual(t, p.killCount, 1, "应用级 ctx 取消触发进程树 Kill")
	r, err := svc.Status(context.Background(), ref, []string{r0(t, svc, ref)},
		interfaces.StatusOptions{Detailed: true})
	require.NoError(t, err)
	require.Equal(t, interfaces.WorkStateLost, r[0].State)
}

func TestLocalSkillEnvCaptureRunsOnlyAfterSuccess(t *testing.T) {
	for _, mode := range []string{"success", "failure", "delete"} {
		t.Run(mode, func(t *testing.T) {
			svc, proc, _ := localFixture(t)
			if mode == "failure" {
				proc.exit.Code = 1
			}
			ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
			sub, err := svc.SubmitShellJob(t.Context(), ref, tools.ShellJobSubmitRequest{
				Command: "true", YieldTime: time.Millisecond,
			})
			require.NoError(t, err)
			job, err := svc.ownedJob(ref, sub.Result.ID)
			require.NoError(t, err)
			calls := 0
			svc.HoldSkillEnvCapture(ref, sub.Result.ID, func(context.Context) { calls++ })
			if mode == "delete" {
				require.NoError(t, svc.OnDelete(t.Context(), ref))
				svc.HoldSkillEnvCapture(ref, sub.Result.ID, func(context.Context) { calls++ })
			} else {
				proc.finish()
			}
			select {
			case <-job.reported:
			case <-time.After(time.Second):
				t.Fatal("watcher did not finish reporting")
			}
			want := 0
			if mode == "success" {
				want = 1
			}
			require.Equal(t, want, calls)
			require.Empty(t, svc.skillCapture.pending())
		})
	}
}

// r0 取该会话唯一的 job id（测试辅助）。
func r0(t *testing.T, svc *LocalShellJobService, ref interfaces.SessionRef) string {
	t.Helper()
	svc.mu.Lock()
	defer svc.mu.Unlock()
	for id, j := range svc.jobs {
		if j.meta.TenantID == ref.TenantID && j.meta.SessionID == ref.SessionID {
			return id
		}
	}
	t.Fatal("no job")
	return ""
}
