package collaboration

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStatusTracksCommittedChangesWithoutLeakingPrivateState(t *testing.T) {
	s, scope := testStore(t)
	ctx := context.Background()
	r := New(s, nil)
	var snapshots []StatusSnapshot
	s.OnStatus = func(ctx context.Context, g *Group) {
		persisted, err := s.Read(ctx, g.Scope)
		require.NoError(t, err)
		require.Equal(t, g.Revision, persisted.Revision)
		snapshots = append(snapshots, g.StatusView())
	}
	_, err := r.Spawn(ctx, scope, "spawn", "worker", "agent", "PRIVATE_CONFIG", "PRIVATE_QUERY")
	require.NoError(t, err)
	require.Len(t, snapshots, 1)
	require.Equal(t, "queued", snapshots[0].Tasks[0].Status)
	var w Work
	require.NoError(t, s.Change(ctx, scope, func(g *Group) error {
		m := g.Members[0]
		v := current(m)
		v.State, v.Owner, v.Lease = "running", "PRIVATE_OWNER", time.Now().Add(time.Minute)
		w = Work{scope, *m, *v}
		return nil
	}))
	n := len(snapshots)
	require.NoError(
		t,
		s.Change(
			ctx,
			scope,
			func(g *Group) error { current(g.Members[0]).Lease = time.Now().Add(time.Hour); return nil },
		),
	)
	require.Len(t, snapshots, n, "leases must not spam UI snapshots")
	require.NoError(t, r.RecordProgress(ctx, w, Progress{Phase: "tool_running", ToolName: "search"}))
	child := &Binding{Runtime: r, Scope: scope, Child: &w}
	require.NoError(t, child.MessageParent(ctx, "finding", "Found the relevant file"))
	require.Equal(t, "Found the relevant file", snapshots[len(snapshots)-1].Tasks[0].LatestMessage)
	require.Equal(t, "search", snapshots[len(snapshots)-1].Tasks[0].Progress.ToolName)
	require.NoError(t, r.Cancel(ctx, scope))
	require.Equal(t, "cancelling", snapshots[len(snapshots)-1].Tasks[0].Status)
	n = len(snapshots)
	require.ErrorIs(t, r.RecordProgress(ctx, w, Progress{Phase: "tool_succeeded"}), ErrConflict)
	require.ErrorIs(t, child.MessageParent(ctx, "late", "late"), ErrClosed)
	require.Len(t, snapshots, n)
	for i := 1; i < len(snapshots); i++ {
		require.Greater(t, snapshots[i].Revision, snapshots[i-1].Revision)
	}
	b, err := json.Marshal(snapshots)
	require.NoError(t, err)
	for _, secret := range []string{
		"PRIVATE_CONFIG",
		"PRIVATE_QUERY",
		"PRIVATE_OWNER",
		"lease_until",
		"config_snapshot",
	} {
		require.NotContains(t, string(b), secret)
	}
	restarted := NewStore(s.db)
	g, err := restarted.Read(ctx, scope)
	require.NoError(t, err)
	require.Equal(t, snapshots[len(snapshots)-1], g.StatusView(), "replay uses the durable CAS revision")
}

func TestPromptRecoveryPublishesOnlyActualTransitions(t *testing.T) {
	r, w := promptWork(t)
	ctx := context.Background()
	require.NoError(t, r.Store.Change(ctx, w.Scope, func(g *Group) error {
		v := current(g.Members[0])
		v.State, v.Lease = "running", time.Time{}
		return nil
	}))
	require.NoError(t, r.ReconcilePrompt(ctx, w, PromptState{ID: w.Run.PromptID, Status: "pending"}))
	require.NoError(t, r.ReconcilePrompt(ctx, w, PromptState{ID: w.Run.PromptID, Status: "pending"}))
	b := &Binding{Runtime: r, Scope: w.Scope}
	events, err := b.Poll(ctx)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "waiting", events[0].Kind)
	updated, err := b.Wait(ctx, b.Seen, 0)
	require.NoError(t, err)
	require.False(t, updated)
	require.NoError(t, r.ReconcilePrompt(ctx, w, PromptState{ID: w.Run.PromptID, Status: "answered"}))
	require.NoError(t, r.ReconcilePrompt(ctx, w, PromptState{ID: w.Run.PromptID, Status: "answered"}))
	events, err = b.Poll(ctx)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "resumed", events[0].Kind)
}

func TestPromptNoticesSurviveRestartAndKeepDecisionSeparateFromExecution(t *testing.T) {
	for _, decision := range []string{"approved", "rejected", ""} {
		t.Run("decision="+decision, func(t *testing.T) {
			r, w := promptWork(t)
			ctx := t.Context()
			require.NoError(t, r.Store.Change(ctx, w.Scope, func(g *Group) error {
				v := current(g.Members[0])
				v.State = "running"
				v.Lease = time.Now().Add(time.Minute)
				v.Owner = "worker"
				w.Run = *v
				return nil
			}))
			p := PromptState{
				ID:         w.Run.PromptID,
				Kind:       "tool_approval",
				Status:     "pending",
				Service:    "KM",
				Tool:       "hot-articles",
				ToolCallID: "call",
			}
			require.NoError(t, r.AwaitPrompt(ctx, w, p))
			// A new scheduler reads only durable data, after the old execution lease expired.
			require.NoError(
				t,
				r.Store.Change(
					ctx,
					w.Scope,
					func(g *Group) error { current(g.Members[0]).Lease = time.Time{}; return nil },
				),
			)
			r = New(NewStore(r.Store.db), nil)
			require.NoError(t, r.ReconcilePrompt(ctx, w, p))
			require.NoError(t, r.ReconcilePrompt(ctx, w, p))
			p.Status, p.Decision = "answered", decision
			require.NoError(t, r.ReconcilePrompt(ctx, w, p))
			require.NoError(t, r.ReconcilePrompt(ctx, w, p))
			g := read(t, r.Store, w.Scope)
			require.Len(t, g.Events, 2, "retries must not duplicate notices")
			for i, e := range g.Events {
				var notice RuntimeNotice
				require.NoError(t, json.Unmarshal([]byte(e.Content), &notice))
				require.Equal(t, p.ID, notice.PromptID)
				require.Equal(t, p.ToolCallID, notice.ToolCallID)
				require.Equal(t, "KM", notice.Service)
				require.Equal(t, "hot-articles", notice.Tool)
				require.Equal(t, w.Run.ID, notice.RunID)
				require.Equal(t, e.Sequence, notice.Sequence)
				if i == 0 {
					require.Equal(t, "approval_required", notice.Type)
					require.Equal(t, "waiting", notice.ExecutionState)
					require.Empty(t, notice.Decision)
				} else {
					require.Equal(t, "approval_resolved", notice.Type)
					require.Equal(t, "queued", notice.ExecutionState)
					require.Equal(t, decision, notice.Decision)
				}
			}
		})
	}
}

func TestLateApprovalNoticeCannotWakeCanceledOrDifferentPrompt(t *testing.T) {
	for _, cancel := range []bool{true, false} {
		r, w := promptWork(t)
		if cancel {
			require.NoError(t, r.Cancel(t.Context(), w.Scope))
		}
		before := read(t, r.Store, w.Scope)
		p := PromptState{ID: w.Run.PromptID, Kind: "tool_approval", Status: "answered", Decision: "approved"}
		if !cancel {
			p.ID = "different-prompt"
		}
		require.NoError(t, r.ReconcilePrompt(t.Context(), w, p))
		after := read(t, r.Store, w.Scope)
		require.NotEqual(t, "queued", current(after.Members[0]).State)
		for _, e := range after.Events[len(before.Events):] {
			require.NotEqual(t, "resumed", e.Kind)
		}
	}
}
