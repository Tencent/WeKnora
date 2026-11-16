package hitl

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// seedSuspended stores a suspended prompt for message msgID with saved run
// state, expiring at expires.
func seedSuspended(
	t *testing.T, b *Broker, id, msgID string, expires time.Time,
) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, b.Store().Create(ctx, &types.AgentPrompt{
		ID: id, TenantID: 1, SessionID: "s1", AssistantMessageID: msgID, Kind: testKind,
		Status: types.AgentPromptSuspended, Suspendable: true, ExpiresAt: &expires,
		Request: types.JSON(`{"question":"color?"}`),
	}))
	require.NoError(t, b.Suspensions().Save(ctx, &types.AgentRunSuspension{
		AssistantMessageID: msgID, TenantID: 1, SessionID: "s1", PromptID: id,
		SnapshotVersion: 1, Snapshot: types.JSON(`{"version":1}`),
	}))
}

type hookLog struct {
	suspended []string
	closed    map[string]string
}

func (h *hookLog) hooks() RunHooks {
	h.closed = map[string]string{}
	return RunHooks{
		Suspended: func(_ context.Context, p *types.AgentPrompt, _ *types.AgentRunSuspension) error {
			h.suspended = append(h.suspended, p.ID)
			return nil
		},
		Closed: func(_ context.Context, p *types.AgentPrompt, _ *types.AgentRunSuspension, reason string) error {
			h.closed[p.ID] = reason
			return nil
		},
	}
}

func TestBroker_ResolveSuspendedHandsRunToCaller(t *testing.T) {
	ctx := context.Background()
	b := newTestBroker(NewMemoryStore())
	seedSuspended(t, b, "p1", "m1", time.Now().Add(time.Hour))

	res, err := b.Resolve(ctx, answer("p1", "blue"))
	require.NoError(t, err)
	require.True(t, res.Resume)
	require.Equal(t, types.AgentPromptAnswered, res.Prompt.Status)

	row, err := b.Store().Get(ctx, "p1")
	require.NoError(t, err)
	require.Equal(t, types.AgentPromptAnswered, row.Status)
	require.Equal(t, res.Prompt.OwnerInstance, row.OwnerInstance, "the resuming replica holds the lease")
	require.NotNil(t, row.LeaseUntil)
	require.True(t, row.LeaseUntil.After(time.Now()))

	require.ErrorIs(t, resolveErr(ctx, b, answer("p1", "red")), ErrAlreadyResolved)
}

func TestBroker_ResolveRejectsExpiredSuspension(t *testing.T) {
	ctx := context.Background()
	b := newTestBroker(NewMemoryStore())
	seedSuspended(t, b, "p1", "m1", time.Now().Add(-time.Second))

	require.ErrorIs(t, resolveErr(ctx, b, answer("p1", "blue")), ErrAlreadyResolved)
	row, err := b.Store().Get(ctx, "p1")
	require.NoError(t, err)
	require.Equal(t, types.AgentPromptSuspended, row.Status, "left for the reaper to expire")
}

func TestBroker_KeepLeaseRenewsUntilPromptCloses(t *testing.T) {
	ctx := context.Background()
	b := NewBroker(nil, nil, withLease(80*time.Millisecond, 10*time.Millisecond))
	b.RegisterKind(testKind, validateTestAnswer)
	seedSuspended(t, b, "p1", "m1", time.Now().Add(time.Hour))
	res, err := b.Resolve(ctx, answer("p1", "blue"))
	require.NoError(t, err)

	stop := b.KeepLease(ctx, "p1", res.Prompt.OwnerInstance)
	defer stop()
	time.Sleep(200 * time.Millisecond)
	row, err := b.Store().Get(ctx, "p1")
	require.NoError(t, err)
	require.True(t, row.LeaseUntil.After(time.Now()), "lease kept past its TTL")

	n, err := b.Reap(ctx, time.Now(), RunHooks{})
	require.NoError(t, err)
	require.Zero(t, n, "a run with a live lease is not reaped")
}

func TestBroker_ReapClosesAbandonedResumeAndExpiredSuspension(t *testing.T) {
	ctx := context.Background()
	b := newTestBroker(NewMemoryStore())
	past, future := time.Now().Add(-time.Minute), time.Now().Add(time.Hour)

	// Resumed, then the resuming replica died before and during the tool.
	for id, status := range map[string]string{
		"answered": types.AgentPromptAnswered, "executing": types.AgentPromptExecuting,
	} {
		seedSuspended(t, b, id, "m-"+id, future)
		ok, err := b.Store().Transition(ctx, id, types.AgentPromptTransition{
			From: []string{types.AgentPromptSuspended}, To: status, LeaseUntil: &past,
		})
		require.NoError(t, err)
		require.True(t, ok)
	}
	// Answered in the hot wait: its waiter dropped the run state.
	require.NoError(t, b.Store().Create(ctx, &types.AgentPrompt{
		ID: "hot", TenantID: 1, SessionID: "s1", AssistantMessageID: "m-hot", Kind: testKind,
		Status: types.AgentPromptExecuting, Suspendable: true, LeaseUntil: &past,
	}))
	seedSuspended(t, b, "old", "m-old", past)
	seedSuspended(t, b, "fresh", "m-fresh", future)

	log := &hookLog{}
	n, err := b.Reap(ctx, time.Now(), log.hooks())
	require.NoError(t, err)
	require.Equal(t, 3, n)
	require.Equal(t, map[string]string{
		"answered":  ReasonResumeStopped,
		"executing": ReasonExecuteStopped,
		"old":       ReasonPromptExpired,
	}, log.closed)

	for id, want := range map[string]string{
		"answered":  types.AgentPromptFailed,
		"executing": types.AgentPromptFailed,
		"hot":       types.AgentPromptExecuting,
		"old":       types.AgentPromptExpired,
		"fresh":     types.AgentPromptSuspended,
	} {
		row, err := b.Store().Get(ctx, id)
		require.NoError(t, err)
		require.Equal(t, want, row.Status, id)
	}
	for msg, kept := range map[string]bool{"m-answered": false, "m-old": false, "m-fresh": true} {
		saved, err := b.Suspensions().Get(ctx, msg)
		require.NoError(t, err)
		require.Equal(t, kept, saved != nil, msg)
	}
}

func TestBroker_SupersedeSessionClosesOnlyItsSuspendedPrompts(t *testing.T) {
	ctx := context.Background()
	b := newTestBroker(NewMemoryStore())
	seedSuspended(t, b, "p1", "m1", time.Now().Add(time.Hour))
	require.NoError(t, b.Store().Create(ctx, &types.AgentPrompt{
		ID: "other", TenantID: 1, SessionID: "s2", AssistantMessageID: "m2", Kind: testKind,
		Status: types.AgentPromptSuspended, Suspendable: true,
	}))

	log := &hookLog{}
	n, err := b.SupersedeSession(ctx, "s1", log.hooks())
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, map[string]string{"p1": ReasonPromptSupersede}, log.closed)

	row, err := b.Store().Get(ctx, "p1")
	require.NoError(t, err)
	require.Equal(t, types.AgentPromptSuperseded, row.Status)
	require.ErrorIs(t, resolveErr(ctx, b, answer("p1", "blue")), ErrAlreadyResolved)

	other, err := b.Store().Get(ctx, "other")
	require.NoError(t, err)
	require.Equal(t, types.AgentPromptSuspended, other.Status)
}
