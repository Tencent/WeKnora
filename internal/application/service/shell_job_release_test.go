package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type sweepReleaseHandoffHook struct {
	once    sync.Once
	handoff func()
}

func (h *sweepReleaseHandoffHook) DialHook(next redis.DialHook) redis.DialHook {
	return next
}

func (h *sweepReleaseHandoffHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

func (h *sweepReleaseHandoffHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		switch cmd.Name() {
		case "del", "eval", "evalsha":
			h.once.Do(h.handoff)
		}
		return next(ctx, cmd)
	}
}

func TestShellJobIndexReleaseKeepsSuccessorClaim(t *testing.T) {
	ctx := context.Background()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	otherClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close(); _ = otherClient.Close() })
	first := NewShellJobIndex(client, "weknora")
	second := NewShellJobIndex(otherClient, "weknora")
	ref := interfaces.SessionRef{TenantID: 7, SessionID: "handoff"}
	now := time.Unix(1006, 0)
	require.NoError(t, first.Add(ctx, ShellJobIndexEntry{
		SessionRef: ref, WorkID: "sj_1234", SandboxID: "sandbox", YieldUntil: time.Unix(1000, 0),
	}))
	claimed, err := first.Claim(ctx, now, "replica-a")
	require.NoError(t, err)
	require.Len(t, claimed[ref], 1)
	handedOff := false
	client.AddHook(&sweepReleaseHandoffHook{handoff: func() {
		// Transfer the expired lease immediately before the release's write is sent.
		// This also exercises atomic releases: their comparison must see replica-b.
		mr.FastForward(shellJobSweepTTL)
		successor, claimErr := second.Claim(ctx, now, "replica-b")
		require.NoError(t, claimErr)
		require.Len(t, successor[ref], 1)
		handedOff = true
	}})
	require.NoError(t, first.Release(ctx, ref, "replica-a"))
	require.True(t, handedOff, "the lease handoff must actually occur")
	third, err := second.Claim(ctx, now, "replica-c")
	require.NoError(t, err)
	require.Empty(t, third, "the previous owner must not delete its successor's claim")
	require.NoError(t, second.Release(ctx, ref, "replica-b"))
	third, err = second.Claim(ctx, now, "replica-c")
	require.NoError(t, err)
	require.Len(t, third[ref], 1, "the current owner must still be able to release")
}
