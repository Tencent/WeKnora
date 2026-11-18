package collaboration

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/google/uuid"
)

var taskName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,47}$`)

// collaborationHorizon is how long an active parent or child may keep the group
// open. Abandoned groups still end one horizon after the last lease renewal.
const collaborationHorizon = time.Hour

const (
	closedBeforeExecutionError = "child prompt closed before the tool ran; followup_task can continue this child"
	uncertainExecutionError    = "child prompt closed or execution outcome uncertain; not replayed"
)

// touchDeadline extends the horizon in coarse steps. Every extension rewrites
// the whole aggregate, so per-heartbeat extensions would multiply writes.
func touchDeadline(g *Group, now time.Time) {
	until := now.Add(collaborationHorizon)
	if g.Deadline.Before(until.Add(-collaborationHorizon / 10)) {
		g.Deadline = until
	}
}

// heartbeatInterval is how often an owner checks for cancellation. Checks read
// the aggregate; renewals write it only when renewalDue.
func (r *Runtime) heartbeatInterval() time.Duration {
	return min(max(r.Poll, time.Second), r.Lease/3)
}

// renewalDue reports whether a lease has used a third of its duration.
func (r *Runtime) renewalDue(lease, now time.Time) bool {
	return lease.Sub(now) < r.Lease*2/3
}

// promptClosedBeforeExecution reports a prompt that ended while the tool had
// not started. A running turn can still have an uncertain side effect.
func promptClosedBeforeExecution(status, runState string) bool {
	switch status {
	case "expired", "canceled", "superseded":
		return runState != "running"
	default:
		return false
	}
}

// Work is a claimed child execution with its scope and ownership lease.
type Work struct {
	Scope  Scope
	Member Member
	Run    Run
}

// Runner executes claimed child work and returns its durable outcome.
type Runner func(context.Context, Work) (Result, error)

// Runtime schedules durable child work and recovers waiting parent executions.
type Runtime struct {
	Store       *Store
	Run         Runner
	WakeParent  func(context.Context, *Binding, []byte) error
	Reconcile   func(context.Context, Scope) error
	ParentInput func(context.Context, Scope, string) (bool, error)
	// Prune bounds durable storage; it runs every PruneInterval.
	Prune         func(context.Context) error
	PruneInterval time.Duration
	Poll          time.Duration
	Lease         time.Duration
	// Concurrency bounds running children per group; MaxRunning bounds the
	// child executions this process runs across all groups.
	Concurrency int
	MaxRunning  int
	mu          sync.Mutex
	cancel      context.CancelFunc
	done        chan struct{}
	workers     sync.WaitGroup
	running     atomic.Int64
}

// New constructs a runtime with bounded concurrency and lease defaults.
func New(store *Store, run Runner) *Runtime {
	return &Runtime{
		Store: store, Run: run, Poll: 250 * time.Millisecond, Lease: 30 * time.Second,
		Concurrency: 3, MaxRunning: 32, PruneInterval: 10 * time.Minute,
	}
}

// Spawn idempotently enqueues a new child task in the parent group.
func (r *Runtime) Spawn(ctx context.Context, s Scope, key, name, agentID, config, query string) (string, error) {
	if key == "" || len(key) > 512 || len(config) > 65536 || len(agentID) > 64 || !taskName.MatchString(name) ||
		strings.TrimSpace(query) == "" ||
		len(query) > 16000 {
		return "", errors.New("invalid task name, request id or task (maximum 16000 bytes)")
	}
	id, runID, session := uuid.NewString(), uuid.NewString(), uuid.NewString()
	err := r.Store.Change(ctx, s, func(g *Group) error {
		if v := g.Requests[key]; v != "" {
			id = v
			return nil
		}
		if g.Status != "active" {
			return ErrClosed
		}
		if currentTurnMemberCount(g) >= 16 {
			return errors.New("maximum 16 child agents per task")
		}
		if _, err := member(g, name); err == nil {
			return errors.New("task name already exists")
		}
		if err := reserveInput(g, len(query)+len(config)); err != nil {
			return err
		}
		g.Members = append(
			g.Members,
			&Member{
				ID:         id,
				Name:       name,
				AgentID:    agentID,
				SessionID:  session,
				ConfigJSON: config,
				Runs:       []*Run{{ID: runID, State: "queued", Query: query}},
			},
		)
		g.Requests[key] = id
		return nil
	})
	return id, err
}

// Send queues guidance or creates a follow-up turn for an idle child.
func (r *Runtime) Send(
	ctx context.Context,
	s Scope,
	key, target, query, expected string,
	followup bool,
) (string, error) {
	if key == "" || len(key) > 512 || strings.TrimSpace(query) == "" || len(query) > 16000 {
		return "", errors.New("message is empty or too large")
	}
	newRunID := uuid.NewString()
	id := newRunID
	err := r.Store.Change(ctx, s, func(g *Group) error {
		id = newRunID
		if v := g.Requests[key]; v != "" {
			id = v
			return nil
		}
		if g.Status != "active" {
			return ErrClosed
		}
		m, err := member(g, target)
		if err != nil {
			return err
		}
		run := current(m)
		if expected != "" && expected != run.ID {
			return ErrConflict
		}
		if run.State == "needs_attention" {
			return errors.New("execution outcome is uncertain; create a new task only after checking its effects")
		}
		if followup && len(m.Runs) >= 8 {
			return errors.New("maximum 8 turns per child")
		}
		if run.CancelRequested && active(run) {
			return errors.New("child is stopping; wait for its terminal state")
		}
		if len(m.Mail) >= 64 || len(g.Requests) >= 512 {
			return errors.New("message budget exhausted")
		}
		if err := reserveInput(g, len(query)); err != nil {
			return err
		}
		if followup && !active(run) {
			if m.Inherited && currentTurnMemberCount(g) >= 16 {
				return errors.New("maximum 16 child agents per task")
			}
			m.Inherited = false
			m.Runs = append(m.Runs, &Run{ID: id, State: "queued", Query: query})
		} else {
			g.NextSequence++
			kind := "message"
			if followup {
				kind = "followup"
			}
			m.Mail = append(m.Mail, Event{g.NextSequence, "root", run.ID, kind, query})
			id = run.ID
		}
		g.Requests[key] = id
		return nil
	})
	return id, err
}

// Interrupt requests cancellation of the selected child execution.
func (r *Runtime) Interrupt(ctx context.Context, s Scope, target, expected string) error {
	return r.Store.Change(ctx, s, func(g *Group) error {
		m, err := member(g, target)
		if err != nil {
			return err
		}
		run := current(m)
		if expected != "" && expected != run.ID {
			return ErrConflict
		}
		if !active(run) {
			return nil
		}
		run.CancelRequested = true
		if run.State == "queued" || run.State == "waiting" {
			run.State = "interrupted"
			publish(g, m, run)
		}
		return nil
	})
}

// Cancel closes a group and cancels its unfinished child executions.
func (r *Runtime) Cancel(ctx context.Context, s Scope) error {
	return r.cancelMessage(ctx, s, "")
}

// CancelMessage ignores a stale stop for the previous parent turn after takeover.
func (r *Runtime) CancelMessage(ctx context.Context, s Scope, messageID string) error {
	return r.cancelMessage(ctx, s, messageID)
}

func (r *Runtime) cancelMessage(ctx context.Context, s Scope, messageID string) error {
	return r.Store.Change(ctx, s, func(g *Group) error {
		if messageID != "" && messageID != g.ParentMessageID && (g.ParentMessageID != "" || messageID != g.Scope.ID) {
			return nil
		}
		if g.Status == "completed" || g.Status == "cancelled" {
			return nil
		}
		cancelGroup(g)
		return nil
	})
}

func cancelGroup(g *Group) {
	g.Status = "cancelling"
	g.ParentWait = nil
	for _, m := range g.Members {
		v := current(m)
		if active(v) {
			v.CancelRequested = true
			if v.State == "queued" || v.State == "waiting" {
				v.State = "cancelled"
				publish(g, m, v)
			}
		}
		m.Mail = nil
	}
	settle(g)
}

// Compact drops bulky history from a terminal group.
func (r *Runtime) Compact(ctx context.Context, s Scope) error {
	return r.Store.Change(ctx, s, func(g *Group) error {
		if g.Status != "completed" && g.Status != "cancelled" {
			return nil
		}
		compactClosed(g)
		return nil
	})
}

func closeForHistoryLimit(g *Group) {
	if g.Status == "active" {
		cancelGroup(g)
	}
}

// compactedResult keeps FINAL_ANSWER envelopes decodable after compaction.
const compactedResult = `{"result":{"error":"collaboration history limit reached; ` +
	`the complete result is in the child conversation"}}`

// compactClosed drops bulky history from a group that no longer runs model
// turns. Child conversations retain the complete transcripts.
func compactClosed(g *Group) {
	g.ParentWait = nil
	for _, m := range g.Members {
		m.Mail = nil
		for _, v := range m.Runs {
			v.Result.Answer, v.Result.Evidence = "", ""
			v.Query = truncate(v.Query, 2000)
		}
	}
	for i := range g.Events {
		e := &g.Events[i]
		if len(e.Content) <= 4096 {
			continue
		}
		if strings.HasPrefix(e.Content, `{"result"`) {
			e.Content = compactedResult
		} else {
			e.Content = truncate(e.Content, 2000)
		}
	}
}

func settle(g *Group) {
	if g.Status != "cancelling" || g.ParentClosePending {
		return
	}
	for _, m := range g.Members {
		if active(current(m)) {
			return
		}
		for _, v := range m.Runs {
			if v.PromptID != "" {
				return
			}
		}
	}
	g.Status = "cancelled"
}

// Finish uses the last sequence actually observed by the parent, not a cursor
// advanced by a background delivery worker. A racing result prevents completion.
func (r *Runtime) Finish(ctx context.Context, s Scope, observed int64) (bool, error) {
	finished := false
	err := r.Store.Change(ctx, s, func(g *Group) error {
		finished = false
		if g.Status != "active" {
			return ErrClosed
		}
		if g.NextSequence != observed {
			return nil
		}
		for _, m := range g.Members {
			if active(current(m)) {
				return nil
			}
			for _, v := range m.Runs {
				if v.CancelRequested && v.PromptID != "" {
					return nil
				}
			}
		}
		g.Status = "completed"
		g.ParentWait = nil
		finished = true
		return nil
	})
	return finished, err
}

// Wait waits for an event newer than the supplied mailbox sequence.
func (r *Runtime) Wait(ctx context.Context, s Scope, after int64, d time.Duration) (bool, error) {
	deadline := time.NewTimer(d)
	defer deadline.Stop()
	tick := time.NewTicker(r.Poll)
	defer tick.Stop()
	for {
		g, err := r.Store.Read(ctx, s)
		if err != nil {
			return false, err
		}
		if g.Status != "active" {
			return false, ErrClosed
		}
		for _, evt := range g.Events {
			if evt.Sequence > after {
				return true, nil
			}
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-deadline.C:
			return false, nil
		case <-tick.C:
		}
	}
}

// Inbox retains mail for snapshot reconstruction, but durably acknowledges its
// delivery before execution. A panic or failed approval continuation must never
// turn already delivered guidance into an automatic new execution.
func (r *Runtime) Inbox(ctx context.Context, w Work, after int64) ([]Event, error) {
	g, err := r.Store.Read(ctx, w.Scope)
	if err != nil {
		return nil, err
	}
	m, err := member(g, w.Member.ID)
	if err != nil {
		return nil, err
	}
	hasMail := false
	for _, e := range m.Mail {
		if e.Sequence > after {
			hasMail = true
			break
		}
	}
	if !hasMail {
		return nil, nil
	}
	var out []Event
	err = r.Store.Change(ctx, w.Scope, func(g *Group) error {
		out = nil
		m, err := member(g, w.Member.ID)
		if err != nil {
			return err
		}
		v := current(m)
		if v.ID != w.Run.ID || v.Owner != w.Run.Owner || v.State != "running" || time.Now().After(v.Lease) {
			return ErrConflict
		}
		if g.Status != "active" || v.CancelRequested {
			return ErrClosed
		}
		for _, e := range m.Mail {
			if e.Sequence > after {
				out = append(out, e)
				v.DeliveredSequence = max(v.DeliveredSequence, e.Sequence)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Start starts the scheduling and recovery loop once.
func (r *Runtime) Start(ctx context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancel != nil {
		return
	}
	ctx, r.cancel = context.WithCancel(ctx)
	r.done = make(chan struct{})
	go func() {
		defer close(r.done)
		tick := time.NewTicker(r.Poll)
		defer tick.Stop()
		var lastPrune time.Time
		for {
			r.Tick(ctx)
			if r.Prune != nil && r.PruneInterval > 0 && time.Since(lastPrune) >= r.PruneInterval {
				lastPrune = time.Now()
				if err := r.Prune(ctx); err != nil && ctx.Err() == nil {
					logger.Warnf(ctx, "Collaboration prune failed: %v", err)
				}
			}
			select {
			case <-ctx.Done():
				r.workers.Wait()
				return
			case <-tick.C:
			}
		}
	}()
}

// Stop cancels scheduling and waits for workers to finish.
func (r *Runtime) Stop(ctx context.Context) error {
	r.mu.Lock()
	cancel, done := r.cancel, r.done
	r.mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

// Tick is also the restart recovery path. Never replay an uncertain running
// turn: expire its lease into needs_attention. Queued work is safe to claim.
func (r *Runtime) Tick(ctx context.Context) {
	scopes, err := r.Store.Pending(ctx)
	if err != nil {
		return
	}
	for _, s := range scopes {
		if ctx.Err() != nil {
			return
		}
		if r.Reconcile != nil {
			if err := r.Reconcile(ctx, s); err != nil {
				logger.Warnf(ctx, "Collaboration %s reconciliation failed: %v", s.ID, err)
				continue
			}
		}
		r.wakeParent(ctx, s)
		capacity := -1
		if r.MaxRunning > 0 {
			capacity = max(r.MaxRunning-int(r.running.Load()), 0)
		}
		var work []Work
		err = r.Store.Change(ctx, s, func(g *Group) error {
			work = nil
			now := time.Now()
			running := 0
			if now.After(g.Deadline) && g.Status == "active" {
				g.Status = "cancelling"
				g.ParentClosePending = r.WakeParent != nil && g.ParentMessageID != ""
				g.ParentWait = nil
				for _, m := range g.Members {
					current(m).CancelRequested = true
				}
			}
			for _, m := range g.Members {
				v := current(m)
				if v.State == "running" && now.After(v.Lease) {
					v.State = "needs_attention"
					v.Result.Error = "executor lease expired; execution was not replayed"
					publish(g, m, v)
				}
				if v.State == "running" {
					running++
				}
			}
			for _, m := range g.Members {
				v := current(m)
				if v.State == "waiting" && (g.Status != "active" || v.CancelRequested) {
					v.State = "cancelled"
					publish(g, m, v)
				}
				if v.State != "queued" {
					continue
				}
				if g.Status != "active" || v.CancelRequested {
					v.State = "cancelled"
					publish(g, m, v)
					continue
				}
				if running >= r.Concurrency || len(work) == capacity {
					continue
				}
				v.State = "running"
				v.Owner = uuid.NewString()
				v.Lease = now.Add(r.Lease)
				touchDeadline(g, now)
				v.Attempt++
				running++
				copyMember := *m
				copyMember.Runs = nil
				copyMember.Mail = nil
				work = append(work, Work{s, copyMember, *v})
			}
			settle(g)
			return nil
		})
		if err != nil {
			continue
		}
		for _, w := range work {
			r.workers.Add(1)
			r.running.Add(1)
			go func() {
				defer r.workers.Done()
				defer r.running.Add(-1)
				r.execute(ctx, w)
			}()
		}
	}
}

func (r *Runtime) execute(serverCtx context.Context, w Work) {
	ctx, cancel := context.WithCancel(serverCtx)
	defer cancel()
	heartbeatDone := make(chan struct{})
	stopHeartbeat := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		tick := time.NewTicker(r.heartbeatInterval())
		defer tick.Stop()
		lease := w.Run.Lease
		for {
			select {
			case <-stopHeartbeat:
				return
			case <-ctx.Done():
				return
			case <-tick.C:
				stop := false
				err := r.Store.Change(ctx, w.Scope, func(g *Group) error {
					m, e := member(g, w.Member.ID)
					if e != nil {
						return e
					}
					v := current(m)
					if v.ID != w.Run.ID || v.Owner != w.Run.Owner || v.State != "running" || time.Now().After(v.Lease) {
						return ErrConflict
					}
					if g.Status != "active" || v.CancelRequested {
						stop = true
					}
					now := time.Now()
					if r.renewalDue(v.Lease, now) {
						v.Lease = now.Add(r.Lease)
						touchDeadline(g, now)
					}
					lease = v.Lease
					return nil
				})
				// A transient store error is retried while the lease still fences
				// this owner; fencing errors and cancellation stop the run.
				if err != nil && !isFenceError(err) && time.Now().Before(lease) && ctx.Err() == nil {
					continue
				}
				if err != nil || stop {
					cancel()
					return
				}
			}
		}
	}()
	result, err := func() (out Result, err error) {
		defer func() {
			if p := recover(); p != nil {
				err = fmt.Errorf("agent executor panicked: %v", p)
			}
		}()
		if r.Run == nil {
			return out, errors.New("executor is unavailable")
		}
		return r.Run(ctx, w)
	}()
	close(stopHeartbeat)
	<-heartbeatDone
	if serverCtx.Err() != nil {
		return
	} // uncertain in-flight work is fenced by its lease
	finishCtx, finishCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer finishCancel()
	commitErr := r.Store.Change(finishCtx, w.Scope, func(g *Group) error {
		m, e := member(g, w.Member.ID)
		if e != nil {
			return e
		}
		v := current(m)
		if v.ID != w.Run.ID || v.Owner != w.Run.Owner || v.State != "running" || time.Now().After(v.Lease) {
			return ErrConflict
		}
		if result.Suspended && err == nil && g.Status == "active" && !v.CancelRequested && v.PromptID != "" {
			v.State = "waiting"
			v.Owner = ""
			touchDeadline(g, time.Now())
			publishPromptState(g, m, v, "waiting")
			return nil
		}
		result.ConsumedSequence = max(result.ConsumedSequence, v.DeliveredSequence)
		v.Result = result
		v.Result.Error = truncate(v.Result.Error, 1000)
		v.State = "succeeded"
		if result.Truncated {
			v.State = "failed"
			v.Result.Error = "Child answer reached its output limit; " +
				"the attached answer is partial, not a complete deliverable."
		}
		if err != nil {
			v.State = "failed"
			v.Result.Error = truncate(err.Error(), 1000)
		}
		if v.CancelRequested || g.Status != "active" {
			v.State = "interrupted"
			if g.Status != "active" {
				v.State = "cancelled"
			}
		}
		publish(g, m, v)
		// The published event is the only reader of the answer; the complete text
		// also stays in the child conversation. Do not store it twice.
		v.Result.Answer = ""
		// A followup accepted at the finishing boundary must have a consumer.
		var followups []string
		for _, mail := range m.Mail {
			if mail.Kind == "followup" && mail.RunID == v.ID &&
				mail.Sequence > result.ConsumedSequence {
				followups = append(followups, mail.Content)
			}
		}
		if len(followups) > 0 && g.Status == "active" && !v.CancelRequested && len(m.Runs) < 8 {
			m.Runs = append(m.Runs, &Run{ID: uuid.NewString(), State: "queued", Query: strings.Join(followups, "\n\n")})
		}
		// Plain messages remain available to subsequent turns. Followup messages
		// have now been converted into work and must not create another turn.
		kept := m.Mail[:0]
		for _, mail := range m.Mail {
			if mail.Kind != "followup" && mail.Sequence > result.ConsumedSequence {
				kept = append(kept, mail)
			}
		}
		m.Mail = kept
		settle(g)
		return nil
	})
	if commitErr != nil {
		logger.Warnf(finishCtx, "Collaboration %s run %s result commit failed: %v", w.Scope.ID, w.Run.ID, commitErr)
	}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "\n[truncated; full result is in the child conversation]"
	}
	return s
}

// CheckOwner validates that a child execution still owns its live lease.
func (r *Runtime) CheckOwner(ctx context.Context, w Work) error {
	g, err := r.Store.Read(ctx, w.Scope)
	if err != nil {
		return err
	}
	m, err := member(g, w.Member.ID)
	if err != nil {
		return err
	}
	v := current(m)
	if g.Status != "active" || v.State != "running" || v.CancelRequested || v.ID != w.Run.ID ||
		v.Owner != w.Run.Owner ||
		time.Now().After(v.Lease) {
		return ErrConflict
	}
	return nil
}

func currentTurnMemberCount(g *Group) int {
	count := 0
	for _, m := range g.Members {
		if !m.Inherited {
			count++
		}
	}
	return count
}
