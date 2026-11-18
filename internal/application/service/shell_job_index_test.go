package service

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func newIndexFixture(t *testing.T) (*ShellJobIndex, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	return NewShellJobIndex(client, "weknora"), mr
}

func TestShellJobIndexLifecycle(t *testing.T) {
	x, _ := newIndexFixture(t)
	ctx := context.Background()
	ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
	entry := ShellJobIndexEntry{
		SessionRef: ref, WorkID: "sj_0123",
		SandboxID: "sbx1", YieldUntil: time.Unix(1000, 0),
	}

	require.NoError(t, x.Add(ctx, entry))
	// 同一会话第二笔：两笔必须在同一次 Claim 里一起回来——抢占是按会话一次
	// SET NX，不是逐条。逐条 SetNX 会让第二笔被自己刚设的锁挡掉（回归钉子）。
	entry2 := ShellJobIndexEntry{
		SessionRef: ref, WorkID: "sj_ab01",
		SandboxID: "sbx1", YieldUntil: time.Unix(1000, 0),
	}
	require.NoError(t, x.Add(ctx, entry2))
	due, err := x.Due(ctx, time.Unix(1000, 0))
	require.NoError(t, err)
	require.Empty(t, due, "score 含 5 秒交接余量，窗口结束时刻尚不 due")
	due, err = x.Due(ctx, time.Unix(1006, 0))
	require.NoError(t, err)
	require.Len(t, due, 2)
	require.Equal(t, "sbx1", due[0].SandboxID)
	require.True(t, due[0].YieldUntil.Equal(time.Unix(1000, 0).UTC()),
		"到期条目要带回 yield_until，score 里的 5 秒交接余量不算进窗口")

	claimed, err := x.Claim(ctx, time.Unix(1006, 0), "replica-a")
	require.NoError(t, err)
	require.Len(t, claimed[ref], 2, "同一会话的全部到期任务随一次抢占一起返回")
	claimed, err = x.Claim(ctx, time.Unix(1006, 0), "replica-b")
	require.NoError(t, err)
	require.Empty(t, claimed, "另一副本 15 秒内抢不到同一会话")
	require.NoError(t, x.Release(ctx, ref, "replica-a"))
	claimed, err = x.Claim(ctx, time.Unix(1006, 0), "replica-b")
	require.NoError(t, err)
	require.Len(t, claimed[ref], 2)
}

func TestShellJobIndexClaimWorkExactlyOneWinner(t *testing.T) {
	x, _ := newIndexFixture(t)
	ctx := context.Background()
	ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
	require.NoError(t, x.Add(ctx, ShellJobIndexEntry{
		SessionRef: ref,
		WorkID:     "sj_0123", SandboxID: "sbx1", YieldUntil: time.Unix(1000, 0),
	}))

	first, err := x.ClaimWork(ctx, ref, "sj_0123")
	require.NoError(t, err)
	require.True(t, first, "ZREM 返回 1 的一方获得报告权")
	second, err := x.ClaimWork(ctx, ref, "sj_0123")
	require.NoError(t, err)
	require.False(t, second, "第二个 ZREM 返回 0，不报告（D2）")

	require.NoError(t, x.Requeue(ctx, ShellJobIndexEntry{
		SessionRef: ref,
		WorkID:     "sj_0123", SandboxID: "sbx1", YieldUntil: time.Unix(1000, 0),
	}))
	first, err = x.ClaimWork(ctx, ref, "sj_0123")
	require.NoError(t, err)
	require.True(t, first, "Requeue 后下一轮可重试")
}

func TestShellJobIndexSandboxIDs(t *testing.T) {
	x, _ := newIndexFixture(t)
	ctx := context.Background()
	ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
	require.NoError(t, x.Add(ctx, ShellJobIndexEntry{
		SessionRef: ref,
		WorkID:     "sj_0123", SandboxID: "sbx-a", YieldUntil: time.Unix(1000, 0),
	}))
	// 另一会话的同名任务不得串扰
	other := interfaces.SessionRef{TenantID: 7, SessionID: "other"}
	require.NoError(t, x.Add(ctx, ShellJobIndexEntry{
		SessionRef: other,
		WorkID:     "sj_ab01", SandboxID: "sbx-x", YieldUntil: time.Unix(1000, 0),
	}))

	got := x.SandboxIDs(ctx, ref, []string{"sj_0123", "sj_ab01"})
	require.Equal(t, "sbx-a", got["sj_0123"], "expected 沙盒来自索引成员（D1）")
	require.Empty(t, got["sj_ab01"], "索引外的 ID 映射为空，由调用方退回当前绑定")
}

func TestShellJobIndexRemoveSession(t *testing.T) {
	x, _ := newIndexFixture(t)
	ctx := context.Background()
	ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
	require.NoError(t, x.Add(ctx, ShellJobIndexEntry{
		SessionRef: ref,
		WorkID:     "sj_0123", SandboxID: "sbx1", YieldUntil: time.Unix(1000, 0),
	}))
	require.NoError(t, x.Add(ctx, ShellJobIndexEntry{
		SessionRef: ref,
		WorkID:     "sj_ab01", SandboxID: "sbx1", YieldUntil: time.Unix(1000, 0),
	}))
	require.NoError(t, x.RemoveSession(ctx, ref))
	due, err := x.Due(ctx, time.Unix(2000, 0))
	require.NoError(t, err)
	require.Empty(t, due)
}

func TestShellJobIndexOutcomeAndCursor(t *testing.T) {
	x, mr := newIndexFixture(t)
	ctx := context.Background()
	ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
	require.NoError(t, x.RecordOutcome(ctx, ref, "sj_0123", interfaces.WorkStateSucceeded))
	state, ok, err := x.Outcome(ctx, ref, "sj_0123")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, interfaces.WorkStateSucceeded, state)
	ttl := mr.TTL("weknora:background:shelljob:outcome:weknora:7:s:sj_0123")
	require.Greater(t, ttl, 23*time.Hour)
	_, ok, err = x.Outcome(ctx, ref, "sj_ab12")
	require.NoError(t, err)
	require.False(t, ok)

	require.NoError(t, x.PutCursor(ctx, ref, "m1", "sj_0123", sandbox.ShellJobOffsets{Stdout: 5}, time.Hour))
	got, ok, err := x.GetCursor(ctx, ref, "m1", "sj_0123")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, int64(5), got.Stdout)
	require.NoError(t, x.DeleteSessionCursors(ctx, ref))
	_, ok, err = x.GetCursor(ctx, ref, "m1", "sj_0123")
	require.NoError(t, err)
	require.False(t, ok)
}
