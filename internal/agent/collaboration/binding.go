package collaboration

import (
	"context"
	"errors"
	"time"
)

// Binding is private runtime authority, never constructed from model arguments.
// Parent event cursors advance only when the engine puts the event into a model
// input. wait_agent has its own cursor and cannot acknowledge model processing.
type Binding struct {
	ParentOwner string
	// Closing only authorizes transcript finalization, never model/tool execution.
	Closing    bool
	Recovered  bool
	Runtime    *Runtime
	Scope      Scope
	Child      *Work
	Seen       int64
	WaitCursor int64
	WaitUntil  time.Time
	// HasInput probes durable steer without consuming it; the engine owns delivery.
	HasInput func(context.Context) (bool, error)
}
type bindingKey struct{}

// WithBinding attaches runtime-owned collaboration authority to the context.
func WithBinding(ctx context.Context, b *Binding) context.Context {
	return context.WithValue(ctx, bindingKey{}, b)
}

// FromContext returns the collaboration authority associated with this execution.
func FromContext(ctx context.Context) *Binding { b, _ := ctx.Value(bindingKey{}).(*Binding); return b }

// Poll reads unseen mailbox events and advances the model-delivery cursor.
func (b *Binding) Poll(ctx context.Context) ([]Event, error) {
	if b.Child != nil {
		if err := b.Runtime.CheckOwner(ctx, *b.Child); err != nil {
			return nil, err
		}
		events, err := b.Runtime.Inbox(ctx, *b.Child, b.Seen)
		if err == nil {
			for _, e := range events {
				b.Seen = max(b.Seen, e.Sequence)
			}
		}
		return events, err
	}
	g, err := b.Runtime.Store.Read(ctx, b.Scope)
	if err != nil {
		return nil, err
	}
	if g.Status != "active" {
		return nil, ErrClosed
	}
	if b.ParentOwner != "" && (g.ParentOwner != b.ParentOwner || time.Now().After(g.ParentLease)) {
		return nil, ErrConflict
	}
	var out []Event
	if b.Recovered {
		out = append(
			out,
			Event{
				From: "runtime",
				Kind: "recovered",
				Content: "Recovered an unfinished delegation. Original task: " + g.RootQuery +
					". Review its durable results and current states before continuing. Uncertain " +
					"executions must be checked before repeating actions.",
			},
		)
		b.Recovered = false
	}
	for _, e := range g.Events {
		if e.Sequence > b.Seen {
			out = append(out, e)
		}
	}
	b.Seen = g.NextSequence
	return out, nil
}

// BeforeFinish either atomically closes the group, or delivers unseen results
// so the engine must call the model again. Polling always checks durable state
// first; events committed before waiting are not lost.
func (b *Binding) BeforeFinish(ctx context.Context) ([]Event, error) {
	if b.Child != nil {
		return b.Poll(ctx)
	}
	for {
		if b.HasInput != nil {
			ready, err := b.HasInput(ctx)
			if err != nil {
				return nil, err
			}
			if ready {
				return nil, ErrInput
			}
		}
		done, err := b.Runtime.Finish(b.Context(ctx), b.Scope, b.Seen)
		if err != nil {
			return nil, err
		}
		if done {
			return nil, nil
		}
		events, err := b.Poll(ctx)
		if err != nil {
			return nil, err
		}
		if len(events) > 0 {
			return events, nil
		}
		_, err = b.Wait(ctx, b.Seen, time.Second)
		if err != nil {
			return nil, err
		}
	}
}

// End owns cleanup, including unexpected engine exits and user cancellation.
// A durable prompt suspension keeps the group open for the resumed engine.
func (b *Binding) End(suspended bool) {
	if b.Child != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	g, err := b.Runtime.Store.Read(ctx, b.Scope)
	if err == nil && g.Status == "active" && g.ParentWait != nil {
		return
	} // retain lease until the old stream has settled
	if suspended {
		_ = b.Runtime.Store.Change(
			b.Context(ctx),
			b.Scope,
			func(g *Group) error { g.ParentOwner = ""; g.ParentLease = time.Time{}; return nil },
		)
		return
	}
	// A turn that never delegated has nothing to cancel. Completing it keeps
	// idle children from earlier turns addressable by the next human turn.
	if delegated, err := b.HasDelegatedWork(ctx); err == nil && !delegated {
		if done, err := b.Runtime.Finish(b.Context(ctx), b.Scope, b.Seen); err == nil && done {
			return
		}
	}
	_ = b.Runtime.Cancel(b.Context(ctx), b.Scope)
}

// MessageParent publishes an idempotent child message while its execution lease remains valid.
func (b *Binding) MessageParent(ctx context.Context, key, content string) error {
	if b.Child == nil {
		return errors.New("only child agents can address /root")
	}
	if key == "" || len(key) > 512 || len(content) == 0 || len(content) > 16000 {
		return errors.New("invalid message")
	}
	return b.Runtime.Store.Change(ctx, b.Scope, func(g *Group) error {
		if g.Requests[key] != "" {
			return nil
		}
		if g.Status != "active" {
			return ErrClosed
		}
		m, err := member(g, b.Child.Member.ID)
		if err != nil {
			return err
		}
		r := current(m)
		if r.ID != b.Child.Run.ID || r.Owner != b.Child.Run.Owner || r.State != "running" || r.CancelRequested ||
			time.Now().After(r.Lease) {
			return ErrConflict
		}
		if len(g.Requests) >= 512 {
			return errors.New("message budget exhausted")
		}
		if err := reserveInput(g, len(content)); err != nil {
			return err
		}
		g.NextSequence++
		g.Events = append(g.Events, Event{g.NextSequence, m.Name, r.ID, "message", content})
		g.Requests[key] = r.ID
		return nil
	})
}

// MemberView is the model-visible snapshot of a child and its current execution.
type MemberView struct {
	PreviousTurn    bool      `json:"previous_turn,omitempty"`
	Progress        *Progress `json:"progress,omitempty"`
	PromptID        string    `json:"prompt_id,omitempty"`
	ID              string    `json:"member_id"`
	Name            string    `json:"task_name"`
	AgentID         string    `json:"agent_id"`
	SessionID       string    `json:"session_id"`
	RunID           string    `json:"run_id"`
	Status          string    `json:"status"`
	CancelRequested bool      `json:"cancel_requested"`
}

// List lists child states, optionally filtered by task path.
func (b *Binding) List(ctx context.Context, prefix string) ([]MemberView, error) {
	g, err := b.Runtime.Store.Read(ctx, b.Scope)
	if err != nil {
		return nil, err
	}
	out := []MemberView{}
	for _, m := range g.Members {
		if prefix != "" && prefix != "/root" && prefix != m.Name && prefix != "/root/"+m.Name {
			continue
		}
		r := current(m)
		out = append(
			out,
			MemberView{
				PreviousTurn:    m.Inherited,
				ID:              m.ID,
				Name:            m.Name,
				AgentID:         m.AgentID,
				SessionID:       m.SessionID,
				RunID:           r.ID,
				Status:          r.State,
				CancelRequested: r.CancelRequested,
				Progress:        r.Progress,
				PromptID:        r.PromptID,
			},
		)
	}
	return out, nil
}

// ErrInput is a wake reason, never an execution failure. The engine drains the
// input through its ordinary persistence path before calling the model again.
var ErrInput = errors.New("wait interrupted by new user input")

// Wait waits for mailbox changes while allowing user steering to interrupt the wait.
func (b *Binding) Wait(ctx context.Context, after int64, duration time.Duration) (bool, error) {
	deadline := time.Now().Add(duration)
	for {
		if b.HasInput != nil {
			ready, err := b.HasInput(ctx)
			if err != nil {
				return false, err
			}
			if ready {
				return false, ErrInput
			}
		}
		remaining := max(time.Until(deadline), time.Duration(0))
		updated, err := b.Runtime.Wait(ctx, b.Scope, after, min(remaining, b.Runtime.Poll))
		if err != nil || updated {
			return updated, err
		}
		if !time.Now().Before(deadline) {
			return false, nil
		}
	}
}

// PendingAnswer prevents an optimistic final answer while delegated work or
// unseen results still need review. Approval visibility is owned by the UI.
func (b *Binding) PendingAnswer(ctx context.Context) (bool, error) {
	if b.Child != nil {
		return false, nil
	}
	g, err := b.Runtime.Store.Read(ctx, b.Scope)
	if err != nil {
		return false, err
	}
	for _, m := range g.Members {
		if active(current(m)) {
			return true, nil
		}
	}
	for _, e := range g.Events {
		if e.Sequence > b.Seen {
			return true, nil
		}
	}
	return false, nil
}

// HasDelegatedWork reports whether this execution owns delegated work whose
// failure must be propagated instead of replaced by a fallback answer. Every
// child does; a parent does only after it has started a child in this turn.
func (b *Binding) HasDelegatedWork(ctx context.Context) (bool, error) {
	if b.Child != nil {
		return true, nil
	}
	g, err := b.Runtime.Store.Read(ctx, b.Scope)
	if err != nil {
		return false, err
	}
	return currentTurnMemberCount(g) > 0 || g.NextSequence > 0, nil
}

// DeliveredEvents recovers input omitted by legacy parent snapshots without
// advancing the cursor or granting execution authority to a cancelled owner.
func (b *Binding) DeliveredEvents(ctx context.Context) ([]Event, error) {
	if err := b.Runtime.CheckParent(ctx, b.Scope, b.ParentOwner); err != nil {
		return nil, err
	}
	g, err := b.Runtime.Store.Read(ctx, b.Scope)
	if err != nil {
		return nil, err
	}
	var events []Event
	for _, event := range g.Events {
		if event.Sequence <= b.Seen {
			events = append(events, event)
		}
	}
	return events, nil
}
