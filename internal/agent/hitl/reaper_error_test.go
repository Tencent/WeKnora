package hitl

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// flakySuspensions fails Get for chosen messages, standing in for a
// suspension store that hiccups while the reaper reads it.
type flakySuspensions struct {
	SuspensionStore
	failFor map[string]error
}

func (f *flakySuspensions) Get(
	ctx context.Context, assistantMessageID string,
) (*types.AgentRunSuspension, error) {
	if err := f.failFor[assistantMessageID]; err != nil {
		return nil, err
	}
	return f.SuspensionStore.Get(ctx, assistantMessageID)
}

// seedPendingOrphan stores a pending prompt whose owner stopped renewing its
// lease, with saved run state under msgID.
func seedPendingOrphan(t *testing.T, b *Broker, id, msgID string) {
	t.Helper()
	ctx := context.Background()
	past := time.Now().Add(-time.Minute)
	require.NoError(t, b.Store().Create(ctx, &types.AgentPrompt{
		ID: id, TenantID: 1, SessionID: "s1", AssistantMessageID: msgID, Kind: testKind,
		Status: types.AgentPromptPending, Suspendable: true, LeaseUntil: &past, OwnerInstance: "dead",
	}))
	require.NoError(t, b.Suspensions().Save(ctx, &types.AgentRunSuspension{
		AssistantMessageID: msgID, TenantID: 1, SessionID: "s1", PromptID: id,
		SnapshotVersion: 1, Snapshot: types.JSON(`{"version":1}`),
	}))
}

// A failed state read must not expire a suspendable orphan: the run may
// still be resumable, and expiring the prompt is not reversible.
func TestReapDefersSuspendableOrphanWhenStateReadFails(t *testing.T) {
	ctx := context.Background()
	suspensions := NewMemorySuspensionStore()
	flaky := &flakySuspensions{
		SuspensionStore: suspensions,
		failFor:         map[string]error{"m1": errors.New("store hiccup")},
	}
	b := NewBroker(NewMemoryStore(), nil, WithSuspensionStore(flaky), WithSuspendExpiry(time.Hour))
	b.RegisterKind(testKind, validateTestAnswer)
	hooks := &hookLog{}

	seedPendingOrphan(t, b, "p1", "m1")
	n, err := b.Reap(ctx, time.Now(), hooks.hooks())
	require.NoError(t, err)
	require.Zero(t, n)
	row, err := b.Store().Get(ctx, "p1")
	require.NoError(t, err)
	require.Equal(t, types.AgentPromptPending, row.Status,
		"a failed state read must not expire a resumable run")

	// Once the store answers again, the next sweep suspends the run.
	delete(flaky.failFor, "m1")
	n, err = b.Reap(ctx, time.Now(), hooks.hooks())
	require.NoError(t, err)
	require.Equal(t, 1, n)
	row, err = b.Store().Get(ctx, "p1")
	require.NoError(t, err)
	require.Equal(t, types.AgentPromptSuspended, row.Status)
	require.Equal(t, []string{"p1"}, hooks.suspended)
}

// A suspendable orphan whose run left no state is still expired.
func TestReapStillExpiresOrphanWithoutState(t *testing.T) {
	ctx := context.Background()
	b := newTestBroker(NewMemoryStore())
	hooks := &hookLog{}

	seedPendingOrphan(t, b, "p1", "m1")
	require.NoError(t, b.Suspensions().Delete(ctx, "m1", "p1"))

	n, err := b.Reap(ctx, time.Now(), hooks.hooks())
	require.NoError(t, err)
	require.Equal(t, 1, n)
	row, err := b.Store().Get(ctx, "p1")
	require.NoError(t, err)
	require.Equal(t, types.AgentPromptExpired, row.Status)
}

// Expiring and superseding defer by one sweep when the state read fails,
// instead of closing the run while the hook that completes its message
// cannot read the state it needs.
func TestReapAndSupersedeDeferWhenStateReadFails(t *testing.T) {
	ctx := context.Background()
	suspensions := NewMemorySuspensionStore()
	flaky := &flakySuspensions{
		SuspensionStore: suspensions,
		failFor:         map[string]error{"m1": errors.New("store hiccup")},
	}
	b := NewBroker(NewMemoryStore(), nil, WithSuspensionStore(flaky), WithSuspendExpiry(time.Hour))
	b.RegisterKind(testKind, validateTestAnswer)
	hooks := &hookLog{}

	seedSuspended(t, b, "p1", "m1", time.Now().Add(-time.Second))
	n, err := b.Reap(ctx, time.Now(), hooks.hooks())
	require.NoError(t, err)
	require.Zero(t, n)
	row, err := b.Store().Get(ctx, "p1")
	require.NoError(t, err)
	require.Equal(t, types.AgentPromptSuspended, row.Status)
	require.Empty(t, hooks.closed, "the run must not be closed without its state")

	delete(flaky.failFor, "m1")
	n, err = b.Reap(ctx, time.Now(), hooks.hooks())
	require.NoError(t, err)
	require.Equal(t, 1, n)
	row, err = b.Store().Get(ctx, "p1")
	require.NoError(t, err)
	require.Equal(t, types.AgentPromptExpired, row.Status)
	require.Equal(t, map[string]string{"p1": ReasonPromptExpired}, hooks.closed)

	// SupersedeSession defers the same way.
	seedSuspended(t, b, "p2", "m2", time.Now().Add(time.Hour))
	flaky.failFor["m2"] = errors.New("store hiccup")
	n, err = b.SupersedeSession(ctx, "s1", hooks.hooks())
	require.NoError(t, err)
	require.Zero(t, n)
	row, err = b.Store().Get(ctx, "p2")
	require.NoError(t, err)
	require.Equal(t, types.AgentPromptSuspended, row.Status)

	delete(flaky.failFor, "m2")
	n, err = b.SupersedeSession(ctx, "s1", hooks.hooks())
	require.NoError(t, err)
	require.Equal(t, 1, n)
	row, err = b.Store().Get(ctx, "p2")
	require.NoError(t, err)
	require.Equal(t, types.AgentPromptSuperseded, row.Status)
	require.Equal(t, map[string]string{"p2": ReasonPromptSupersede}, hooks.closed)
}
