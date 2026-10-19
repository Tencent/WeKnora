package collaboration

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParentWaitRestartOneWinnerAndNoLostWake(t *testing.T) {
	store, scope := testStore(t)
	ctx := context.Background()
	first := New(store, nil)
	b, err := first.AcquireParent(ctx, scope)
	require.NoError(t, err)
	until := time.Now().Add(time.Minute)
	require.NoError(t, b.SaveWait(ctx, []byte(`{"safe":true}`), until))
	// Result commits before the new scheduler starts; the DB is the wake outbox.
	require.NoError(t, store.Change(ctx, scope, func(g *Group) error {
		g.Events = append(g.Events, Event{Sequence: 1, Kind: "succeeded"})
		g.NextSequence = 1
		g.ParentLease = time.Now().Add(-time.Second)
		return nil
	}))
	var calls atomic.Int32
	entered := make(chan *Binding, 2)
	release := make(chan struct{})
	wake := func(ctx context.Context, b *Binding, data []byte) error {
		calls.Add(1)
		entered <- b
		if string(data) != `{"safe":true}` {
			return ErrConflict
		}
		<-release
		return b.ClearWait(ctx)
	}
	a, c := New(NewStore(store.db), nil), New(NewStore(store.db), nil)
	a.WakeParent, c.WakeParent = wake, wake
	var wg sync.WaitGroup
	for range 16 {
		for _, r := range []*Runtime{a, c} {
			wg.Add(1)
			go func() { defer wg.Done(); r.Tick(ctx) }()
		}
	}
	wg.Wait()
	var resumed *Binding
	select {
	case resumed = <-entered:
	case <-time.After(time.Second):
		t.Fatal("not woken")
	}
	require.Equal(t, int32(1), calls.Load())
	require.Equal(t, until.UnixNano(), resumed.WaitUntil.UnixNano())
	require.ErrorIs(t, first.CheckParent(ctx, scope, b.ParentOwner), ErrConflict)
	close(release)
	a.workers.Wait()
	c.workers.Wait()
	require.Nil(t, read(t, store, scope).ParentWait)
}

func TestCanceledParentCannotBeWokenByLateResult(t *testing.T) {
	store, scope := testStore(t)
	r := New(store, nil)
	ctx := context.Background()
	b, err := r.AcquireParent(ctx, scope)
	require.NoError(t, err)
	require.NoError(t, b.SaveWait(ctx, []byte("checkpoint"), time.Now().Add(-time.Second)))
	require.NoError(t, r.Cancel(ctx, scope))
	require.NoError(t, store.Change(ctx, scope, func(g *Group) error {
		g.ParentLease = time.Time{}
		g.NextSequence++
		g.Events = append(g.Events, Event{Sequence: g.NextSequence})
		return nil
	}))
	var calls atomic.Int32
	r.WakeParent = func(context.Context, *Binding, []byte) error { calls.Add(1); return nil }
	r.Tick(ctx)
	r.workers.Wait()
	require.Zero(t, calls.Load())
	require.Nil(t, read(t, store, scope).ParentWait)
	require.Error(t, b.SaveWait(ctx, []byte("stale"), time.Now()))
}

func TestCancelAfterWakeClaimFencesContinuation(t *testing.T) {
	store, scope := testStore(t)
	r := New(store, nil)
	ctx := context.Background()
	b, err := r.AcquireParent(ctx, scope)
	require.NoError(t, err)
	require.NoError(t, b.SaveWait(ctx, []byte("safe"), time.Now().Add(-time.Second)))
	require.NoError(t, store.Change(ctx, scope, func(g *Group) error { g.ParentLease = time.Time{}; return nil }))
	claimed := make(chan *Binding, 1)
	resume := make(chan struct{})
	result := make(chan error, 1)
	r.WakeParent = func(ctx context.Context, b *Binding, _ []byte) error {
		claimed <- b
		<-resume
		err := b.Runtime.CheckParent(ctx, b.Scope, b.ParentOwner)
		result <- err
		return err
	}
	r.Tick(ctx)
	select {
	case <-claimed:
	case <-time.After(time.Second):
		t.Fatal("not claimed")
	}
	require.NoError(t, r.Cancel(ctx, scope))
	close(resume)
	require.Error(t, <-result)
	r.workers.Wait()
	require.Equal(t, "cancelled", read(t, store, scope).Status)
}

func promptWork(t *testing.T) (*Runtime, Work) {
	t.Helper()
	s, scope := testStore(t)
	r := New(s, nil)
	ctx := context.Background()
	_, err := r.Spawn(ctx, scope, "spawn", "worker", "agent", "{}", "task")
	require.NoError(t, err)
	require.NoError(t, s.Change(ctx, scope, func(g *Group) error {
		v := current(g.Members[0])
		v.State = "waiting"
		v.PromptID = "prompt"
		return nil
	}))
	g := read(t, s, scope)
	return r, Work{Scope: scope, Member: *g.Members[0], Run: *current(g.Members[0])}
}

func TestPromptRestartAndCancellationFences(t *testing.T) {
	for _, status := range []string{"pending", "suspended", "answered", "executing", "done", "canceled"} {
		t.Run(status, func(t *testing.T) {
			r, w := promptWork(t)
			ctx := context.Background()
			require.NoError(t, r.ReconcilePrompt(ctx, w, PromptState{ID: w.Run.PromptID, Status: status}))
			want := "needs_attention"
			if status == "pending" || status == "suspended" {
				want = "waiting"
			}
			if status == "answered" {
				want = "queued"
			}
			if status == "canceled" {
				want = "interrupted"
			}
			require.Equal(t, want, current(read(t, r.Store, w.Scope).Members[0]).State)
		})
	}
	for _, interrupt := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "interrupt"}[interrupt], func(t *testing.T) {
			r, w := promptWork(t)
			ctx := context.Background()
			if interrupt {
				require.NoError(t, r.Interrupt(ctx, w.Scope, w.Member.ID, w.Run.ID))
			} else {
				require.NoError(t, r.Cancel(ctx, w.Scope))
			}
			require.NoError(t, r.ReconcilePrompt(ctx, w, PromptState{ID: w.Run.PromptID, Status: "answered"}))
			require.NotEqual(t, "queued", current(read(t, r.Store, w.Scope).Members[0]).State)
			var calls atomic.Int32
			r.Run = func(context.Context, Work) (Result, error) { calls.Add(1); return Result{}, nil }
			r.Tick(ctx)
			r.workers.Wait()
			require.Zero(t, calls.Load())
		})
	}
}

func TestExpiredWaitingPromptCanBeContinued(t *testing.T) {
	for _, status := range []string{"expired", "canceled", "superseded"} {
		t.Run(status, func(t *testing.T) {
			r, w := promptWork(t)
			ctx := context.Background()
			require.NoError(t, r.ReconcilePrompt(ctx, w, PromptState{ID: w.Run.PromptID, Status: status}))
			g := read(t, r.Store, w.Scope)
			require.Equal(t, "interrupted", current(g.Members[0]).State)
			require.Empty(t, current(g.Members[0]).PromptID)
			_, err := r.Send(ctx, w.Scope, "follow-"+status, w.Member.Name, "continue", "", true)
			require.NoError(t, err)
		})
	}
}

func TestWaitingChildPreventsParentCompletionAndDoesNotOccupySlot(t *testing.T) {
	r, w := promptWork(t)
	ctx := context.Background()
	done, err := r.Finish(ctx, w.Scope, 0)
	require.NoError(t, err)
	require.False(t, done)
	r.Concurrency = 1
	_, err = r.Spawn(ctx, w.Scope, "another", "other", "agent", "{}", "task")
	require.NoError(t, err)
	var calls atomic.Int32
	r.Run = func(context.Context, Work) (Result, error) { calls.Add(1); return Result{}, nil }
	r.Tick(ctx)
	r.workers.Wait()
	require.Equal(t, int32(1), calls.Load())
}

func TestCompletedParentHeartbeatDoesNotCancelFinalDelivery(t *testing.T) {
	store, scope := testStore(t)
	r := New(store, nil)
	r.Poll = time.Millisecond
	b, err := r.AcquireParent(context.Background(), scope)
	require.NoError(t, err)
	ctx, stop := b.Start(context.Background())
	defer stop()
	done, err := r.Finish(ctx, scope, 0)
	require.NoError(t, err)
	require.True(t, done)
	select {
	case <-ctx.Done():
		t.Fatal("normal completion canceled final event delivery")
	case <-time.After(30 * time.Millisecond):
	}
}

func TestWaitWakesForSteerWithoutConsumingOrClosingGroup(t *testing.T) {
	for _, before := range []bool{true, false} {
		t.Run(map[bool]string{true: "already-pending", false: "arrives-during-wait"}[before], func(t *testing.T) {
			store, scope := testStore(t)
			r := New(store, nil)
			r.Poll = 5 * time.Millisecond
			var ready atomic.Bool
			ready.Store(before)
			b := &Binding{
				Runtime:  r,
				Scope:    scope,
				HasInput: func(context.Context) (bool, error) { return ready.Load(), nil },
			}
			done := make(chan error, 1)
			go func() { _, err := b.Wait(context.Background(), 0, time.Minute); done <- err }()
			ready.Store(true)
			select {
			case err := <-done:
				require.ErrorIs(t, err, ErrInput)
			case <-time.After(time.Second):
				t.Fatal("steer did not wake wait")
			}
			require.True(t, ready.Load())
			_, err := b.BeforeFinish(context.Background())
			require.ErrorIs(t, err, ErrInput)
			g, err := store.Read(context.Background(), scope)
			require.NoError(t, err)
			require.Equal(t, "active", g.Status)
		})
	}
}

func TestProgressIsObservableButDoesNotWakeAndLateWorkerIsFenced(t *testing.T) {
	store, scope := testStore(t)
	r := New(store, nil)
	ctx := context.Background()
	_, err := r.Spawn(ctx, scope, "spawn", "child", "a", "{}", "work")
	require.NoError(t, err)
	var w Work
	require.NoError(t, store.Change(ctx, scope, func(g *Group) error {
		m := g.Members[0]
		v := current(m)
		v.State = "running"
		v.Owner = "worker"
		v.Lease = time.Now().Add(time.Minute)
		w = Work{scope, *m, *v}
		return nil
	}))
	require.NoError(t, r.RecordProgress(ctx, w, Progress{Phase: "tool_running", ToolName: "mcp_write", Iteration: 2}))
	b := &Binding{Runtime: r, Scope: scope}
	list, err := b.List(ctx, "")
	require.NoError(t, err)
	require.Equal(t, "mcp_write", list[0].Progress.ToolName)
	updated, err := r.Wait(ctx, scope, 0, 0)
	require.NoError(t, err)
	require.False(t, updated)
	child := &Binding{Runtime: r, Scope: scope, Child: &w}
	require.NoError(t, child.MessageParent(ctx, "milestone", "sources checked; one article is unavailable"))
	updated, err = b.Wait(ctx, 0, time.Second)
	require.NoError(t, err)
	require.True(t, updated, "a key finding must wake the parent before the child finishes")
	events, err := b.Poll(ctx)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "message", events[0].Kind)
	require.Equal(t, "sources checked; one article is unavailable", events[0].Content)
	require.Equal(t, "running", current(read(t, store, scope).Members[0]).State)
	require.NoError(t, r.Cancel(ctx, scope))
	require.ErrorIs(t, child.MessageParent(ctx, "late", "late finding"), ErrClosed)
	require.ErrorIs(t, r.RecordProgress(ctx, w, Progress{Phase: "tool_succeeded"}), ErrConflict)
}

func TestRestartWakeOnSteerWithoutChildResultAndCancellationWins(t *testing.T) {
	for _, cancelTask := range []bool{false, true} {
		t.Run(map[bool]string{false: "resume", true: "cancel"}[cancelTask], func(t *testing.T) {
			store, scope := testStore(t)
			r := New(store, nil)
			ctx := context.Background()
			b, err := r.AcquireParent(ctx, scope)
			require.NoError(t, err)
			require.NoError(t, b.SaveWait(ctx, []byte("safe"), time.Now().Add(time.Hour)))
			require.NoError(
				t,
				store.Change(
					ctx,
					scope,
					func(g *Group) error { g.ParentLease = time.Now().Add(-time.Minute); return nil },
				),
			)
			r.ParentInput = func(context.Context, Scope, string) (bool, error) { return true, nil }
			var calls atomic.Int32
			r.WakeParent = func(ctx context.Context,
				b *Binding,
				_ []byte,
			) error {
				calls.Add(1)
				return b.ClearWait(ctx)
			}
			if cancelTask {
				require.NoError(t, r.Cancel(ctx, scope))
			}
			r.Tick(ctx)
			r.workers.Wait()
			if cancelTask {
				require.Zero(t, calls.Load())
			} else {
				require.Equal(t, int32(1), calls.Load())
			}
		})
	}
}

func TestAbandonedParentWithoutCheckpointClosesInsteadOfReplaying(t *testing.T) {
	store, scope := testStore(t)
	ctx := context.Background()
	r := New(store, nil)
	b, err := r.AcquireParent(ctx, scope)
	require.NoError(t, err)
	require.NoError(
		t,
		store.Change(ctx, scope, func(g *Group) error { g.ParentLease = time.Now().Add(-time.Second); return nil }),
	)
	var calls atomic.Int32
	r.WakeParent = func(ctx context.Context, recovered *Binding, snapshot []byte) error {
		calls.Add(1)
		if len(snapshot) != 0 {
			return ErrConflict
		}
		return r.Cancel(recovered.Context(ctx), scope)
	}
	for range 5 {
		r.Tick(ctx)
	}
	r.workers.Wait()
	require.Equal(t, int32(1), calls.Load())
	require.Equal(t, "cancelled", read(t, store, scope).Status)
	require.Error(t, r.CheckParent(ctx, scope, b.ParentOwner))
	for range 3 {
		r.Tick(ctx)
	}
	require.Equal(t, int32(1), calls.Load())
}

func TestExpiredParentFinalizationRetriesAfterRestartWithoutExecution(t *testing.T) {
	for _, checkpoint := range []bool{false, true} {
		t.Run(map[bool]string{false: "in-flight", true: "waiting"}[checkpoint], func(t *testing.T) {
			store, scope := testStore(t)
			ctx := context.Background()
			var executions atomic.Int32
			runner := func(context.Context, Work) (Result, error) { executions.Add(1); return Result{}, nil }
			r := New(store, runner)
			b, err := r.AcquireParent(ctx, scope)
			require.NoError(t, err)
			_, err = r.Spawn(ctx, scope, "spawn", "child", "a", "{}", "task")
			require.NoError(t, err)
			if checkpoint {
				require.NoError(t, b.SaveWait(ctx, []byte("must never replay"), time.Now().Add(time.Hour)))
			}
			require.NoError(t, store.Change(ctx, scope, func(g *Group) error {
				g.ParentLease = time.Now().Add(-time.Minute)
				g.Deadline = time.Now().Add(-time.Second)
				return nil
			}))
			r.WakeParent = func(ctx context.Context, b *Binding, snapshot []byte) error {
				if len(snapshot) != 0 {
					return errors.New("execution snapshot must not be replayed")
				}
				if err := r.CheckParentFinalization(ctx, scope, b.ParentOwner); err != nil {
					return err
				}
				return errors.New("temporary delivery failure")
			}
			r.Tick(ctx)
			r.workers.Wait()
			g := read(t, store, scope)
			require.Equal(t, "cancelling", g.Status)
			require.True(t, g.ParentClosePending)
			require.Equal(t, "cancelled", g.Members[0].Runs[0].State)
			require.Error(t, r.CheckParent(ctx, scope, g.ParentOwner), "closing authority cannot execute tools")
			// Restart with the same durable record and expire the failed claim.
			require.NoError(
				t,
				store.Change(
					ctx,
					scope,
					func(g *Group) error { g.ParentLease = time.Now().Add(-time.Second); return nil },
				),
			)
			restarted := New(NewStore(store.db), runner)
			var closures atomic.Int32
			restarted.WakeParent = func(ctx context.Context, b *Binding, snapshot []byte) error {
				if len(snapshot) != 0 {
					return errors.New("execution snapshot must not be replayed")
				}
				if err := restarted.CheckParentFinalization(ctx, scope, b.ParentOwner); err != nil {
					return err
				}
				closures.Add(1)
				return nil
			}
			for range 3 {
				restarted.Tick(ctx)
				restarted.workers.Wait()
			}
			require.Equal(t, int32(1), closures.Load())
			require.Zero(t, executions.Load())
			g = read(t, store, scope)
			require.Equal(t, "cancelled", g.Status)
			require.False(t, g.ParentClosePending)
		})
	}
}
