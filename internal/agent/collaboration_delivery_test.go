package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/collaboration"
	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func collaborationAnswerModel(answer string) *mockChat {
	return &mockChat{responses: []mockResponse{{chunks: []types.StreamResponse{{
		ResponseType: types.ResponseTypeAnswer, Content: answer, Done: true, FinishReason: "stop",
	}}}}}
}

func collaborationTranscript(messages []chat.Message) string {
	var text strings.Builder
	for _, message := range messages {
		text.WriteString(message.Content)
		text.WriteByte('\n')
	}
	return text.String()
}

// Exercise a real saved wait, then start another scheduler against the same
// database. The first child's answer must survive even though its cursor was
// acknowledged before the second child completed.
func TestCollaborationRestartRetainsConsumedResults(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "current"
		if legacy {
			name = "legacy"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			r, b := engineCollaboration(t)
			owned, err := r.AcquireParent(ctx, b.Scope)
			require.NoError(t, err)
			_, err = r.Spawn(ctx, b.Scope, "spawn", "second", "a", "{}", "task")
			require.NoError(t, err)
			require.NoError(t, r.Store.Change(ctx, b.Scope, func(g *collaboration.Group) error {
				g.NextSequence = 1
				g.Events = []collaboration.Event{{
					Sequence: 1, From: "first", RunID: "first-run", Kind: "succeeded",
					Content: `{"result":{"answer":"FIRST_COMPLETE_RESULT [source](https://example.com/first)"}}`,
				}}
				return nil
			}))
			engine := newTestEngine(t, collaborationAnswerModel("WAITING_FOR_SECOND"))
			engine.toolRegistry = agenttools.NewToolRegistry()
			engine.SetCollaboration(owned)
			engine.EnableSuspension(json.RawMessage(`{}`))
			runCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			finished := make(chan struct{})
			go func() { defer close(finished); _, _ = engine.Execute(runCtx, "s", "m", "combine results", nil) }()
			require.Eventually(t, func() bool {
				g, readErr := r.Store.Read(ctx, b.Scope)
				return readErr == nil && g.ParentWait != nil
			}, 3*time.Second, time.Millisecond)
			cancel()
			select {
			case <-finished:
			case <-time.After(3 * time.Second):
				t.Fatal("parent failed to stop")
			}
			require.NoError(t, r.Store.Change(ctx, b.Scope, func(g *collaboration.Group) error {
				require.Equal(t, int64(1), g.ParentWait.After)
				snap, decodeErr := DecodeRunSnapshot(g.ParentWait.Snapshot)
				require.NoError(t, decodeErr)
				require.Len(t, snap.CollaborationDeliveries, 1)
				require.Equal(t, int64(1), *snap.CollaborationCursor)
				if legacy {
					snap.CollaborationDeliveries, snap.CollaborationCursor = nil, nil
					g.ParentWait.Snapshot, decodeErr = json.Marshal(snap)
					require.NoError(t, decodeErr)
				}
				g.ParentLease = time.Time{}
				g.Members[0].Runs[0].State = "succeeded"
				g.NextSequence = 2
				g.Events = append(g.Events, collaboration.Event{
					Sequence: 2, From: "second", RunID: "second-run", Kind: "succeeded",
					Content: `{"result":{"answer":"SECOND_COMPLETE_RESULT"}}`,
				})
				return nil
			}))
			model := collaborationAnswerModel("combined")
			restarted := collaboration.New(r.Store, nil)
			resumed := make(chan error, 1)
			restarted.WakeParent = func(ctx context.Context, binding *collaboration.Binding, data []byte) error {
				next := newTestEngine(t, model)
				next.toolRegistry = agenttools.NewToolRegistry()
				next.SetCollaboration(binding)
				_, resumeErr := next.Resume(ctx, "s", "m", nil, data, nil)
				resumed <- resumeErr
				return resumeErr
			}
			restarted.Tick(ctx)
			select {
			case err = <-resumed:
				require.NoError(t, err)
			case <-time.After(3 * time.Second):
				t.Fatal("parent did not recover")
			}
			require.Len(t, model.calls, 1)
			input := collaborationTranscript(model.calls[0])
			require.Equal(t, 1, strings.Count(input, "FIRST_COMPLETE_RESULT"))
			require.Equal(t, 1, strings.Count(input, "SECOND_COMPLETE_RESULT"))
			require.Contains(t, input, "https://example.com/first")
			require.Contains(t, input, "Run ID: first-run")
			if !legacy {
				require.Less(t,
					strings.Index(input, "FIRST_COMPLETE_RESULT"), strings.Index(input, "WAITING_FOR_SECOND"))
			}
		})
	}
}

func TestCollaborationPromptSnapshotPreservesDeliveryOrder(t *testing.T) {
	r, b := engineCollaboration(t)
	owned, err := r.AcquireParent(t.Context(), b.Scope)
	require.NoError(t, err)
	first := collaboration.Event{Sequence: 1, From: "first", RunID: "run1", Kind: "message", Content: "FIRST_PROGRESS"}
	second := collaboration.Event{
		Sequence: 2, From: "first", RunID: "run1", Kind: "succeeded",
		Content: `{"result":{"answer":"FINAL_RESULT"}}`,
	}
	require.NoError(t, r.Store.Change(t.Context(), b.Scope, func(g *collaboration.Group) error {
		g.NextSequence, g.Events = 2, []collaboration.Event{first, second}
		return nil
	}))
	initial := newTestEngine(t, nil)
	initial.toolRegistry = agenttools.NewToolRegistry()
	initial.SetCollaboration(owned)
	state := &types.AgentState{}
	initial.beginTurn("task", nil, state)
	var input []chat.Message
	initial.deliverCollaborationEvents(&input, []collaboration.Event{first}, 0, false)
	state.RoundSteps = append(state.RoundSteps, types.AgentStep{Thought: "BETWEEN_MESSAGES"})
	initial.deliverCollaborationEvents(&input, []collaboration.Event{second}, 1, true)
	owned.Seen = 2
	initial.recordSteerMessage("steer", "NEW_USER_GUIDANCE")
	initial.beginRound(types.AgentStep{Thought: "APPROVAL_ROUND", UserMessagesBefore: []string{"steer"}},
		[]types.LLMToolCall{{ID: "approved", Function: types.FunctionCall{Name: "approved_action", Arguments: `{}`}}})
	snap, err := initial.snapshot(0)
	require.NoError(t, err)
	data, err := json.Marshal(snap)
	require.NoError(t, err)
	model := collaborationAnswerModel("done")
	next := newTestEngine(t, model)
	next.toolRegistry = agenttools.NewToolRegistry()
	action := newCountingTool("approved_action")
	next.toolRegistry.RegisterTool(action)
	// Approval restoration creates a new binding whose cursor starts at zero.
	next.SetCollaboration(&collaboration.Binding{Runtime: r, Scope: b.Scope, ParentOwner: owned.ParentOwner})
	_, err = next.Resume(t.Context(), "s", "m", nil, data, &types.AgentPrompt{ID: "approval", ToolCallID: "approved"})
	require.NoError(t, err)
	require.Equal(t, 1, action.calls)
	restored := collaborationTranscript(model.calls[0])
	require.Equal(t, 1, strings.Count(restored, "FIRST_PROGRESS"))
	require.Equal(t, 1, strings.Count(restored, "FINAL_RESULT"))
	require.Less(t, strings.Index(restored, "FIRST_PROGRESS"), strings.Index(restored, "BETWEEN_MESSAGES"))
	require.Less(t, strings.Index(restored, "BETWEEN_MESSAGES"), strings.Index(restored, "FINAL_RESULT"))
	require.Less(t, strings.Index(restored, "FINAL_RESULT"), strings.Index(restored, "NEW_USER_GUIDANCE"))
	require.Less(t, strings.Index(restored, "NEW_USER_GUIDANCE"), strings.Index(restored, "APPROVAL_ROUND"))
}
