package hitl

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestDelegatedPromptPersistsBeforeEmissionAndSuspendsImmediately(t *testing.T) {
	b := NewBroker(nil, nil)
	registered := false
	ctx := WithDelegation(
		context.Background(),
		Delegation{
			Check: func(context.Context) error { return nil },
			Register: func(ctx context.Context, p *types.AgentPrompt) error {
				snapshot, err := b.Suspensions().Get(ctx, p.AssistantMessageID)
				require.NoError(t, err)
				require.Equal(t, p.ID, snapshot.PromptID)
				registered = true
				return nil
			},
		},
	)
	start := time.Now()
	out, err := b.Await(ctx, Spec{
		Prompt: &types.AgentPrompt{ID: "p", AssistantMessageID: "m"}, Timeout: time.Hour,
		Suspend: &SuspendSpec{
			Snapshot:        []byte(`{}`),
			SnapshotVersion: 1,
		}, Emit: func(ctx context.Context, p *types.AgentPrompt) error {
			require.True(t, registered)
			row, err := b.Store().Get(ctx, p.ID)
			require.NoError(t, err)
			require.NotNil(t, row)
			return nil
		},
	})
	require.NoError(t, err)
	require.True(t, out.Suspended)
	require.Less(t, time.Since(start), time.Second)
	snapshot, err := b.Suspensions().Get(ctx, "m")
	require.NoError(t, err)
	require.NotNil(t, snapshot)
}

func TestCanceledDelegationCannotConsumeLateApproval(t *testing.T) {
	b := NewBroker(nil, nil)
	require.NoError(
		t,
		b.Store().
			Create(context.Background(),
				&types.AgentPrompt{
					ID:            "p",
					Status:        types.AgentPromptAnswered,
					OwnerInstance: "owner",
				}),
	)
	ctx := WithDelegation(
		context.Background(),
		Delegation{Check: func(context.Context) error { return errors.New("task canceled") }},
	)
	ok, err := b.Consume(ctx, "p", types.AgentPromptExecuting, "owner")
	require.False(t, ok)
	require.ErrorContains(t, err, "task canceled")
	p, err := b.Store().Get(context.Background(), "p")
	require.NoError(t, err)
	require.Equal(t, types.AgentPromptAnswered, p.Status)
}

func TestDelegatedPromptCannotFallBackToUnsafeHotWait(t *testing.T) {
	b := NewBroker(nil, nil, WithSuspensionStore(failingSuspensionStore{NewMemorySuspensionStore()}))
	emitted := false
	ctx := WithDelegation(context.Background(), Delegation{})
	_, err := b.Await(
		ctx,
		Spec{
			Prompt:          &types.AgentPrompt{ID: "p", AssistantMessageID: "m"},
			Timeout:         time.Hour,
			FallbackTimeout: time.Hour,
			Suspend: &SuspendSpec{
				Snapshot:        []byte(`{}`),
				SnapshotVersion: 1,
			},
			Emit: func(context.Context, *types.AgentPrompt) error { emitted = true; return nil },
		},
	)
	require.Error(t, err)
	require.False(t, emitted)
}
