package service

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/background"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

var shellJobMarkerInScript = regexp.MustCompile(`__WEKNORA_SJ___[0-9a-f]{12}__`)

// fakeRemoteReader 模拟批量读取，签名与 remoteReadFunc 缝一致。它必须忠实
// 履行契约：len(snapshots)==len(ids) 恒成立、缺席任务给 Missing 占位、
// errs 按 workID 逐条注入——否则它测不出生产实现的 D1 合规性。
type fakeRemoteReader struct {
	snapshots map[string]sandbox.ShellJobSnapshot
	errs      map[string]error
	execCalls int
	// calls 记录每次读取收到的 (ids, sandboxIDs)，测试据此断言 D1。
	calls []fakeBatchReadCall
}

type fakeBatchReadCall struct {
	ids        []string
	sandboxIDs map[string]string
}

func (f *fakeRemoteReader) batchRead(_ context.Context, _ interfaces.SessionRef,
	ids []string, sandboxIDs map[string]string,
) ([]sandbox.ShellJobSnapshot, map[string]error) {
	f.execCalls++
	copied := make(map[string]string, len(sandboxIDs))
	for id, sbx := range sandboxIDs {
		copied[id] = sbx
	}
	f.calls = append(f.calls, fakeBatchReadCall{
		ids:        append([]string(nil), ids...),
		sandboxIDs: copied,
	})
	out := make([]sandbox.ShellJobSnapshot, len(ids))
	for i, id := range ids {
		if snap, ok := f.snapshots[id]; ok {
			out[i] = snap
		} else {
			out[i] = sandbox.ShellJobSnapshot{Missing: true, Meta: sandbox.ShellJobMeta{ID: id}}
		}
	}
	return out, f.errs
}

func newRemoteServiceFixture(t *testing.T) (*ShellJobService, *fakeRemoteReader, *ShellJobIndex, *BackgroundWork) {
	t.Helper()
	mr := miniredisRunT(t)
	client := redisNewClient(mr)
	t.Cleanup(func() { _ = client.Close() })
	index := NewShellJobIndex(client, "weknora")
	bg := NewBackgroundWork(background.NewMemoryActivityRegistry(nil), background.NewMemoryNoticeInbox(nil))
	reader := &fakeRemoteReader{snapshots: map[string]sandbox.ShellJobSnapshot{}}
	// keeper 经构造器注入（与生产同一条路径）；需要断言顺序的测试用
	// newRemoteServiceFixtureWithKeeper 换成自己的 fake。
	svc := newRemoteServiceFixtureWithKeeper(t, bg, index, keeperFunc(
		func(context.Context, interfaces.SessionRef) error { return nil }))
	svc.remoteRead = reader.batchRead
	svc.remoteExec = func(context.Context, string, string, sandbox.ShellExecOptions) (*sandbox.ExecuteResult, error) {
		return &sandbox.ExecuteResult{ExitCode: 0}, nil
	}
	svc.boundSandbox = func(context.Context, string) (string, bool) { return "sbx1", true }
	return svc, reader, index, bg
}

func newRemoteServiceFixtureWithKeeper(t *testing.T, bg *BackgroundWork,
	index *ShellJobIndex, keeper interfaces.SessionKeepAliver,
) *ShellJobService {
	t.Helper()
	svc := NewShellJobService(nil, bg, index, nil, keeper)
	// pinned 为 nil。RemoteAvailable 在注入 remoteExec 后才为真，提交才能走到
	// keeper 检查；否则会先报 unavailable，测不到「没接线必须当场失败」。
	svc.remoteExec = func(context.Context, string, string, sandbox.ShellExecOptions) (*sandbox.ExecuteResult, error) {
		return &sandbox.ExecuteResult{ExitCode: 0}, nil
	}
	return svc
}

// keeperFunc 把保活断言挂进服务：keepalive 与 exec 的先后顺序必须可观测。
type keeperFunc func(context.Context, interfaces.SessionRef) error

func (f keeperFunc) KeepAlive(ctx context.Context, s interfaces.SessionRef) error { return f(ctx, s) }

func TestSubmitKeepAlivesBeforeTheYieldWait(t *testing.T) {
	var order []string
	mr := miniredisRunT(t)
	client := redisNewClient(mr)
	t.Cleanup(func() { _ = client.Close() })
	index := NewShellJobIndex(client, "weknora")
	bg := NewBackgroundWork(background.NewMemoryActivityRegistry(nil), background.NewMemoryNoticeInbox(nil))
	// keeper 经构造器注入——与生产 provider 的接线路径一致，不是字段直塞。
	svc := newRemoteServiceFixtureWithKeeper(t, bg, index, keeperFunc(
		func(context.Context, interfaces.SessionRef) error {
			order = append(order, "keepalive")
			return nil
		}))
	reader := &fakeRemoteReader{snapshots: map[string]sandbox.ShellJobSnapshot{}}
	svc.remoteRead = reader.batchRead
	svc.remoteExec = func(context.Context, string, string, sandbox.ShellExecOptions) (*sandbox.ExecuteResult, error) {
		order = append(order, "exec")
		return &sandbox.ExecuteResult{ExitCode: 0}, nil
	}
	svc.boundSandbox = func(context.Context, string) (string, bool) { return "sbx1", true }
	ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
	_, err := svc.SubmitShellJob(context.Background(), ref, tools.ShellJobSubmitRequest{
		Command: "true", YieldTime: 30 * time.Second, RunID: "m1",
	})
	require.NoError(t, err)
	// Begin 在 start 返回之后还会再保活一次。前两步必须是「保活、再 exec」。
	require.Equal(t, []string{"keepalive", "exec", "keepalive"}, order,
		"保活必须在 yield 等待之前：Lifecycle.Begin 自带的保活要等 start 返回（最长 40s 后）")
}

// E2B 的 exec 不续期，keeper 漏接线绝不能静默跳过：提交必须当场失败，
// 而不是几分钟后以「莫名的 lost」出现。
func TestSubmitFailsLoudlyWithoutKeepAliver(t *testing.T) {
	mr := miniredisRunT(t)
	client := redisNewClient(mr)
	t.Cleanup(func() { _ = client.Close() })
	index := NewShellJobIndex(client, "weknora")
	bg := NewBackgroundWork(background.NewMemoryActivityRegistry(nil), background.NewMemoryNoticeInbox(nil))
	svc := newRemoteServiceFixtureWithKeeper(t, bg, index, nil)
	require.True(t, svc.lifecycle.Events == nil, "nil emitter must not become a typed-nil interface")
	svc.boundSandbox = func(context.Context, string) (string, bool) { return "sbx1", true }
	ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
	_, err := svc.SubmitShellJob(context.Background(), ref, tools.ShellJobSubmitRequest{
		Command: "true", YieldTime: time.Second,
	})
	require.ErrorContains(t, err, "no keep-aliver wired")
	items, listErr := bg.Registry.List(context.Background(), ref)
	require.NoError(t, listErr)
	require.Empty(t, items, "Begin 之前就失败：不留登记")
	due, dueErr := index.Due(context.Background(), time.Now().Add(time.Hour))
	require.NoError(t, dueErr)
	require.Empty(t, due, "Begin 之前就失败：不留索引成员")
}

func TestAllocateShellJobIDSkipsTakenHandles(t *testing.T) {
	id, err := allocateShellJobID(func(string) bool { return false })
	require.NoError(t, err)
	require.True(t, sandbox.ValidShellJobID(id))

	_, err = allocateShellJobID(func(string) bool { return true })
	require.ErrorIs(t, err, errShellJobIDTaken)
}

func TestSubmitRetriesWhenTheJobDirectoryExists(t *testing.T) {
	svc, _, index, bg := newRemoteServiceFixture(t)
	ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
	calls := 0
	svc.remoteExec = func(context.Context, string, string, sandbox.ShellExecOptions) (*sandbox.ExecuteResult, error) {
		calls++
		if calls == 1 {
			return &sandbox.ExecuteResult{ExitCode: sandbox.ShellJobDirExistsExit}, nil
		}
		return &sandbox.ExecuteResult{ExitCode: 0}, nil
	}
	_, err := svc.SubmitShellJob(context.Background(), ref, tools.ShellJobSubmitRequest{
		Command: "true", YieldTime: time.Second,
	})
	require.NoError(t, err)
	require.Equal(t, 2, calls)
	items, err := bg.Registry.List(context.Background(), ref)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.True(t, sandbox.ValidShellJobID(items[0].ID))
	due, err := index.Due(context.Background(), time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.Len(t, due, 1)
}

func TestSubmitProvisionsSandboxWhenUnbound(t *testing.T) {
	svc, _, index, bg := newRemoteServiceFixture(t)
	ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
	bound := ""
	var sawProvision bool
	svc.boundSandbox = func(context.Context, string) (string, bool) {
		if bound == "" {
			return "", false
		}
		return bound, true
	}
	svc.remoteExec = func(
		_ context.Context, _ string, command string, opts sandbox.ShellExecOptions,
	) (*sandbox.ExecuteResult, error) {
		if opts.ExpectedSandboxID == "" {
			require.Equal(t, "true", command)
			// No pinned cwd: shared machines admit only the session layout
			// root, which is exactly the manager's empty-WorkDir default.
			require.Empty(t, opts.WorkDir)
			sawProvision = true
			bound = "sbx-new"
			return &sandbox.ExecuteResult{ExitCode: 0}, nil
		}
		require.Equal(t, "sbx-new", opts.ExpectedSandboxID)
		require.NotEqual(t, "true", command)
		return &sandbox.ExecuteResult{ExitCode: 0}, nil
	}
	_, err := svc.SubmitShellJob(context.Background(), ref, tools.ShellJobSubmitRequest{
		Command: "sleep 30", YieldTime: time.Second,
	})
	require.NoError(t, err)
	require.True(t, sawProvision)
	items, err := bg.Registry.List(context.Background(), ref)
	require.NoError(t, err)
	require.Len(t, items, 1)
	due, err := index.Due(context.Background(), time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.Len(t, due, 1)
	require.Equal(t, "sbx-new", due[0].SandboxID)
}

func TestSubmitProvisionFailureLeavesNoRegistration(t *testing.T) {
	svc, _, index, bg := newRemoteServiceFixture(t)
	ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
	svc.boundSandbox = func(context.Context, string) (string, bool) { return "", false }
	svc.remoteExec = func(context.Context, string, string, sandbox.ShellExecOptions) (*sandbox.ExecuteResult, error) {
		return nil, errors.New("create sandbox: boom")
	}
	_, err := svc.SubmitShellJob(context.Background(), ref, tools.ShellJobSubmitRequest{
		Command: "true", YieldTime: time.Second,
	})
	require.ErrorContains(t, err, "create sandbox: boom")
	items, listErr := bg.Registry.List(context.Background(), ref)
	require.NoError(t, listErr)
	require.Empty(t, items)
	due, dueErr := index.Due(context.Background(), time.Now().Add(time.Hour))
	require.NoError(t, dueErr)
	require.Empty(t, due)
}

func TestSubmitLeaseCoversTheWholeYieldWindow(t *testing.T) {
	require.Equal(t, 45*time.Second, shellJobLease(30*time.Second))
	require.Equal(t, 25*time.Second, shellJobLease(10*time.Second),
		"默认窗口下接近设计稿的 30 秒；长窗口必须按比例放宽，否则登记在窗口内过期")
}

func TestSubmitPreLaunchFailureLeavesNoGhostIndexEntry(t *testing.T) {
	svc, _, index, bg := newRemoteServiceFixture(t)
	ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
	// /stop 或 resolve 失败发生在 client.Exec 之前：返回真 Go 错误。
	svc.remoteExec = func(context.Context, string, string, sandbox.ShellExecOptions) (*sandbox.ExecuteResult, error) {
		return nil, errors.New("context canceled before exec")
	}
	_, err := svc.SubmitShellJob(context.Background(), ref, tools.ShellJobSubmitRequest{
		Command: "true", YieldTime: time.Second,
	})
	require.Error(t, err)
	due, err := index.Due(context.Background(), time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.Empty(t, due, "wrapper 从未启动：索引成员必须回滚，否则 sweeper 会发假 lost 通知")
	items, err := bg.Registry.List(context.Background(), ref)
	require.NoError(t, err)
	require.Empty(t, items, "Begin 已回滚登记")
}

func TestSubmitMidExecCancellationKeepsRegistration(t *testing.T) {
	svc, reader, index, bg := newRemoteServiceFixture(t)
	ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
	// /stop 落在 exec 中段：transport 错误被 remoteExecuteResult 包装进
	// ExecuteResult.Error，Go err 为 nil —— 必须按「已启动、等待被切断」处理。
	svc.remoteExec = func(context.Context, string, string, sandbox.ShellExecOptions) (*sandbox.ExecuteResult, error) {
		return &sandbox.ExecuteResult{ExitCode: -1, Error: "context canceled"}, nil
	}
	sub, err := svc.SubmitShellJob(context.Background(), ref, tools.ShellJobSubmitRequest{
		Command: "true", YieldTime: time.Second, RunID: "m1",
	})
	require.NoError(t, err)
	require.True(t, sub.Background, "/stop 中段取消：返回运行中句柄，sweeper 窗口后裁决")
	require.Equal(t, interfaces.WorkStateRunning, sub.Result.State)
	items, err := bg.Registry.List(context.Background(), ref)
	require.NoError(t, err)
	require.Len(t, items, 1, "登记保留：rewind/重建保护不因 /stop 失效")
	due, err := index.Due(context.Background(), time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.NotEmpty(t, due, "索引成员保留：命令可能仍在运行")
	require.Zero(t, reader.execCalls, "中段取消后不得再详细读取：context 可能已取消，读失败会看起来像 lost")
}

func TestSweepReportsTerminalAndClaimsOnce(t *testing.T) {
	svc, reader, index, bg := newRemoteServiceFixture(t)
	ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
	now := time.Now().UTC()
	ended := now.Add(-time.Minute)
	exit := 0
	reader.snapshots["sj_0123"] = sandbox.ShellJobSnapshot{
		Meta: sandbox.ShellJobMeta{
			ID: "sj_0123", TenantID: 7, SessionID: "s",
			SandboxID: "sbx1", Title: "build", ParentMessageID: "m1",
		},
		Status: sandbox.ShellJobStatus{
			State:    interfaces.WorkStateSucceeded,
			ExitCode: &exit, StartedAt: now.Add(-2 * time.Minute), EndedAt: &ended,
		},
		WatcherAlive: true,
	}
	require.NoError(t, index.Add(context.Background(), ShellJobIndexEntry{
		SessionRef: ref, WorkID: "sj_0123", SandboxID: "sbx1",
		YieldUntil: now.Add(-time.Minute),
	}))
	err := bg.Registry.Add(context.Background(), ref,
		interfaces.BackgroundActivity{ID: "sj_0123", Kind: "shell"}, 30*time.Second, 0)
	require.NoError(t, err)

	require.NoError(t, svc.SweepOnce(context.Background()))

	notices, err := bg.Inbox.Pending(context.Background(), ref)
	require.NoError(t, err)
	require.Len(t, notices, 1)
	require.Equal(t, "sj_0123", notices[0].ID)
	items, err := bg.Registry.List(context.Background(), ref)
	require.NoError(t, err)
	require.Empty(t, items, "Finish 移除登记")
	due, err := index.Due(context.Background(), time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.Empty(t, due, "终态后索引成员被 ZREM")

	// 第二轮 sweep 不重复通知（D2：ZREM 已把成员拿走）
	require.NoError(t, svc.SweepOnce(context.Background()))
	notices, err = bg.Inbox.Pending(context.Background(), ref)
	require.NoError(t, err)
	require.Len(t, notices, 1)
}

// 共享机器（user-mode 沙盒）只接受 layout.WorkDirRoots 作为 exec 的 cwd，
// 管理器对空 WorkDir 自动取会话 layout 根（session_manager 的默认）。
// 任一维护 exec 显式指定 "/" 或旧 "/workspace" 都会被 CleanWorkDirIn
// 拒绝，把 shell_exec_async 的提交、取消与 work_wait 全部打断。
func TestMaintenanceExecsLeaveWorkDirToTheSessionLayout(t *testing.T) {
	svc, reader, _, _ := newRemoteServiceFixture(t)
	var execOpts []sandbox.ShellExecOptions
	inner := svc.remoteExec
	record := func(
		ctx context.Context, sid, cmd string, opts sandbox.ShellExecOptions,
	) (*sandbox.ExecuteResult, error) {
		execOpts = append(execOpts, opts)
		return inner(ctx, sid, cmd, opts)
	}
	svc.remoteExec = record
	ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
	execOpts = nil
	svc.boundSandbox = func(context.Context, string) (string, bool) { return "", false }
	_, _ = svc.SubmitShellJob(context.Background(), ref, tools.ShellJobSubmitRequest{
		Command: "true", YieldTime: time.Second,
	})
	require.NotEmpty(t, execOpts, "无绑定时 ensureBoundSandbox 也要走一次预热 exec")
	require.Empty(t, execOpts[0].WorkDir, "预热 exec 不得钉死 cwd：旧 /workspace 在共享机器上被拒")

	svc.boundSandbox = func(context.Context, string) (string, bool) { return "sbx1", true }
	execOpts = nil
	_, err := svc.SubmitShellJob(context.Background(), ref, tools.ShellJobSubmitRequest{
		Command: "true", YieldTime: time.Second,
	})
	require.NoError(t, err)
	require.NotEmpty(t, execOpts)
	for _, opts := range execOpts {
		require.Empty(t, opts.WorkDir, "提交 exec 不得指定 cwd：\"/\" 在共享机器上被拒")
	}

	execOpts = nil
	reader.snapshots["sj_0123"] = ownedRunningShellSnapshot("sj_0123", "s", "sbx1")
	inner = func(context.Context, string, string, sandbox.ShellExecOptions) (*sandbox.ExecuteResult, error) {
		return &sandbox.ExecuteResult{Stdout: `{"state":"cancelled"}`}, nil
	}
	_, err = svc.Cancel(context.Background(), ref, []string{"sj_0123"})
	require.NoError(t, err)
	require.NotEmpty(t, execOpts)
	require.Empty(t, execOpts[0].WorkDir, "取消 exec 不得指定 cwd")

	recorded := 0
	svc.remoteExec = func(_ context.Context, _ string, _ string,
		opts sandbox.ShellExecOptions,
	) (*sandbox.ExecuteResult, error) {
		recorded++
		require.Empty(t, opts.WorkDir, "批量读 exec 不得指定 cwd")
		return &sandbox.ExecuteResult{ExitCode: 0}, nil
	}
	_, _ = svc.batchReadViaPinned(context.Background(), ref,
		[]string{"sj_0123"}, map[string]string{"sj_0123": "sbx1"})
	require.NotZero(t, recorded)
}

func TestSweepKeepsJobWhenReadFails(t *testing.T) {
	svc, reader, index, bg := newRemoteServiceFixture(t)
	ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
	now := time.Now().UTC()
	reader.errs = map[string]error{"sj_0123": errors.New("sandbox exec timed out")}
	reader.snapshots["sj_0123"] = sandbox.ShellJobSnapshot{
		Meta: sandbox.ShellJobMeta{
			ID: "sj_0123", TenantID: 7, SessionID: "s", SandboxID: "sbx1", Title: "build",
		},
		Status:       sandbox.ShellJobStatus{State: interfaces.WorkStateRunning, StartedAt: now},
		WatcherAlive: true,
	}
	require.NoError(t, index.Add(context.Background(), ShellJobIndexEntry{
		SessionRef: ref, WorkID: "sj_0123", SandboxID: "sbx1", YieldUntil: now.Add(-time.Minute),
	}))
	require.NoError(t, bg.Registry.Add(context.Background(), ref,
		interfaces.BackgroundActivity{ID: "sj_0123", Kind: "shell", Title: "build"}, time.Minute, 0))

	require.NoError(t, svc.SweepOnce(context.Background()))

	notices, err := bg.Inbox.Pending(context.Background(), ref)
	require.NoError(t, err)
	require.Empty(t, notices, "读取失败不是终态，不能写完成通知")
	items, err := bg.Registry.List(context.Background(), ref)
	require.NoError(t, err)
	require.Len(t, items, 1, "登记还在，沙盒重建保护不能因为一次读取失败消失")
	due, err := index.Due(context.Background(), time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.NotEmpty(t, due, "索引成员留下，下一轮再读")
}

func TestSweepMarksLostWhenTheIndexHasNoSandbox(t *testing.T) {
	svc, reader, index, bg := newRemoteServiceFixture(t)
	ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
	now := time.Now().UTC()
	reader.errs = map[string]error{"sj_0123": errors.New("session has no bound sandbox")}
	require.NoError(t, index.Add(context.Background(), ShellJobIndexEntry{
		SessionRef: ref, WorkID: "sj_0123", SandboxID: "", YieldUntil: now.Add(-time.Minute),
	}))
	require.NoError(t, bg.Registry.Add(context.Background(), ref,
		interfaces.BackgroundActivity{ID: "sj_0123", Kind: "shell", Title: "build"}, time.Minute, 0))

	require.NoError(t, svc.SweepOnce(context.Background()))

	notices, err := bg.Inbox.Pending(context.Background(), ref)
	require.NoError(t, err)
	require.Len(t, notices, 1)
	require.Contains(t, notices[0].Text, "lost")
	items, err := bg.Registry.List(context.Background(), ref)
	require.NoError(t, err)
	require.Empty(t, items)
}

func TestCancelReportsTheStatusItRead(t *testing.T) {
	svc, reader, _, _ := newRemoteServiceFixture(t)
	ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
	reader.snapshots["sj_0123"] = ownedRunningShellSnapshot("sj_0123", "s", "sbx1")
	svc.remoteExec = func(context.Context, string, string, sandbox.ShellExecOptions) (*sandbox.ExecuteResult, error) {
		return &sandbox.ExecuteResult{
			ExitCode: 0,
			Stdout:   `{"state":"succeeded","watcher_pid":1,"pgid":2,"exit_code":0}`,
		}, nil
	}
	results, err := svc.Cancel(context.Background(), ref, []string{"sj_0123"})
	require.NoError(t, err)
	require.Equal(t, interfaces.WorkStateSucceeded, results[0].State)
	require.True(t, results[0].Terminal)
}

func TestCancelDoesNotInventCancelledWithoutAStatus(t *testing.T) {
	svc, reader, _, _ := newRemoteServiceFixture(t)
	ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
	reader.snapshots["sj_0123"] = ownedRunningShellSnapshot("sj_0123", "s", "sbx1")
	svc.remoteExec = func(context.Context, string, string, sandbox.ShellExecOptions) (*sandbox.ExecuteResult, error) {
		return &sandbox.ExecuteResult{ExitCode: 0, Stdout: ""}, nil
	}
	results, err := svc.Cancel(context.Background(), ref, []string{"sj_0123"})
	require.ErrorContains(t, err, "cancel status unreadable")
	require.Empty(t, results)
}

func TestCancelScriptPrintsStatusInsteadOfExitingEarly(t *testing.T) {
	script := sandbox.ShellJobCancelScript(sandbox.ShellJobMeta{ID: "sj_0123"}, false)
	require.NotContains(t, script, "exit 0")
	require.Contains(t, script, `cat "$D/status.json"`)
	require.Contains(t, script, "kill -TERM")
}

func TestSweepMarksLostWhenWatcherDeadOrSandboxChanged(t *testing.T) {
	svc, reader, index, bg := newRemoteServiceFixture(t)
	ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
	now := time.Now().UTC()
	reader.snapshots["sj_0123"] = sandbox.ShellJobSnapshot{
		Meta:   sandbox.ShellJobMeta{ID: "sj_0123", TenantID: 7, SessionID: "s", SandboxID: "old"},
		Status: sandbox.ShellJobStatus{State: interfaces.WorkStateRunning, StartedAt: now},
	}
	require.NoError(t, index.Add(context.Background(), ShellJobIndexEntry{
		SessionRef: ref, WorkID: "sj_0123", SandboxID: "new", YieldUntil: now.Add(-time.Minute),
	}))
	require.NoError(t, svc.SweepOnce(context.Background()))
	notices, err := bg.Inbox.Pending(context.Background(), ref)
	require.NoError(t, err)
	require.Len(t, notices, 1)
	require.Contains(t, notices[0].Text, "lost")
}

func TestSweepRenewsAndReaddsExpiredRegistration(t *testing.T) {
	svc, reader, index, bg := newRemoteServiceFixture(t)
	ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
	now := time.Now().UTC()
	reader.snapshots["sj_0123"] = sandbox.ShellJobSnapshot{
		Meta:         sandbox.ShellJobMeta{ID: "sj_0123", TenantID: 7, SessionID: "s", SandboxID: "sbx1"},
		Status:       sandbox.ShellJobStatus{State: interfaces.WorkStateRunning, StartedAt: now},
		WatcherAlive: true,
	}
	require.NoError(t, index.Add(context.Background(), ShellJobIndexEntry{
		SessionRef: ref, WorkID: "sj_0123", SandboxID: "sbx1", YieldUntil: now.Add(-time.Minute),
	}))
	// 不预置登记：模拟租约已过期
	require.NoError(t, svc.SweepOnce(context.Background()))
	items, err := bg.Registry.List(context.Background(), ref)
	require.NoError(t, err)
	require.Len(t, items, 1, "sweeper 对仍在运行的任务重新 Add（kindCap=0）")
	require.Equal(t, "sj_0123", items[0].ID)
	// D1 钉子：sweep 的批量读取必须以索引成员的 sandbox_id 作为 expected。
	require.Len(t, reader.calls, 1)
	require.Equal(t, "sbx1", reader.calls[0].sandboxIDs["sj_0123"],
		"expected 来自索引成员（D1「任务索引成员带 sandbox_id」），绝不能空——空值会走 resolveSession")
}

// D1 钉子：生产批量读取按 (会话, 沙盒) 分组、每组一次 exec、每次都带
// ExpectedSandboxID + SkipWorkspacePrep。这是防止「无 turn resolve 立即重建」
// （session_lifecycle.go:186-196）的核心执行点，fake 读取器测不到，必须
// 用 fake remoteExec 直测 batchReadViaPinned。
func TestBatchReadLocksExpectedSandbox(t *testing.T) {
	svc, _, _, _ := newRemoteServiceFixture(t)
	ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
	var calls []sandbox.ShellExecOptions
	svc.remoteExec = func(_ context.Context, _ string, _ string,
		opts sandbox.ShellExecOptions,
	) (*sandbox.ExecuteResult, error) {
		calls = append(calls, opts)
		return &sandbox.ExecuteResult{
			ExitCode: 0,
			Stdout: `{"meta":{"work_id":"sj_0123"},"missing":true}` + "\n" +
				`{"meta":{"work_id":"sj_ab01"},"missing":true}`,
		}, nil
	}
	snapshots, errs := svc.batchReadViaPinned(context.Background(), ref,
		[]string{"sj_0123", "sj_ab01"},
		map[string]string{"sj_0123": "sbx-a", "sj_ab01": "sbx-b"})
	require.Empty(t, errs)
	require.Len(t, snapshots, 2)
	require.Len(t, calls, 2, "按 (会话, 沙盒) 分组，每组一次 exec")
	seen := map[string]bool{}
	for _, opts := range calls {
		require.NotEmpty(t, opts.ExpectedSandboxID,
			"D1：维护 exec 绝不带空 expected——空值走 resolveSession，无 turn 时立即销毁重建沙盒")
		require.True(t, opts.SkipWorkspacePrep, "维护路径不重做目录准备（session_manager.go SkipWorkspacePrep 注释）")
		seen[opts.ExpectedSandboxID] = true
	}
	require.True(t, seen["sbx-a"] && seen["sbx-b"])
}

// A confirmed replacement is per-item lost; transient errors remain retryable.
func TestBatchReadSandboxChangedIsPerIDLost(t *testing.T) {
	svc, _, _, _ := newRemoteServiceFixture(t)
	ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
	svc.remoteExec = func(_ context.Context, _ string, _ string,
		opts sandbox.ShellExecOptions,
	) (*sandbox.ExecuteResult, error) {
		if opts.ExpectedSandboxID == "sbx-gone" {
			// SessionBoundManager 对 expected 不匹配返回真 Go 错误
			//（session_manager.go:843-845），不包装进 ExecuteResult。
			return nil, sandbox.ErrBoundSandboxChanged
		}
		return &sandbox.ExecuteResult{
			ExitCode: 0,
			Stdout: `{"meta":{"work_id":"sj_0123"},"missing":true}` + "\n" +
				`{"meta":{"work_id":"sj_ab01"},"missing":true}`,
		}, nil
	}
	snapshots, errs := svc.batchReadViaPinned(context.Background(), ref,
		[]string{"sj_0123", "sj_ab01"},
		map[string]string{"sj_0123": "sbx-gone", "sj_ab01": "sbx-ok"})
	require.Len(t, snapshots, 2)
	require.ErrorContains(t, errs["sj_0123"], "bound sandbox changed")
	require.NoError(t, errs["sj_ab01"], "组失败只判组内任务，不连累同批健康任务")

	// Status 出口的翻译：沙盒被换的任务逐条 lost，健康任务照常，绝不整批报错。
	svc.remoteRead = func(_ context.Context, _ interfaces.SessionRef, ids []string,
		_ map[string]string,
	) ([]sandbox.ShellJobSnapshot, map[string]error) {
		return []sandbox.ShellJobSnapshot{
			{Meta: sandbox.ShellJobMeta{ID: ids[0]}},
			{
				Meta:   sandbox.ShellJobMeta{ID: ids[1]},
				Status: sandbox.ShellJobStatus{State: interfaces.WorkStateRunning},
			},
		}, map[string]error{ids[0]: sandbox.ErrBoundSandboxChanged}
	}
	results, err := svc.Status(context.Background(), ref,
		[]string{"sj_0123", "sj_ab01"},
		interfaces.StatusOptions{Detailed: true})
	require.NoError(t, err, "confirmed loss must not obscure the healthy item")
	require.Equal(t, interfaces.WorkStateLost, results[0].State)
	require.Equal(t, interfaces.WorkStateRunning, results[1].State)
}

func TestBatchReadEmptySandboxIsPerIDLostWithoutExec(t *testing.T) {
	svc, _, _, _ := newRemoteServiceFixture(t)
	ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
	var calls int
	svc.remoteExec = func(context.Context, string, string, sandbox.ShellExecOptions) (*sandbox.ExecuteResult, error) {
		calls++
		return &sandbox.ExecuteResult{
			ExitCode: 0,
			Stdout: `{"meta":{"work_id":"sj_0123"},"missing":true}` + "\n" +
				`{"meta":{"work_id":"sj_ab01"},"missing":true}`,
		}, nil
	}
	snapshots, errs := svc.batchReadViaPinned(context.Background(), ref,
		[]string{"sj_0123", "sj_ab01"},
		map[string]string{"sj_0123": "", "sj_ab01": "sbx-ok"})
	require.Len(t, snapshots, 2)
	require.Equal(t, 1, calls, "空 sandbox id 不发 exec")
	require.ErrorContains(t, errs["sj_0123"], "no bound sandbox")
	require.NoError(t, errs["sj_ab01"])
}

func TestStatusCheapReturnsUnknownPerIDAndNeverExecs(t *testing.T) {
	svc, reader, _, _ := newRemoteServiceFixture(t)
	ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
	// 登记表里放一个 running；信箱里放一条已结束未 Ack 的通知；第三个 ID
	// 两处都没有（已 Ack 的终态）——三种廉价结果必须在一次调用里各自命中。
	err := svc.bg.Registry.Add(context.Background(), ref,
		interfaces.BackgroundActivity{ID: "sj_0123", Kind: "shell", Title: "build"}, time.Minute, 0)
	require.NoError(t, err)
	_, err = svc.bg.Inbox.Push(context.Background(), ref, interfaces.BackgroundNotice{
		ID: "sj_ab01", Kind: "shell",
		Text:       "Background shell sj_ab01 succeeded: tests. Use work_wait(wait_sec=0) to read its output.",
		FinishedAt: time.Now().UTC(),
	})
	require.NoError(t, err)

	results, err := svc.Status(context.Background(), ref,
		[]string{"sj_0123", "sj_ab01", "sj_fedc"},
		interfaces.StatusOptions{})
	require.NoError(t, err, "廉价查不到不是整批错误：work_wait 只对结果里的 unknown 补详细读")
	require.Len(t, results, 3)
	require.Equal(t, interfaces.WorkStateRunning, results[0].State)
	require.Equal(t, "build", results[0].Title)
	require.Equal(t, interfaces.WorkStateSucceeded, results[1].State, "从通知文本解析终态")
	require.True(t, results[1].Terminal)
	require.Equal(t, interfaces.WorkStateUnknown, results[2].State,
		"两处都没有 → 逐条 unknown，work_wait 的 poll 立即补一次 Detailed=true（work.go 只认这个）")
	require.Zero(t, reader.execCalls, "Detailed=false 绝不发 exec")

	// 格式非法才整批拒绝（接口契约允许的 ErrWorkNotFound 用法）。
	_, err = svc.Status(context.Background(), ref, []string{"sa_0123456789ab"}, interfaces.StatusOptions{})
	require.ErrorIs(t, err, interfaces.ErrWorkNotFound)
}

func TestApplyOutputFilesReportsNewDeliverables(t *testing.T) {
	mod := time.Unix(100, 0)
	svc := &ShellJobService{
		listFiles: func(context.Context, string, string) ([]sandbox.RemoteDirEntry, error) {
			return []sandbox.RemoteDirEntry{{
				Name: "b.txt", Path: "/workspace/output/b.txt",
				Type: sandbox.RemoteEntryFile, Size: 3, ModTime: mod,
			}}, nil
		},
	}
	meta := sandbox.ShellJobMeta{OutputDir: "/workspace/output", OutputSeen: true}
	result := interfaces.WorkResult{
		State: interfaces.WorkStateSucceeded, Terminal: true, Payload: ShellJobPayload{},
	}
	got := svc.applyOutputFiles(context.Background(), interfaces.SessionRef{SessionID: "s"}, result, meta)
	require.Equal(t, []string{"sandbox:b.txt"}, got.OutputFiles)
}

func TestShellJobResultNotes(t *testing.T) {
	snap := sandbox.ShellJobSnapshot{
		Status: sandbox.ShellJobStatus{State: interfaces.WorkStateRunning},
	}
	r := ShellJobResult(snap, false)
	require.Equal(t, interfaces.WorkStateRunning, r.State)
	require.False(t, r.Terminal)
	payload := r.Payload.(ShellJobPayload)
	require.Contains(t, payload.Note, "Call work_wait only when you cannot continue")
	require.Contains(t, payload.Note, "Do not pipe the command through tail or head")
	r = ShellJobResult(snap, true)
	payload = r.Payload.(ShellJobPayload)
	require.Contains(t, payload.Note, "WeKnora Lite is closed")
	snap.Status.State = interfaces.WorkStateLost
	payload = ShellJobResult(snap, false).Payload.(ShellJobPayload)
	require.Contains(t, payload.Note, "watcher died")
}

func TestShellJobDurationKeepsFractionalSeconds(t *testing.T) {
	start, err := time.Parse(time.RFC3339Nano, "2026-10-18T04:39:01.100Z")
	require.NoError(t, err)
	end, err := time.Parse(time.RFC3339Nano, "2026-10-18T04:39:01.340Z")
	require.NoError(t, err)
	got := shellJobDurationMS(sandbox.ShellJobStatus{StartedAt: start, EndedAt: &end})
	require.Equal(t, int64(240), got)
}

func TestSkillEnvCaptureRunsOnlyAfterSuccess(t *testing.T) {
	svc := &ShellJobService{}
	ref := interfaces.SessionRef{TenantID: 1, SessionID: "session-1"}
	calls := 0
	hold := func() {
		svc.HoldSkillEnvCapture(context.Background(), ref, "sj_ab12", func(context.Context) { calls++ })
	}
	hold()
	svc.skillCapture.settle(context.Background(), ref, "sj_ab12", interfaces.WorkStateFailed)
	require.Zero(t, calls)
	hold()
	svc.skillCapture.drop(ref, "sj_ab12")
	svc.skillCapture.settle(context.Background(), ref, "sj_ab12", interfaces.WorkStateSucceeded)
	require.Zero(t, calls, "lost or cancelled jobs drop the pending write")
	hold()
	svc.skillCapture.dropSession(ref)
	svc.skillCapture.settle(context.Background(), ref, "sj_ab12", interfaces.WorkStateSucceeded)
	require.Zero(t, calls, "deleting the session drops the pending write")
	hold()
	svc.skillCapture.settle(context.Background(), ref, "sj_ab12", interfaces.WorkStateSucceeded)
	require.Equal(t, 1, calls)
}

// A lost submit epilogue must not turn an already-finished job into background
// work. That used to hand the model the result inline and then deliver a
// completion notice for the same id, which cost it an extra reasoning round.
func TestSubmitReportsAFinishedJobInlineWhenTheEpilogueIsLost(t *testing.T) {
	svc, _, index, bg := newRemoteServiceFixture(t)
	ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
	exit := 0
	svc.remoteRead = func(_ context.Context, _ interfaces.SessionRef,
		ids []string, _ map[string]string,
	) ([]sandbox.ShellJobSnapshot, map[string]error) {
		out := make([]sandbox.ShellJobSnapshot, len(ids))
		for i, id := range ids {
			out[i] = sandbox.ShellJobSnapshot{
				Meta: sandbox.ShellJobMeta{
					ID: id, TenantID: ref.TenantID, SessionID: ref.SessionID, Title: "t",
				},
				Status: sandbox.ShellJobStatus{
					State: interfaces.WorkStateSucceeded, ExitCode: &exit,
				},
				WatcherAlive: true,
			}
		}
		return out, nil
	}

	// No marker is ever fed to OnOutput, so ParseShellJobEpilogue fails and the
	// submit cannot know whether the job finished inside the yield window.
	sub, err := svc.SubmitShellJob(context.Background(), ref, tools.ShellJobSubmitRequest{
		Command: "true", YieldTime: time.Second, RunID: "m1",
	})
	require.NoError(t, err)
	require.False(t, sub.Background, "a finished job must be answered inline")
	require.Equal(t, interfaces.WorkStateSucceeded, sub.Result.State)

	items, err := bg.Registry.List(context.Background(), ref)
	require.NoError(t, err)
	require.Empty(t, items, "no activity registration: the sweeper would report it again")
	due, err := index.Due(context.Background(), time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.Empty(t, due, "no index member: nothing left for the sweeper to find")
	pending, err := bg.Inbox.Pending(context.Background(), ref)
	require.NoError(t, err)
	require.Empty(t, pending, "no completion notice for a result already returned")
}

// The same lost epilogue must still register a job that is genuinely running.
func TestSubmitRegistersAJobWhoseEpilogueIsLostButStillRuns(t *testing.T) {
	svc, _, index, bg := newRemoteServiceFixture(t)
	ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
	svc.remoteRead = func(_ context.Context, _ interfaces.SessionRef,
		ids []string, _ map[string]string,
	) ([]sandbox.ShellJobSnapshot, map[string]error) {
		out := make([]sandbox.ShellJobSnapshot, len(ids))
		for i, id := range ids {
			out[i] = sandbox.ShellJobSnapshot{
				Meta: sandbox.ShellJobMeta{
					ID: id, TenantID: ref.TenantID, SessionID: ref.SessionID, Title: "t",
				},
				Status: sandbox.ShellJobStatus{
					State: interfaces.WorkStateRunning, StartedAt: time.Now().UTC(),
				},
				WatcherAlive: true,
			}
		}
		return out, nil
	}

	sub, err := svc.SubmitShellJob(context.Background(), ref, tools.ShellJobSubmitRequest{
		Command: "sleep 30", YieldTime: time.Second, RunID: "m1",
	})
	require.NoError(t, err)
	require.True(t, sub.Background, "a running job must stay background work")

	items, err := bg.Registry.List(context.Background(), ref)
	require.NoError(t, err)
	require.Len(t, items, 1)
	due, err := index.Due(context.Background(), time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.Len(t, due, 1)
}

// stderr that looks like an epilogue must not be parsed as the job's status.
// A later replica-silent backend (Cube) also has to be parsed from the
// buffered exec result, and that buffer must not be fed a second time.
func TestSubmitIgnoresStderrWhenParsingTheEpilogue(t *testing.T) {
	svc, _, _, _ := newRemoteServiceFixture(t)
	ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
	svc.remoteRead = func(context.Context, interfaces.SessionRef, []string, map[string]string) (
		[]sandbox.ShellJobSnapshot, map[string]error,
	) {
		t.Fatal("a parsed epilogue must not fall through to a status read")
		return nil, nil
	}
	svc.remoteExec = func(
		_ context.Context, _ string, script string, opts sandbox.ShellExecOptions,
	) (*sandbox.ExecuteResult, error) {
		marker := shellJobMarkerInScript.FindString(script)
		require.NotEmpty(t, marker)
		stdout := "hello\n" + marker + "\n" + `{"state":"succeeded","exit_code":0}` + "\n4 5\n"
		if opts.OnOutput != nil {
			opts.OnOutput("stdout", []byte(stdout))
			opts.OnOutput("stderr", []byte("noise\n1 2\n"))
		}
		// Already delivered via OnOutput. Re-feeding this would replace the offsets.
		return &sandbox.ExecuteResult{ExitCode: 0, Stdout: "9 9\n", Stderr: "1 2\n"}, nil
	}

	sub, err := svc.SubmitShellJob(context.Background(), ref, tools.ShellJobSubmitRequest{
		Command: "true", YieldTime: time.Second, RunID: "m1",
	})
	require.NoError(t, err)
	require.False(t, sub.Background)
	require.Equal(t, interfaces.WorkStateSucceeded, sub.Result.State)
	payload := sub.Result.Payload.(ShellJobPayload)
	require.Contains(t, payload.Stdout, "hello")
	require.NotContains(t, payload.Stdout, "noise")
	require.Contains(t, payload.Stderr, "noise")
	require.Equal(t, int64(4), payload.TotalBytes.Stdout)
	require.Equal(t, int64(5), payload.TotalBytes.Stderr)
	require.NotNil(t, payload.ExitCode)
	require.Zero(t, *payload.ExitCode)
}

func TestSubmitParsesEpilogueFromExecResultWhenOnOutputIsSilent(t *testing.T) {
	svc, _, _, _ := newRemoteServiceFixture(t)
	ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
	svc.remoteRead = func(context.Context, interfaces.SessionRef, []string, map[string]string) (
		[]sandbox.ShellJobSnapshot, map[string]error,
	) {
		t.Fatal("buffered stdout must be parsed without a status read")
		return nil, nil
	}
	svc.remoteExec = func(
		_ context.Context, _ string, script string, _ sandbox.ShellExecOptions,
	) (*sandbox.ExecuteResult, error) {
		marker := shellJobMarkerInScript.FindString(script)
		require.NotEmpty(t, marker)
		stdout := "hello\n" + marker + "\n" + `{"state":"succeeded","exit_code":0}` + "\n4 5\n"
		return &sandbox.ExecuteResult{ExitCode: 0, Stdout: stdout, Stderr: "noise\n"}, nil
	}

	sub, err := svc.SubmitShellJob(context.Background(), ref, tools.ShellJobSubmitRequest{
		Command: "true", YieldTime: time.Second, RunID: "m1",
	})
	require.NoError(t, err)
	require.False(t, sub.Background)
	require.Equal(t, interfaces.WorkStateSucceeded, sub.Result.State)
	payload := sub.Result.Payload.(ShellJobPayload)
	require.Contains(t, payload.Stdout, "hello")
	require.Contains(t, payload.Stderr, "noise")
	require.Equal(t, int64(4), payload.TotalBytes.Stdout)
}

func TestSweepSettlesSkillCaptureFromRecordedOutcome(t *testing.T) {
	sweeper, reader, index, bg := newRemoteServiceFixture(t)
	submitter := NewShellJobService(nil, bg, index, nil, keeperFunc(
		func(context.Context, interfaces.SessionRef) error { return nil }))
	ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
	ctx := context.Background()
	calls := 0
	submitter.HoldSkillEnvCapture(ctx, ref, "sj_0123", func(context.Context) { calls++ })

	exit := 0
	now := time.Now().UTC()
	reader.snapshots["sj_0123"] = sandbox.ShellJobSnapshot{
		Meta: sandbox.ShellJobMeta{ID: "sj_0123", TenantID: 7, SessionID: "s", SandboxID: "sbx1"},
		Status: sandbox.ShellJobStatus{
			State: interfaces.WorkStateSucceeded, ExitCode: &exit, StartedAt: now,
		},
	}
	require.NoError(t, index.Add(ctx, ShellJobIndexEntry{
		SessionRef: ref, WorkID: "sj_0123", SandboxID: "sbx1", YieldUntil: now.Add(-time.Minute),
	}))
	require.NoError(t, sweeper.SweepOnce(ctx))
	require.Zero(t, calls, "the replica that finishes the job does not hold the credential callback")
	require.NoError(t, submitter.SweepOnce(ctx))
	require.Equal(t, 1, calls, "the submitting replica applies the recorded success")
	require.NoError(t, submitter.SweepOnce(ctx))
	require.Equal(t, 1, calls, "a later sweep must not run the write-back again")

	failed := 0
	submitter.HoldSkillEnvCapture(ctx, ref, "sj_ab12", func(context.Context) { failed++ })
	require.NoError(t, index.RecordOutcome(ctx, ref, "sj_ab12", interfaces.WorkStateFailed))
	require.NoError(t, submitter.SweepOnce(ctx))
	require.Zero(t, failed, "a failed outcome drops the write-back")
	require.NoError(t, index.RecordOutcome(ctx, ref, "sj_ab12", interfaces.WorkStateSucceeded))
	require.NoError(t, submitter.SweepOnce(ctx))
	require.Zero(t, failed, "the dropped write-back is not revived by a later success")
}

func TestShellJobCursorIsSharedAcrossReplicas(t *testing.T) {
	svcA, _, index, bg := newRemoteServiceFixture(t)
	svcB := NewShellJobService(nil, bg, index, nil, keeperFunc(
		func(context.Context, interfaces.SessionRef) error { return nil }))
	ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
	const id = "sj_0123"
	svcA.remoteRead = func(_ context.Context, _ interfaces.SessionRef, ids []string, _ map[string]string) (
		[]sandbox.ShellJobSnapshot, map[string]error,
	) {
		out := make([]sandbox.ShellJobSnapshot, len(ids))
		for i, jobID := range ids {
			out[i] = sandbox.ShellJobSnapshot{
				Meta: sandbox.ShellJobMeta{
					ID: jobID, TenantID: ref.TenantID, SessionID: ref.SessionID, SandboxID: "sbx1",
				},
				Status:       sandbox.ShellJobStatus{State: interfaces.WorkStateRunning},
				Stdout:       sandbox.ShellJobStream{Text: "hello", TotalBytes: 5},
				WatcherAlive: true,
			}
		}
		return out, nil
	}
	_, err := svcA.Status(context.Background(), ref, []string{id}, interfaces.StatusOptions{
		Detailed: true, RunID: "m1",
	})
	require.NoError(t, err)

	var script string
	var opts sandbox.ShellExecOptions
	svcB.remoteExec = func(
		_ context.Context, _ string, got string, gotOpts sandbox.ShellExecOptions,
	) (*sandbox.ExecuteResult, error) {
		script = got
		opts = gotOpts
		return &sandbox.ExecuteResult{ExitCode: 0, Stdout: `{"meta":{"work_id":"sj_0123"},"missing":true}`}, nil
	}
	readCtx := context.WithValue(context.Background(), shellReadRunKey{}, "m1")
	_, errs := svcB.batchReadViaPinned(readCtx, ref, []string{id}, map[string]string{id: "sbx1"})
	require.Empty(t, errs)
	require.Contains(t, script, "SF='5'", "the other replica must resume at the cursor this one stored")
	require.Equal(t, "sbx1", opts.ExpectedSandboxID)
	require.True(t, opts.SkipWorkspacePrep)
}

func TestSweepKillsOrphansWhenTheWatcherDies(t *testing.T) {
	svc, reader, index, bg := newRemoteServiceFixture(t)
	ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
	now := time.Now().UTC()
	reader.snapshots["sj_0123"] = sandbox.ShellJobSnapshot{
		Meta: sandbox.ShellJobMeta{ID: "sj_0123", TenantID: 7, SessionID: "s", SandboxID: "sbx1"},
		Status: sandbox.ShellJobStatus{
			State: interfaces.WorkStateRunning, StartedAt: now, PGID: 99, TouchPID: 77,
		},
		WatcherAlive: false,
	}
	require.NoError(t, index.Add(context.Background(), ShellJobIndexEntry{
		SessionRef: ref, WorkID: "sj_0123", SandboxID: "sbx1", YieldUntil: now.Add(-time.Minute),
	}))
	var script string
	var opts sandbox.ShellExecOptions
	svc.remoteExec = func(
		_ context.Context, _ string, got string, gotOpts sandbox.ShellExecOptions,
	) (*sandbox.ExecuteResult, error) {
		script = got
		opts = gotOpts
		return &sandbox.ExecuteResult{ExitCode: 0}, nil
	}
	require.NoError(t, svc.SweepOnce(context.Background()))
	require.Contains(t, script, "kill -TERM")
	require.Contains(t, script, "99")
	require.Contains(t, script, "77")
	require.Contains(t, script, "weknora-sandbox-activity")
	require.Equal(t, "sbx1", opts.ExpectedSandboxID)
	require.True(t, opts.SkipWorkspacePrep)
	notices, err := bg.Inbox.Pending(context.Background(), ref)
	require.NoError(t, err)
	require.Len(t, notices, 1)
	require.Contains(t, notices[0].Text, "lost")
}

func TestSweepDoesNotKillInsideAReplacedSandbox(t *testing.T) {
	svc, reader, index, _ := newRemoteServiceFixture(t)
	ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
	now := time.Now().UTC()
	reader.snapshots["sj_0123"] = sandbox.ShellJobSnapshot{
		Meta: sandbox.ShellJobMeta{ID: "sj_0123", TenantID: 7, SessionID: "s", SandboxID: "old"},
		Status: sandbox.ShellJobStatus{
			State: interfaces.WorkStateRunning, StartedAt: now, PGID: 99, TouchPID: 77,
		},
		WatcherAlive: false,
	}
	require.NoError(t, index.Add(context.Background(), ShellJobIndexEntry{
		SessionRef: ref, WorkID: "sj_0123", SandboxID: "new", YieldUntil: now.Add(-time.Minute),
	}))
	called := false
	svc.remoteExec = func(context.Context, string, string, sandbox.ShellExecOptions) (*sandbox.ExecuteResult, error) {
		called = true
		return &sandbox.ExecuteResult{ExitCode: 0}, nil
	}
	require.NoError(t, svc.SweepOnce(context.Background()))
	require.False(t, called, "a replaced sandbox is already gone; do not exec a kill into the new one")
}

func miniredisRunT(t *testing.T) *miniredis.Miniredis { return miniredis.RunT(t) }

func redisNewClient(mr *miniredis.Miniredis) *redis.Client {
	return redis.NewClient(&redis.Options{Addr: mr.Addr()})
}
