package collaboration

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func testStore(t *testing.T) (*Store, Scope) {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open(filepath.Join(t.TempDir(), "work.db")+"?_busy_timeout=5000&_journal_mode=WAL"),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)},
	)
	require.NoError(t, err)
	sql, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sql.Close() })
	require.NoError(t, db.AutoMigrate(&Record{}))
	require.NoError(
		t,
		db.Exec(
			"CREATE UNIQUE INDEX active_parent ON agent_collaborations(tenant_id,user_id,sess"+
				"ion_id) WHERE status IN ('active','cancelling')",
		).Error,
	)
	store := NewStore(db)
	scope := Scope{7, "u", "parent", uuid.NewString()}
	require.NoError(t, store.Ensure(context.Background(), scope))
	return store, scope
}

func read(t *testing.T, s *Store, scope Scope) *Group {
	t.Helper()
	g, err := s.Read(context.Background(), scope)
	require.NoError(t, err)
	return g
}

func start(t *testing.T, r *Runtime) {
	t.Helper()
	r.Poll = 5 * time.Millisecond
	r.Lease = time.Second
	r.Start(context.Background())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		require.NoError(t, r.Stop(ctx))
	})
}

func eventually(t *testing.T, fn func() bool) {
	t.Helper()
	require.Eventually(t, fn, 4*time.Second, 5*time.Millisecond)
}

func TestConcurrentSpawnIsIdempotentAndScoped(t *testing.T) {
	s, scope := testStore(t)
	r := New(s, nil)
	ctx := context.Background()
	var wg sync.WaitGroup
	ids := make(chan string, 24)
	errs := make(chan error, 24)
	for range 24 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := r.Spawn(ctx, scope, "same-call", "research", "agent", "{}", "question")
			ids <- id
			errs <- err
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	id := ""
	for v := range ids {
		if id == "" {
			id = v
		}
		require.Equal(t, id, v)
	}
	require.Len(t, read(t, s, scope).Members, 1)
	other := scope
	other.UserID = "other"
	_, err := r.Spawn(ctx, other, "call2", "other", "a", "{}", "q")
	require.ErrorIs(t, err, ErrScope)
	other = scope
	other.TenantID++
	require.ErrorIs(t, s.Ensure(ctx, other), ErrScope)
}

func TestTwoSchedulersShareConcurrencyAndNeverDoubleExecute(t *testing.T) {
	s, scope := testStore(t)
	ctx := context.Background()
	var live, peak, total atomic.Int32
	release := make(chan struct{})
	defer close(release)
	seen := sync.Map{}
	runner := func(ctx context.Context, w Work) (Result, error) {
		if _, loaded := seen.LoadOrStore(w.Run.ID, true); loaded {
			return Result{}, fmt.Errorf("duplicate run")
		}
		n := live.Add(1)
		defer live.Add(-1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		total.Add(1)
		select {
		case <-release:
			return Result{Answer: "ok"}, nil
		case <-ctx.Done():
			return Result{}, ctx.Err()
		}
	}
	r1, r2 := New(s, runner), New(NewStore(s.db), runner)
	for i := range 8 {
		_, err := r1.Spawn(ctx, scope, fmt.Sprint(i), fmt.Sprintf("task_%d", i), "a", "{}", "q")
		require.NoError(t, err)
	}
	start(t, r1)
	start(t, r2)
	eventually(t, func() bool { return total.Load() == 3 })
	require.EqualValues(t, 3, peak.Load())
	require.NoError(t, r1.Cancel(ctx, scope))
	eventually(t, func() bool { return read(t, s, scope).Status == "cancelled" })
	require.EqualValues(t, 3, total.Load())
}

func TestIdleMessageDoesNotRunFollowupDoesAndStaleInterruptCannotCancelIt(t *testing.T) {
	s, scope := testStore(t)
	var count atomic.Int32
	started := make(chan Work, 2)
	release := make(chan struct{})
	defer close(release)
	r := New(s, func(ctx context.Context, w Work) (Result, error) {
		n := count.Add(1)
		started <- w
		if n == 2 {
			select {
			case <-ctx.Done():
				return Result{}, ctx.Err()
			case <-release:
			}
		}
		return Result{Answer: "done"}, nil
	})
	id, err := r.Spawn(context.Background(), scope, "spawn", "one", "a", "{}", "q")
	require.NoError(t, err)
	start(t, r)
	first := <-started
	eventually(t, func() bool { return current(read(t, s, scope).Members[0]).State == "succeeded" })
	_, err = r.Send(context.Background(), scope, "msg", id, "queued for later", "", false)
	require.NoError(t, err)
	r.Tick(context.Background())
	require.EqualValues(t, 1, count.Load())
	_, err = r.Send(context.Background(), scope, "follow", id, "continue", first.Run.ID, true)
	require.NoError(t, err)
	second := <-started
	require.NotEqual(t, first.Run.ID, second.Run.ID)
	require.ErrorIs(t, r.Interrupt(context.Background(), scope, id, first.Run.ID), ErrConflict)
	require.NoError(t, r.Interrupt(context.Background(), scope, id, second.Run.ID))
	eventually(t, func() bool { return current(read(t, s, scope).Members[0]).State == "interrupted" })
}

func TestFollowupAtFinishIsNeitherLostNorProcessedTwice(t *testing.T) {
	for _, consume := range []bool{false, true} {
		t.Run(fmt.Sprint(consume), func(t *testing.T) {
			s, scope := testStore(t)
			entered, release := make(chan struct{}), make(chan struct{})
			var calls atomic.Int32
			var r *Runtime
			r = New(s, func(ctx context.Context, w Work) (Result, error) {
				n := calls.Add(1)
				result := Result{Answer: "ok"}
				if n == 1 {
					close(entered)
					select {
					case <-release:
					case <-ctx.Done():
						return result, ctx.Err()
					}
					if consume {
						mail, err := r.Inbox(ctx, w, 0)
						if err != nil {
							return result, err
						}
						for _, m := range mail {
							result.ConsumedSequence = max(result.ConsumedSequence, m.Sequence)
						}
					}
				}
				return result, nil
			})
			id, err := r.Spawn(context.Background(), scope, "spawn", "one", "a", "{}", "q")
			require.NoError(t, err)
			start(t, r)
			<-entered
			_, err = r.Send(context.Background(), scope, "follow", id, "more work", "", true)
			require.NoError(t, err)
			close(release)
			want := 1
			if !consume {
				want = 2
			}
			eventually(t, func() bool {
				g := read(t, s, scope)
				return len(g.Members[0].Runs) == want && current(g.Members[0]).State == "succeeded"
			})
			r.Tick(context.Background())
			require.EqualValues(t, want, calls.Load())
		})
	}
}

func TestCancellationFencesLateSuccessAndPreventsResurrection(t *testing.T) {
	s, scope := testStore(t)
	entered, release := make(chan struct{}), make(chan struct{})
	r := New(s, func(_ context.Context, _ Work) (Result, error) {
		close(entered)
		<-release
		return Result{Answer: "late success"}, nil
	})
	id, err := r.Spawn(context.Background(), scope, "spawn", "one", "a", "{}", "q")
	require.NoError(t, err)
	start(t, r)
	<-entered
	require.NoError(t, r.Cancel(context.Background(), scope))
	close(release)
	eventually(t, func() bool { return read(t, s, scope).Status == "cancelled" })
	g := read(t, s, scope)
	require.Equal(t, "cancelled", current(g.Members[0]).State)
	require.Len(t, g.Events, 1)
	_, err = r.Send(context.Background(), scope, "follow", id, "continue", "", true)
	require.ErrorIs(t, err, ErrClosed)
	_, err = r.Spawn(context.Background(), scope, "new", "new", "a", "{}", "q")
	require.ErrorIs(t, err, ErrClosed)
}

func TestRestartClaimsQueuedButDoesNotReplayUncertainExecution(t *testing.T) {
	s, scope := testStore(t)
	ctx := context.Background()
	r := New(s, nil)
	_, err := r.Spawn(ctx, scope, "one", "one", "a", "{}", "q")
	require.NoError(t, err)
	_, err = r.Spawn(ctx, scope, "two", "two", "a", "{}", "q")
	require.NoError(t, err)
	require.NoError(t, s.Change(ctx, scope, func(g *Group) error {
		v := current(g.Members[0])
		v.State = "running"
		v.Owner = "dead-worker"
		v.Lease = time.Now().Add(-time.Second)
		return nil
	}))
	var calls atomic.Int32
	restarted := New(NewStore(s.db), func(_ context.Context, _ Work) (Result, error) {
		calls.Add(1)
		return Result{Answer: "recovered queue"}, nil
	})
	start(t, restarted)
	eventually(t, func() bool { g := read(t, s, scope); return current(g.Members[1]).State == "succeeded" })
	g := read(t, s, scope)
	require.Equal(t, "needs_attention", current(g.Members[0]).State)
	require.EqualValues(t, 1, calls.Load())
	_, err = restarted.Send(ctx, scope, "follow", "one", "retry", "", true)
	require.Error(t, err)
}

func TestWaitTimeoutCancellationAndPreexistingEvent(t *testing.T) {
	s, scope := testStore(t)
	r := New(s, nil)
	r.Poll = time.Millisecond
	ctx := context.Background()
	updated, err := r.Wait(ctx, scope, 0, 5*time.Millisecond)
	require.NoError(t, err)
	require.False(t, updated)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = r.Wait(cancelled, scope, 0, time.Second)
	require.ErrorIs(t, err, context.Canceled)
	_, err = r.Spawn(ctx, scope, "spawn", "one", "a", "{}", "q")
	require.NoError(t, err)
	require.NoError(t, r.Interrupt(ctx, scope, "one", ""))
	updated, err = r.Wait(ctx, scope, 0, time.Second)
	require.NoError(t, err)
	require.True(t, updated)
	b := &Binding{Runtime: r, Scope: scope}
	events, err := b.BeforeFinish(ctx)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "active", read(t, s, scope).Status)
	events, err = b.BeforeFinish(ctx)
	require.NoError(t, err)
	require.Empty(t, events)
	require.Equal(t, "completed", read(t, s, scope).Status)
}

func TestExpiredWorkerCannotCommitOrSendMessages(t *testing.T) {
	s, scope := testStore(t)
	ctx := context.Background()
	entered, release := make(chan struct{}), make(chan struct{})
	var work Work
	r := New(s, func(_ context.Context, w Work) (Result, error) {
		work = w
		close(entered)
		<-release
		return Result{Answer: "stale"}, nil
	})
	_, err := r.Spawn(ctx, scope, "spawn", "one", "a", "{}", "q")
	require.NoError(t, err)
	r.Poll = time.Hour
	r.Lease = time.Hour
	r.Tick(ctx)
	<-entered
	require.NoError(
		t,
		s.Change(
			ctx,
			scope,
			func(g *Group) error { current(g.Members[0]).Lease = time.Now().Add(-time.Second); return nil },
		),
	)
	r.Tick(ctx)
	b := &Binding{Runtime: r, Scope: scope, Child: &work}
	require.ErrorIs(t, b.MessageParent(ctx, "msg", "stale message"), ErrConflict)
	close(release)
	r.workers.Wait()
	g := read(t, s, scope)
	require.Equal(t, "needs_attention", current(g.Members[0]).State)
	require.Len(t, g.Events, 1)
	require.NotContains(t, g.Events[0].Content, "stale")
}

func TestParentRecoveryFencesOldEngineAndPreservesResults(t *testing.T) {
	s, scope := testStore(t)
	r := New(s, nil)
	ctx := context.Background()
	first, err := r.AcquireParent(ctx, scope)
	require.NoError(t, err)
	_, err = r.Spawn(first.Context(ctx), scope, "spawn", "one", "a", "{}", "q")
	require.NoError(t, err)
	require.NoError(t, r.Interrupt(ctx, scope, "one", ""))
	next := scope
	next.ID = "next-message"
	_, err = r.AcquireParent(ctx, next)
	require.Error(t, err, "live parent must not be stolen")
	require.NoError(t, s.Change(ctx, scope, func(g *Group) error {
		g.ParentLease = time.Now().Add(-time.Second)
		g.RootQuery = "original user goal"
		return nil
	}))
	second, err := r.AcquireParent(ctx, next)
	require.NoError(t, err)
	require.Equal(t, scope.ID, second.Scope.ID)
	_, err = r.Spawn(first.Context(ctx), scope, "stale", "stale", "a", "{}", "q")
	require.ErrorIs(t, err, ErrConflict)
	first.End(false)
	require.Equal(t, "active", read(t, s, scope).Status, "old cleanup must not cancel recovered task")
	events, err := second.Poll(ctx)
	require.NoError(t, err)
	require.Len(t, events, 2)
	require.Equal(t, "recovered", events[0].Kind)
	require.Contains(t, events[0].Content, "original user goal")
	second.End(false)
	require.Equal(t, "cancelled", read(t, s, scope).Status)
	third, err := r.AcquireParent(ctx, next)
	require.NoError(t, err)
	require.Equal(t, next.ID, third.Scope.ID)
	require.NotEqual(t, scope.ID, third.Scope.ID, "a cancelled group must never revive")
}

func TestDurableStopAfterParentCrashAndStaleStopAfterRecovery(t *testing.T) {
	s, scope := testStore(t)
	r := New(s, nil)
	ctx := context.Background()
	first, err := r.AcquireParent(ctx, scope)
	require.NoError(t, err)
	_, err = r.Spawn(first.Context(ctx), scope, "spawn", "one", "a", "{}", "q")
	require.NoError(t, err)
	require.NoError(
		t,
		s.Change(ctx, scope, func(g *Group) error { g.ParentLease = time.Now().Add(-time.Second); return nil }),
	)
	next := scope
	next.ID = "new-parent-message"
	_, err = r.AcquireParent(ctx, next)
	require.NoError(t, err)
	require.NoError(t, r.CancelMessage(ctx, scope, scope.ID))
	require.Equal(t, "active", read(t, s, scope).Status)
	require.NoError(t, r.CancelMessage(ctx, scope, next.ID))
	require.Equal(t, "cancelled", read(t, s, scope).Status)
	var calls atomic.Int32
	restarted := New(s, func(context.Context, Work) (Result, error) { calls.Add(1); return Result{}, nil })
	restarted.Tick(ctx)
	require.Zero(t, calls.Load())
}

func TestInputBudgetCannotPreventCancellation(t *testing.T) {
	s, scope := testStore(t)
	r := New(s, nil)
	ctx := context.Background()
	_, err := r.Spawn(ctx, scope, "spawn", "one", "a", "{}", "q")
	require.NoError(t, err)
	require.NoError(t, s.Change(ctx, scope, func(g *Group) error { g.InputBytes = 512 << 10; return nil }))
	_, err = r.Send(ctx, scope, "msg", "one", "more", "", false)
	require.ErrorContains(t, err, "budget")
	require.NoError(t, r.Cancel(ctx, scope))
	require.Equal(t, "cancelled", read(t, s, scope).Status)
}

func TestFollowupCASRetryAfterCompletionAllocatesFreshRunID(t *testing.T) {
	s, scope := testStore(t)
	r := New(s, nil)
	ctx := context.Background()
	_, err := r.Spawn(ctx, scope, "spawn", "one", "a", "{}", "q")
	require.NoError(t, err)
	require.NoError(t, s.Change(ctx, scope, func(g *Group) error {
		v := current(g.Members[0])
		v.State = "running"
		v.Owner = "worker"
		v.Lease = time.Now().Add(time.Minute)
		return nil
	}))
	terminal := read(t, s, scope)
	oldRunID := current(terminal.Members[0]).ID
	current(terminal.Members[0]).State = "succeeded"
	payload, err := json.Marshal(terminal)
	require.NoError(t, err)
	// Force the exact CAS boundary: Send read running and prepared mail, but
	// the worker commits completion before that update reaches the database.
	var injected atomic.Bool
	require.NoError(t, s.db.Callback().Update().Before("gorm:update").Register("finish_during_send", func(tx *gorm.DB) {
		if injected.Swap(true) {
			return
		}
		if err := s.db.Exec("UPDATE agent_collaborations SET data = ?, version = version + 1 WHERE id = ?",
			string(payload),
			scope.ID).Error; err != nil {
			_ = tx.AddError(err)
		}
	}))
	id, err := r.Send(ctx, scope, "follow", "one", "continue", "", true)
	require.NoError(t, err)
	g := read(t, s, scope)
	require.Len(t, g.Members[0].Runs, 2)
	require.NotEqual(t, oldRunID, id)
	require.Equal(t, id, current(g.Members[0]).ID)
}

func TestResultDeliveryPreservesLongAnswerAndPartialStatus(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(fmt.Sprint(partial), func(t *testing.T) {
			s, scope := testStore(t)
			answer := strings.Repeat("带有引用的完整内容。", 1200) + "结尾：今天的信号"
			r := New(
				s,
				func(context.Context, Work) (Result, error) { return Result{Answer: answer, Truncated: partial}, nil },
			)
			_, err := r.Spawn(context.Background(), scope, "spawn", "report", "a", "{}", "q")
			require.NoError(t, err)
			start(t, r)
			eventually(t, func() bool { return !active(current(read(t, s, scope).Members[0])) })
			// Reopen the persisted store as a restarted parent would.
			b := &Binding{Runtime: New(NewStore(s.db), nil), Scope: scope}
			events, err := b.Poll(context.Background())
			require.NoError(t, err)
			require.Len(t, events, 1)
			var payload struct {
				Result Result `json:"result"`
			}
			require.NoError(t, json.Unmarshal([]byte(events[0].Content), &payload))
			require.Equal(t, answer, payload.Result.Answer)
			require.Equal(t, partial, payload.Result.Truncated)
			if partial {
				require.Equal(t, "failed", events[0].Kind)
				require.Contains(t, payload.Result.Error, "partial")
			} else {
				require.Equal(t, "succeeded", events[0].Kind)
			}
		})
	}
}

func TestApprovalWaitingWakesModelOnceAfterRestart(t *testing.T) {
	s, scope := testStore(t)
	ctx := context.Background()
	r := New(s, nil)
	b, err := r.AcquireParent(ctx, scope)
	require.NoError(t, err)
	require.NoError(t, b.SaveWait(ctx, []byte("safe"), time.Now().Add(time.Hour)))
	require.NoError(t, s.Change(ctx, scope, func(g *Group) error {
		g.ParentLease = time.Time{}
		g.NextSequence = 1
		g.Events = []Event{{Sequence: 1, Kind: "waiting"}}
		return nil
	}))
	restarted := New(NewStore(s.db), nil)
	var calls atomic.Int32
	restarted.WakeParent = func(ctx context.Context, resumed *Binding, _ []byte) error {
		calls.Add(1)
		events, err := resumed.Poll(ctx)
		require.NoError(t, err)
		require.Len(t, events, 1)
		return resumed.SaveWait(ctx, []byte("safe"), time.Now().Add(time.Hour))
	}
	restarted.wakeParent(ctx, scope)
	restarted.workers.Wait()
	require.Equal(t, int32(1), calls.Load())
	updated, err := restarted.Wait(ctx, scope, 0, time.Millisecond)
	require.NoError(t, err)
	require.True(t, updated)
	polled := &Binding{Runtime: restarted, Scope: scope}
	events, err := polled.Poll(ctx)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "waiting", events[0].Kind)
	require.Equal(t, int64(1), polled.Seen)
	events, err = polled.Poll(ctx)
	require.NoError(t, err)
	require.Empty(t, events)
	require.NoError(t, s.Change(ctx, scope, func(g *Group) error {
		g.ParentLease = time.Time{}
		return nil
	}))
	restarted.wakeParent(ctx, scope)
	restarted.workers.Wait()
	require.Equal(t, int32(1), calls.Load(), "consumed waiting state must not wake again")
	require.NoError(t, s.Change(ctx, scope, func(g *Group) error {
		g.NextSequence++
		g.Events = append(g.Events, Event{Sequence: g.NextSequence, Kind: "succeeded"})
		return nil
	}))
	restarted.wakeParent(ctx, scope)
	restarted.workers.Wait()
	require.Equal(t, int32(2), calls.Load())
}

// Mix successful, failed, panicking and canceled children under two competing
// schedulers. One child's failure/cancel must not consume a sibling's result or
// strand queued work, and the parent may close only after consuming all events.
func TestConcurrentMixedOutcomesDrainQueueAndDeliverEachRunOnce(t *testing.T) {
	s, scope := testStore(t)
	ctx := context.Background()
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	var live, peak, total, duplicates atomic.Int32
	var seen sync.Map
	runner := func(_ context.Context, w Work) (Result, error) {
		if _, loaded := seen.LoadOrStore(w.Run.ID, true); loaded {
			duplicates.Add(1)
		}
		total.Add(1)
		n := live.Add(1)
		defer live.Add(-1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		if w.Member.Name == "task_0" || w.Member.Name == "task_1" || w.Member.Name == "task_2" {
			<-release // deliberately allow task_2's result to arrive after interrupt
		}
		switch w.Member.Name {
		case "task_0":
			return Result{}, fmt.Errorf("isolated child failure")
		case "task_1":
			panic("isolated child panic")
		default:
			return Result{Answer: "result:" + w.Member.Name}, nil
		}
	}
	r1, r2 := New(s, runner), New(NewStore(s.db), runner)
	for i := range 8 {
		_, err := r1.Spawn(ctx, scope, fmt.Sprint(i), fmt.Sprintf("task_%d", i), "a", "{}", "read only")
		require.NoError(t, err)
	}
	start(t, r1)
	start(t, r2)
	eventually(t, func() bool { return total.Load() == 3 })
	require.EqualValues(t, 3, peak.Load())
	require.NoError(t, r1.Interrupt(ctx, scope, "task_2", ""))
	require.NoError(t, r2.Interrupt(ctx, scope, "task_7", ""))
	once.Do(func() { close(release) })
	eventually(t, func() bool {
		for _, m := range read(t, s, scope).Members {
			if active(current(m)) {
				return false
			}
		}
		return true
	})
	require.EqualValues(t, 7, total.Load(), "queued canceled child must never start")
	require.Zero(t, duplicates.Load())
	require.LessOrEqual(t, peak.Load(), int32(3))
	g := read(t, s, scope)
	want := []string{
		"failed",
		"failed",
		"interrupted",
		"succeeded",
		"succeeded",
		"succeeded",
		"succeeded",
		"interrupted",
	}
	for i, m := range g.Members {
		require.Equal(t, want[i], current(m).State, m.Name)
		require.Empty(t, current(m).Result.Answer, "the published event is the only answer copy")
	}
	answers := map[string]string{}
	for _, e := range g.Events {
		var payload struct {
			Result Result `json:"result"`
		}
		if e.Kind == "succeeded" && json.Unmarshal([]byte(e.Content), &payload) == nil {
			answers[e.From] = payload.Result.Answer
		}
	}
	for i, m := range g.Members {
		if want[i] == "succeeded" {
			require.Equal(t, "result:"+m.Name, answers[m.Name])
		}
	}
	parent := &Binding{Runtime: r1, Scope: scope}
	done, err := r1.Finish(ctx, scope, parent.Seen)
	require.NoError(t, err)
	require.False(t, done, "unread child outcomes prevent early parent completion")
	events, err := parent.Poll(ctx)
	require.NoError(t, err)
	require.Len(t, events, 8)
	unique := map[string]bool{}
	for _, e := range events {
		require.False(t, unique[e.From])
		unique[e.From] = true
	}
	events, err = parent.Poll(ctx)
	require.NoError(t, err)
	require.Empty(t, events)
	done, err = r1.Finish(ctx, scope, parent.Seen)
	require.NoError(t, err)
	require.True(t, done)
	// A fresh scheduler cannot replay already committed children.
	restarted := New(NewStore(s.db), runner)
	restarted.Tick(ctx)
	restarted.workers.Wait()
	require.EqualValues(t, 7, total.Load())
}

// A failed model request after an external effect cannot authorize replay of
// consumed guidance. Only genuinely new mail at the finishing boundary may run.
func TestFailedRunDoesNotReplayConsumedFollowup(t *testing.T) {
	for _, failure := range []string{"error", "panic", "missing_result"} {
		for _, late := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/late=%t", failure, late), func(t *testing.T) {
				s, scope := testStore(t)
				ctx := t.Context()
				r := New(s, nil)
				id, err := r.Spawn(ctx, scope, "spawn", "child", "agent", "{}", "initial")
				require.NoError(t, err)
				_, err = r.Send(ctx, scope, "consumed", id, "COMPLETED_EXTERNAL_EFFECT", "", true)
				require.NoError(t, err)
				var effects atomic.Int32
				r.Run = func(ctx context.Context, w Work) (Result, error) {
					mail, inboxErr := r.Inbox(ctx, w, 0)
					if inboxErr != nil {
						return Result{}, inboxErr
					}
					require.Len(t, mail, 1)
					effects.Add(1)
					if late {
						_, sendErr := r.Send(ctx, scope, "late", id, "NEW_UNCONSUMED_TASK", "", true)
						if sendErr != nil {
							return Result{}, sendErr
						}
					}
					if failure == "panic" {
						panic("model adapter panic after external effect")
					}
					if failure == "missing_result" {
						return Result{}, fmt.Errorf("executor lost its result")
					}
					return Result{ConsumedSequence: mail[0].Sequence}, fmt.Errorf("later model request failed")
				}
				r.Tick(ctx)
				r.workers.Wait()
				g := read(t, s, scope)
				require.Equal(t, "failed", g.Members[0].Runs[0].State)
				wantRuns := 1
				if late {
					wantRuns = 2
					require.Equal(t, "NEW_UNCONSUMED_TASK", g.Members[0].Runs[1].Query)
				}
				require.Len(t, g.Members[0].Runs, wantRuns)
				var newCalls atomic.Int32
				restarted := New(NewStore(s.db), func(_ context.Context, w Work) (Result, error) {
					newCalls.Add(1)
					require.Equal(t, "NEW_UNCONSUMED_TASK", w.Run.Query)
					return Result{Answer: "new task completed"}, nil
				})
				for i := 0; i < 3; i++ {
					restarted.Tick(ctx)
					restarted.workers.Wait()
				}
				require.EqualValues(t, 1, effects.Load())
				require.EqualValues(t, wantRuns-1, newCalls.Load())
				require.Len(t, read(t, s, scope).Members[0].Runs, wantRuns)
			})
		}
	}
}

func TestSuspendedFollowupDeliverySurvivesFailedResume(t *testing.T) {
	s, scope := testStore(t)
	ctx := t.Context()
	r := New(s, nil)
	id, err := r.Spawn(ctx, scope, "spawn", "child", "a", "{}", "initial")
	require.NoError(t, err)
	_, err = r.Send(ctx, scope, "followup", id, "external effect", "", true)
	require.NoError(t, err)
	var suspended Work
	r.Run = func(ctx context.Context, w Work) (Result, error) {
		mail, err := r.Inbox(ctx, w, 0)
		require.NoError(t, err)
		require.Len(t, mail, 1)
		require.NoError(t, r.AwaitPrompt(ctx, w, PromptState{ID: "approval", Status: "suspended"}))
		suspended = w
		return Result{Suspended: true}, nil
	}
	r.Tick(ctx)
	r.workers.Wait()
	require.Equal(t, "waiting", read(t, s, scope).Members[0].Runs[0].State)
	restarted := New(NewStore(s.db), func(context.Context, Work) (Result, error) {
		return Result{}, fmt.Errorf("resume failed before restoring engine snapshot")
	})
	suspended.Run.PromptID = "approval"
	require.NoError(t, restarted.ReconcilePrompt(ctx, suspended, PromptState{ID: "approval", Status: "answered"}))
	for i := 0; i < 3; i++ {
		restarted.Tick(ctx)
		restarted.workers.Wait()
	}
	g := read(t, s, scope)
	require.Len(t, g.Members[0].Runs, 1)
	require.Equal(t, "failed", g.Members[0].Runs[0].State)
	require.Positive(t, g.Members[0].Runs[0].Result.ConsumedSequence)
}

func TestParentHeartbeatExtendsDeadline(t *testing.T) {
	s, scope := testStore(t)
	r := New(s, nil)
	r.Poll = 5 * time.Millisecond
	r.Lease = time.Second
	ctx := context.Background()
	b, err := r.AcquireParent(ctx, scope)
	require.NoError(t, err)
	require.NoError(t, s.Change(b.Context(ctx), scope, func(g *Group) error {
		g.Deadline = time.Now().Add(-time.Minute)
		return nil
	}))
	_, stop := b.Start(ctx)
	defer stop()
	require.Eventually(t, func() bool {
		return read(t, s, scope).Deadline.After(time.Now().Add(30 * time.Minute))
	}, time.Second, 5*time.Millisecond)
	r.Tick(context.Background())
	require.Equal(t, "active", read(t, s, scope).Status)
}

func TestChildHeartbeatExtendsDeadline(t *testing.T) {
	s, scope := testStore(t)
	ctx := context.Background()
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	r := New(s, func(context.Context, Work) (Result, error) {
		close(entered)
		<-release
		return Result{Answer: "ok"}, nil
	})
	r.Poll = 5 * time.Millisecond
	r.Lease = time.Second
	_, err := r.Spawn(ctx, scope, "spawn", "one", "a", "{}", "q")
	require.NoError(t, err)
	r.Tick(ctx)
	<-entered
	require.NoError(t, s.Change(ctx, scope, func(g *Group) error {
		g.Deadline = time.Now().Add(-time.Minute)
		return nil
	}))
	require.Eventually(t, func() bool {
		return read(t, s, scope).Deadline.After(time.Now().Add(30 * time.Minute))
	}, time.Second, 5*time.Millisecond)
	r.Tick(ctx)
	g := read(t, s, scope)
	require.Equal(t, "running", current(g.Members[0]).State)
	require.Equal(t, "active", g.Status)
}

func TestCanceledPanickingRunCannotQueueFollowup(t *testing.T) {
	s, scope := testStore(t)
	ctx := t.Context()
	r := New(s, nil)
	id, err := r.Spawn(ctx, scope, "spawn", "child", "a", "{}", "initial")
	require.NoError(t, err)
	_, err = r.Send(ctx, scope, "consumed", id, "effect", "", true)
	require.NoError(t, err)
	r.Run = func(ctx context.Context, w Work) (Result, error) {
		_, err := r.Inbox(ctx, w, 0)
		require.NoError(t, err)
		_, err = r.Send(ctx, scope, "late", id, "new task", "", true)
		require.NoError(t, err)
		require.NoError(t, r.Cancel(ctx, scope))
		panic("late panic after cancellation")
	}
	r.Tick(ctx)
	r.workers.Wait()
	g := read(t, s, scope)
	require.Len(t, g.Members[0].Runs, 1)
	require.Equal(t, "cancelled", g.Members[0].Runs[0].State)
	require.Nil(t, g.ParentWait)
}
