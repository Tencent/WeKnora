package hitl

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// testKind stands in for any prompt kind: the broker must not care what the
// request and response mean.
const testKind = "test_question"

func validateTestAnswer(_, response json.RawMessage) error {
	var r struct {
		Answer string `json:"answer"`
	}
	if err := json.Unmarshal(response, &r); err != nil {
		return err
	}
	if r.Answer == "" {
		return errors.New("answer is required")
	}
	return nil
}

func newTestBroker(store Store) *Broker {
	b := NewBroker(store, nil, WithPollInterval(20*time.Millisecond))
	b.RegisterKind(testKind, validateTestAnswer)
	return b
}

func testSpec(timeout time.Duration, emit func(ctx context.Context, p *types.AgentPrompt) error) Spec {
	return Spec{
		Prompt: &types.AgentPrompt{
			TenantID: 1, UserID: "alice", SessionID: "s1", AssistantMessageID: "m1",
			Kind: testKind, Request: types.JSON(`{"question":"which?"}`),
		},
		Timeout:       timeout,
		TimeoutReason: "no answer",
		Emit:          emit,
	}
}

func answer(promptID, text string) ResolveRequest {
	return ResolveRequest{
		TenantID: 1, UserID: "alice", SessionID: "s1", Kind: testKind, PromptID: promptID,
		Response: json.RawMessage(`{"answer":"` + text + `"}`),
	}
}

func TestBroker_AnswerOnOtherReplicaReachesOwnerThroughStore(t *testing.T) {
	store := NewMemoryStore()
	owner, other := newTestBroker(store), newTestBroker(store)

	out, err := owner.Await(context.Background(), testSpec(5*time.Second,
		func(_ context.Context, p *types.AgentPrompt) error {
			go func() { require.NoError(t, resolveErr(other, context.Background(), answer(p.ID, "blue"))) }()
			return nil
		}))
	require.NoError(t, err)
	require.True(t, out.Answered)
	require.JSONEq(t, `{"answer":"blue"}`, string(out.Prompt.Response))
	require.Equal(t, "alice", out.Prompt.ResolvedBy)

	owner.Consume(context.Background(), out.Prompt.ID, types.AgentPromptDone)
	row, err := store.Get(context.Background(), out.Prompt.ID)
	require.NoError(t, err)
	require.Equal(t, types.AgentPromptDone, row.Status)
}

func TestBroker_ResolveChecksScopeOwnershipAndAnswer(t *testing.T) {
	b := newTestBroker(NewMemoryStore())
	out, err := b.Await(context.Background(), testSpec(5*time.Second,
		func(_ context.Context, p *types.AgentPrompt) error {
			go func() {
				ctx := context.Background()
				otherSession := answer(p.ID, "x")
				otherSession.SessionID = "s2"
				require.ErrorIs(t, resolveErr(b, ctx, otherSession), ErrPromptNotFound)

				otherKind := answer(p.ID, "x")
				otherKind.Kind = "something_else"
				require.ErrorIs(t, resolveErr(b, ctx, otherKind), ErrPromptNotFound)

				otherTenant := answer(p.ID, "x")
				otherTenant.TenantID = 2
				require.ErrorIs(t, resolveErr(b, ctx, otherTenant), ErrTenantMismatch)

				anonymous := answer(p.ID, "x")
				anonymous.UserID = ""
				require.ErrorIs(t, resolveErr(b, ctx, anonymous), ErrUserMismatch)

				require.ErrorIs(t, resolveErr(b, ctx, answer(p.ID, "")), ErrInvalidResponse)

				require.NoError(t, resolveErr(b, ctx, answer(p.ID, "red")))
				require.ErrorIs(t, resolveErr(b, ctx, answer(p.ID, "green")), ErrAlreadyResolved)
			}()
			return nil
		}))
	require.NoError(t, err)
	require.True(t, out.Answered)
	require.JSONEq(t, `{"answer":"red"}`, string(out.Prompt.Response))
}

func TestBroker_UnregisteredKindIsRejected(t *testing.T) {
	b := NewBroker(nil, nil, WithPollInterval(20*time.Millisecond))
	out, err := b.Await(context.Background(), testSpec(200*time.Millisecond,
		func(_ context.Context, p *types.AgentPrompt) error {
			require.ErrorIs(t, resolveErr(b, context.Background(), answer(p.ID, "x")), ErrInvalidResponse)
			return nil
		}))
	require.NoError(t, err)
	require.True(t, out.TimedOut)
}

// An answer recorded without a wake-up must win over the owner's timeout.
func TestBroker_AnswerRecordedBeforeTimeoutWins(t *testing.T) {
	store := NewMemoryStore()
	b := NewBroker(store, nil, WithPollInterval(time.Hour))
	out, err := b.Await(context.Background(), testSpec(300*time.Millisecond,
		func(_ context.Context, p *types.AgentPrompt) error {
			_, err := store.Transition(context.Background(), p.ID, types.AgentPromptTransition{
				From: []string{types.AgentPromptPending}, To: types.AgentPromptAnswered,
				Response: types.JSON(`{"answer":"late"}`),
			})
			return err
		}))
	require.NoError(t, err)
	require.True(t, out.Answered)
	require.False(t, out.TimedOut)
}

func TestBroker_TimeoutAndCancelAreRecorded(t *testing.T) {
	b := newTestBroker(NewMemoryStore())

	var timedOutID string
	out, err := b.Await(context.Background(), testSpec(100*time.Millisecond,
		func(_ context.Context, p *types.AgentPrompt) error { timedOutID = p.ID; return nil }))
	require.NoError(t, err)
	require.True(t, out.TimedOut)
	require.Equal(t, "no answer", out.Reason)
	require.ErrorIs(t, resolveErr(b, context.Background(), answer(timedOutID, "x")), ErrAlreadyResolved)

	ctx, cancel := context.WithCancel(context.Background())
	out, err = b.Await(ctx, testSpec(time.Minute,
		func(context.Context, *types.AgentPrompt) error { cancel(); return nil }))
	require.NoError(t, err)
	require.True(t, out.Canceled)
	row, err := b.Store().Get(context.Background(), out.Prompt.ID)
	require.NoError(t, err)
	require.Equal(t, types.AgentPromptCanceled, row.Status)
}

func TestBroker_ExecutingUntilMarkedExecuted(t *testing.T) {
	b := newTestBroker(NewMemoryStore())
	out, err := b.Await(context.Background(), testSpec(5*time.Second,
		func(_ context.Context, p *types.AgentPrompt) error {
			go func() { require.NoError(t, resolveErr(b, context.Background(), answer(p.ID, "go"))) }()
			return nil
		}))
	require.NoError(t, err)

	b.Consume(context.Background(), out.Prompt.ID, types.AgentPromptExecuting)
	row, _ := b.Store().Get(context.Background(), out.Prompt.ID)
	require.Equal(t, types.AgentPromptExecuting, row.Status)

	b.MarkExecuted(context.Background(), out.Prompt.ID)
	row, _ = b.Store().Get(context.Background(), out.Prompt.ID)
	require.Equal(t, types.AgentPromptDone, row.Status)
}

func resolveErr(b *Broker, ctx context.Context, req ResolveRequest) error {
	_, err := b.Resolve(ctx, req)
	return err
}
