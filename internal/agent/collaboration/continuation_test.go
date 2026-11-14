package collaboration

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNewParentTurnContinuesSameChildSessionWithoutReplayingOldWork(t *testing.T) {
	store, scope := testStore(t)
	ctx := context.Background()
	r := New(store, nil)
	b, err := r.AcquireParent(ctx, scope)
	require.NoError(t, err)
	id, err := r.Spawn(b.Context(ctx), scope, "spawn", "novelist", "template", "{}", "plan")
	require.NoError(t, err)
	var originalSession, originalRun string
	require.NoError(t, store.Change(b.Context(ctx), scope, func(g *Group) error {
		m := g.Members[0]
		originalSession, originalRun = m.SessionID, current(m).ID
		current(m).State = "succeeded"
		current(m).Result = Result{Answer: "choose A or B", MessageID: "first-answer"}
		m.Mail = []Event{{Sequence: 42, Kind: "followup", Content: "old unread mail"}}
		g.Requests["old-request"] = originalRun
		publish(g, m, current(m))
		return nil
	}))
	g := read(t, store, scope)
	finished, err := r.Finish(b.Context(ctx), scope, g.NextSequence)
	require.NoError(t, err)
	require.True(t, finished)
	newScope := scope
	newScope.ID = "next-human-answer"
	// A different runtime instance simulates a server restart between turns.
	r = New(NewStore(store.db), nil)
	next, err := r.AcquireParent(ctx, newScope)
	require.NoError(t, err)
	require.Equal(t, newScope, next.Scope)
	listed, err := next.List(ctx, "")
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.True(t, listed[0].PreviousTurn)
	require.Equal(t, originalSession, listed[0].SessionID)
	g = read(t, store, newScope)
	require.Empty(t, g.StatusView().Tasks, "old tasks must not appear as work in the new timeline")
	require.Empty(t, g.Events)
	require.Empty(t, g.Requests)
	require.Empty(t, g.Members[0].Mail)

	run, err := r.Send(next.Context(ctx), newScope, "continue", id, "A; write chapter 1", originalRun, true)
	require.NoError(t, err)
	require.NotEqual(t, originalRun, run)
	g = read(t, store, newScope)
	require.Len(t, g.StatusView().Tasks, 1)
	require.Equal(t, originalSession, g.Members[0].SessionID)
	require.Len(t, g.Members[0].Runs, 2)
	// Retry cannot schedule the second chapter twice.
	again, err := r.Send(next.Context(ctx), newScope, "continue", id, "A; write chapter 1", originalRun, true)
	require.NoError(t, err)
	require.Equal(t, run, again)
	var executions int
	r.Run = func(_ context.Context, w Work) (Result, error) {
		executions++
		require.Equal(t, originalSession, w.Member.SessionID)
		require.Equal(t, "A; write chapter 1", w.Run.Query)
		return Result{Answer: "chapter 1"}, nil
	}
	r.Tick(ctx)
	r.workers.Wait()
	require.Equal(t, 1, executions)
	require.Equal(t, "completed", read(t, store, scope).Status)
	require.Len(t, read(t, store, scope).Members[0].Runs, 1, "old group is immutable")
	// Old stop and stale completion cannot touch the new turn.
	require.NoError(t, r.CancelMessage(ctx, scope, scope.ID))
	require.ErrorIs(t, r.CheckParent(ctx, scope, b.ParentOwner), ErrConflict)
	require.Equal(t, "active", read(t, store, newScope).Status)
}

func TestNewParentDoesNotInheritCancelledOrOtherOwnersChildren(t *testing.T) {
	for _, barrier := range []string{"cancelled", "other-user", "other-session", "other-tenant"} {
		t.Run(barrier, func(t *testing.T) {
			store, scope := testStore(t)
			r := New(store, nil)
			_, err := r.Spawn(t.Context(), scope, "spawn", "old", "a", "{}", "q")
			require.NoError(t, err)
			require.NoError(t, store.Change(t.Context(), scope, func(g *Group) error {
				g.Status = "completed"
				current(g.Members[0]).State = "succeeded"
				return nil
			}))
			next := scope
			next.ID = "next"
			switch barrier {
			case "other-user":
				next.UserID = "someone-else"
			case "other-session":
				next.SessionID = "different-session"
			case "other-tenant":
				next.TenantID++
			case "cancelled":
				cancelled := scope
				cancelled.ID = "cancelled-later"
				require.NoError(t, store.Ensure(t.Context(), cancelled))
				require.NoError(t, r.Cancel(t.Context(), cancelled))
				// Establish deterministic order without relying on clock granularity.
				require.NoError(t, store.db.Model(&Record{}).Where("id = ?", scope.ID).
					Update("updated_at", time.Now().UTC().Add(-time.Minute)).Error)
			}
			b, err := r.AcquireParent(t.Context(), next)
			require.NoError(t, err)
			list, err := b.List(t.Context(), "")
			require.NoError(t, err)
			require.Empty(t, list)
		})
	}
}

func TestConcurrentParentTurnsCannotCloneOneChildIntoTwoActiveGroups(t *testing.T) {
	store, scope := testStore(t)
	r := New(store, nil)
	_, err := r.Spawn(t.Context(), scope, "spawn", "novelist", "a", "{}", "plan")
	require.NoError(t, err)
	require.NoError(t, store.Change(t.Context(), scope, func(g *Group) error {
		g.Status, current(g.Members[0]).State = "completed", "succeeded"
		return nil
	}))
	var wg sync.WaitGroup
	winners := make(chan *Binding, 12)
	for i := range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			next := scope
			next.ID = fmt.Sprintf("human-%d", i)
			if b, err := New(NewStore(store.db), nil).AcquireParent(t.Context(), next); err == nil {
				winners <- b
			}
		}()
	}
	wg.Wait()
	close(winners)
	var acquired []*Binding
	for b := range winners {
		acquired = append(acquired, b)
	}
	require.Len(t, acquired, 1)
	var activeGroups int64
	require.NoError(t, store.db.Model(&Record{}).Where("status = ?", "active").Count(&activeGroups).Error)
	require.EqualValues(t, 1, activeGroups)
	require.Equal(t, acquired[0].Scope.ID, read(t, store, scope).SuccessorID)
}

func TestParentContinuationRecoversClaimBeforeSuccessorWasCreated(t *testing.T) {
	store, scope := testStore(t)
	r := New(store, nil)
	_, err := r.Spawn(t.Context(), scope, "spawn", "novelist", "a", "{}", "plan")
	require.NoError(t, err)
	require.NoError(t, store.Change(t.Context(), scope, func(g *Group) error {
		g.Status, g.SuccessorID = "completed", "claimed-before-crash"
		current(g.Members[0]).State = "succeeded"
		return nil
	}))
	next := scope
	next.ID = "human-after-crash"
	b, err := New(NewStore(store.db), nil).AcquireParent(t.Context(), next)
	require.NoError(t, err)
	require.Equal(t, "claimed-before-crash", b.Scope.ID)
	g := read(t, store, b.Scope)
	require.Equal(t, "human-after-crash", g.ParentMessageID)
	require.Len(t, g.Members, 1)
	require.Empty(t, g.Events)
}
