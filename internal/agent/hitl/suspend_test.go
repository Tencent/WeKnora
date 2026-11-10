package hitl

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func suspendableSpec(hotWait time.Duration, emit func(ctx context.Context, p *types.AgentPrompt) error) Spec {
	spec := testSpec(hotWait, emit)
	spec.Suspend = &SuspendSpec{Snapshot: []byte(`{"version":1}`), SnapshotVersion: 1}
	return spec
}

func TestBroker_HotWaitEndsInSuspensionAndKeepsRunState(t *testing.T) {
	b := NewBroker(nil, nil, WithPollInterval(20*time.Millisecond), WithSuspendExpiry(time.Hour))
	b.RegisterKind(testKind, validateTestAnswer)

	before := time.Now()
	out, err := b.Await(context.Background(), suspendableSpec(100*time.Millisecond, nil))
	require.NoError(t, err)
	require.True(t, out.Suspended)
	require.False(t, out.TimedOut)
	require.NotNil(t, out.ExpiresAt)
	require.WithinDuration(t, before.Add(time.Hour), *out.ExpiresAt, 5*time.Second)

	row, err := b.Store().Get(context.Background(), out.Prompt.ID)
	require.NoError(t, err)
	require.Equal(t, types.AgentPromptSuspended, row.Status)
	require.True(t, row.Suspendable)

	saved, err := b.Suspensions().Get(context.Background(), "m1")
	require.NoError(t, err)
	require.NotNil(t, saved)
	require.Equal(t, out.Prompt.ID, saved.PromptID)
	require.JSONEq(t, `{"version":1}`, string(saved.Snapshot))
}

func TestBroker_NegativeExpiryNeverExpires(t *testing.T) {
	b := NewBroker(nil, nil, WithPollInterval(20*time.Millisecond), WithSuspendExpiry(0))
	out, err := b.Await(context.Background(), suspendableSpec(50*time.Millisecond, nil))
	require.NoError(t, err)
	require.True(t, out.Suspended)
	require.Nil(t, out.ExpiresAt)
}

func TestBroker_AnsweredInHotWaitDropsRunState(t *testing.T) {
	b := newTestBroker(NewMemoryStore())
	out, err := b.Await(context.Background(), suspendableSpec(5*time.Second,
		func(_ context.Context, p *types.AgentPrompt) error {
			go func() { require.NoError(t, resolveErr(context.Background(), b, answer(p.ID, "blue"))) }()
			return nil
		}))
	require.NoError(t, err)
	require.True(t, out.Answered)
	require.False(t, out.Suspended)

	saved, err := b.Suspensions().Get(context.Background(), "m1")
	require.NoError(t, err)
	require.Nil(t, saved)
}

func TestBroker_CanceledInHotWaitDropsRunState(t *testing.T) {
	b := newTestBroker(NewMemoryStore())
	ctx, cancel := context.WithCancel(context.Background())
	out, err := b.Await(ctx, suspendableSpec(time.Minute,
		func(context.Context, *types.AgentPrompt) error { cancel(); return nil }))
	require.NoError(t, err)
	require.True(t, out.Canceled)

	saved, err := b.Suspensions().Get(context.Background(), "m1")
	require.NoError(t, err)
	require.Nil(t, saved)
}

func TestBroker_SuspendableNeedsAssistantMessage(t *testing.T) {
	b := newTestBroker(NewMemoryStore())
	spec := suspendableSpec(time.Second, nil)
	spec.Prompt.AssistantMessageID = ""
	_, err := b.Await(context.Background(), spec)
	require.Error(t, err)
}

// An owner whose lease lapsed sees another replica's takeover as a
// suspension, not as a prompt still waiting on it.
func TestBroker_OwnerSeesTakeoverAsSuspension(t *testing.T) {
	store := NewMemoryStore()
	owner := NewBroker(store, nil, WithPollInterval(20*time.Millisecond), withLease(time.Millisecond, time.Hour))
	reaper := NewBroker(store, nil, WithSuspendExpiry(time.Hour))
	reaper.suspensions = owner.suspensions

	out, err := owner.Await(context.Background(), suspendableSpec(time.Minute,
		func(context.Context, *types.AgentPrompt) error {
			go func() {
				time.Sleep(10 * time.Millisecond)
				n, err := reaper.Reap(context.Background(), time.Now(), RunHooks{})
				require.NoError(t, err)
				require.Equal(t, 1, n)
			}()
			return nil
		}))
	require.NoError(t, err)
	require.True(t, out.Suspended)

	saved, err := owner.Suspensions().Get(context.Background(), "m1")
	require.NoError(t, err)
	require.NotNil(t, saved, "the run state must survive for the resume")
}

func TestBroker_ReapSuspendsOrphanedSuspendablePrompt(t *testing.T) {
	ctx := context.Background()
	store, suspensions := NewMemoryStore(), NewMemorySuspensionStore()
	b := NewBroker(store, nil, WithSuspensionStore(suspensions), WithSuspendExpiry(time.Hour))

	past := time.Now().Add(-time.Minute)
	require.NoError(t, store.Create(ctx, &types.AgentPrompt{
		ID: "p1", TenantID: 1, SessionID: "s1", AssistantMessageID: "m1", Kind: testKind,
		Status: types.AgentPromptPending, Suspendable: true, LeaseUntil: &past,
	}))
	require.NoError(t, suspensions.Save(ctx, &types.AgentRunSuspension{
		AssistantMessageID: "m1", TenantID: 1, SessionID: "s1", PromptID: "p1",
		SnapshotVersion: 1, Snapshot: types.JSON(`{"version":1}`),
	}))

	var hooked *types.AgentRunSuspension
	n, err := b.Reap(ctx, time.Now(), RunHooks{Suspended: func(
		_ context.Context, p *types.AgentPrompt, s *types.AgentRunSuspension,
	) error {
		require.Equal(t, "p1", p.ID)
		hooked = s
		return nil
	}})
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.NotNil(t, hooked)

	row, err := store.Get(ctx, "p1")
	require.NoError(t, err)
	require.Equal(t, types.AgentPromptSuspended, row.Status)
	require.NotNil(t, row.ExpiresAt)

	n, err = b.Reap(ctx, time.Now(), RunHooks{})
	require.NoError(t, err)
	require.Zero(t, n, "a suspended prompt is not reaped again")
}

func TestBroker_ReapExpiresOrphanWithoutRunState(t *testing.T) {
	ctx := context.Background()
	store, suspensions := NewMemoryStore(), NewMemorySuspensionStore()
	b := NewBroker(store, nil, WithSuspensionStore(suspensions))

	past, future := time.Now().Add(-time.Minute), time.Now().Add(time.Minute)
	require.NoError(t, store.Create(ctx, &types.AgentPrompt{
		ID: "plain", TenantID: 1, SessionID: "s1", AssistantMessageID: "m1", Kind: testKind,
		Status: types.AgentPromptPending, LeaseUntil: &past,
	}))
	// Suspendable, but its saved state belongs to a newer prompt.
	require.NoError(t, store.Create(ctx, &types.AgentPrompt{
		ID: "stale", TenantID: 1, SessionID: "s1", AssistantMessageID: "m2", Kind: testKind,
		Status: types.AgentPromptPending, Suspendable: true, LeaseUntil: &past,
	}))
	require.NoError(t, suspensions.Save(ctx, &types.AgentRunSuspension{
		AssistantMessageID: "m2", PromptID: "newer", Snapshot: types.JSON(`{}`),
	}))
	require.NoError(t, store.Create(ctx, &types.AgentPrompt{
		ID: "alive", TenantID: 1, SessionID: "s1", AssistantMessageID: "m3", Kind: testKind,
		Status: types.AgentPromptPending, LeaseUntil: &future,
	}))

	called := false
	n, err := b.Reap(ctx, time.Now(), RunHooks{Suspended: func(
		context.Context, *types.AgentPrompt, *types.AgentRunSuspension,
	) error {
		called = true
		return nil
	}})
	require.NoError(t, err)
	require.Equal(t, 2, n)
	require.False(t, called)

	for id, want := range map[string]string{
		"plain": types.AgentPromptExpired,
		"stale": types.AgentPromptExpired,
		"alive": types.AgentPromptPending,
	} {
		row, err := store.Get(ctx, id)
		require.NoError(t, err)
		require.Equal(t, want, row.Status, id)
	}
}

// An answer to a pending prompt whose owner stopped renewing suspends it as
// the reaper would and resumes the run, instead of waking nobody.
func TestBroker_ResolveTakesOverPromptOfVanishedOwner(t *testing.T) {
	ctx := context.Background()
	store, suspensions := NewMemoryStore(), NewMemorySuspensionStore()
	b := NewBroker(store, nil, WithSuspensionStore(suspensions), WithSuspendExpiry(time.Hour))
	b.RegisterKind(testKind, validateTestAnswer)
	var hooked bool
	b.SetRunHooks(RunHooks{Suspended: func(context.Context, *types.AgentPrompt, *types.AgentRunSuspension) error {
		hooked = true
		return nil
	}})

	past := time.Now().Add(-time.Minute)
	require.NoError(t, store.Create(ctx, &types.AgentPrompt{
		ID: "p1", TenantID: 1, UserID: "alice", SessionID: "s1", AssistantMessageID: "m1", Kind: testKind,
		Status: types.AgentPromptPending, Suspendable: true, LeaseUntil: &past, OwnerInstance: "dead",
	}))
	require.NoError(t, suspensions.Save(ctx, &types.AgentRunSuspension{
		AssistantMessageID: "m1", TenantID: 1, SessionID: "s1", PromptID: "p1",
		SnapshotVersion: 1, Snapshot: types.JSON(`{"version":1}`),
	}))

	res, err := b.Resolve(ctx, answer("p1", "blue"))
	require.NoError(t, err)
	require.True(t, res.Resume)
	require.True(t, res.OwnerGone)
	require.True(t, hooked, "the message must be shown as awaiting input before it resumes")

	row, err := store.Get(ctx, "p1")
	require.NoError(t, err)
	require.Equal(t, types.AgentPromptAnswered, row.Status)
	require.Equal(t, res.Prompt.OwnerInstance, row.OwnerInstance)
}

// A run that suspended itself still has an owner tearing it down.
func TestBroker_ResolveOfSelfSuspendedPromptKeepsOwner(t *testing.T) {
	b := NewBroker(nil, nil, WithPollInterval(20*time.Millisecond), WithSuspendExpiry(time.Hour))
	b.RegisterKind(testKind, validateTestAnswer)
	out, err := b.Await(context.Background(), suspendableSpec(50*time.Millisecond, nil))
	require.NoError(t, err)
	require.True(t, out.Suspended)

	res, err := b.Resolve(context.Background(), answer(out.Prompt.ID, "blue"))
	require.NoError(t, err)
	require.True(t, res.Resume)
	require.False(t, res.OwnerGone)
}

type failingSuspensionStore struct{ SuspensionStore }

func (failingSuspensionStore) Save(context.Context, *types.AgentRunSuspension) error {
	return errors.New("jsonb rejected the snapshot")
}

func TestBroker_UnsavableRunStateFallsBackToPlainWait(t *testing.T) {
	b := NewBroker(nil, nil, WithPollInterval(20*time.Millisecond),
		WithSuspensionStore(failingSuspensionStore{NewMemorySuspensionStore()}))
	spec := suspendableSpec(10*time.Millisecond, func(_ context.Context, p *types.AgentPrompt) error {
		require.False(t, p.Suspendable)
		return nil
	})
	spec.FallbackTimeout = 80 * time.Millisecond

	start := time.Now()
	out, err := b.Await(context.Background(), spec)
	require.NoError(t, err)
	require.False(t, out.Suspended)
	require.True(t, out.TimedOut)
	require.GreaterOrEqual(t, time.Since(start), 80*time.Millisecond)

	spec = suspendableSpec(10*time.Millisecond, nil)
	_, err = b.Await(context.Background(), spec)
	require.Error(t, err, "without a fallback the failure is reported")
}
