package collaboration

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// ParentWait is a replay-safe checkpoint. It exists only while the engine is
// waiting, never while a model request or side-effecting tool is in flight.
type ParentWait struct {
	Snapshot []byte
	After    int64
	Until    time.Time
}

// SaveWait persists a replay-safe checkpoint for the current parent execution.
func (b *Binding) SaveWait(ctx context.Context, snapshot []byte, until time.Time) error {
	if b.Child != nil || b.ParentOwner == "" {
		return nil
	}
	return b.Runtime.Store.Change(b.Context(ctx), b.Scope, func(g *Group) error {
		if g.Status != "active" {
			return ErrClosed
		}
		g.ParentWait = &ParentWait{Snapshot: snapshot, After: b.Seen, Until: until}
		return nil
	})
}

// ClearWait removes the owned parent checkpoint after the engine resumes.
func (b *Binding) ClearWait(ctx context.Context) error {
	if b.Child != nil || b.ParentOwner == "" {
		return nil
	}
	return b.Runtime.Store.Change(b.Context(ctx), b.Scope, func(g *Group) error {
		if g.Status != "active" {
			return ErrClosed
		}
		g.ParentWait = nil
		return nil
	})
}

func (r *Runtime) wakeParent(ctx context.Context, scope Scope) {
	if r.WakeParent == nil {
		return
	}
	// Check durable input only for an abandoned safe checkpoint. The CAS below
	// rechecks ownership and parent message, so a concurrent takeover wins.
	inputReady := false
	inputMessage := ""
	if r.ParentInput != nil {
		g, err := r.Store.Read(ctx, scope)
		if err != nil {
			return
		}
		if g.Status == "active" && g.ParentWait != nil && (g.ParentOwner == "" || time.Now().After(g.ParentLease)) {
			inputMessage = g.ParentMessageID
			inputReady, err = r.ParentInput(ctx, scope, inputMessage)
			if err != nil {
				return
			}
		}
	}
	var wait *ParentWait
	var closing bool
	owner := uuid.NewString()
	err := r.Store.Change(ctx, scope, func(g *Group) error {
		wait = nil
		closing = false
		now := time.Now()
		if (g.Status != "active" && !g.ParentClosePending) || (g.ParentOwner != "" && now.Before(g.ParentLease)) {
			return nil
		}
		if g.ParentClosePending || !now.Before(g.Deadline) || g.ParentWait == nil {
			if g.ParentMessageID == "" || (g.ParentWait == nil && !g.ParentClosePending && g.ParentOwner == "") {
				return nil
			}
			// Fence all execution before closing the transcript. Keep this outbox
			// pending across failures/restarts until WakeParent acknowledges delivery.
			closing = true
			g.ParentClosePending = true
			g.Status = "cancelling"
			g.ParentWait = nil
			for _, m := range g.Members {
				if active(current(m)) {
					current(m).CancelRequested = true
				}
			}
			wait = &ParentWait{}
			g.ParentOwner, g.ParentLease = owner, now.Add(r.Lease)
			return nil
		}
		ready := !now.Before(g.ParentWait.Until) || (inputReady && g.ParentMessageID == inputMessage)
		for _, e := range g.Events {
			ready = ready || e.Sequence > g.ParentWait.After
		}
		if !ready {
			return nil
		}
		wait = g.ParentWait
		g.ParentOwner, g.ParentLease = owner, now.Add(r.Lease)
		return nil
	})
	if err != nil || wait == nil {
		return
	}
	r.workers.Add(1)
	go func() {
		defer r.workers.Done()
		b := &Binding{
			Runtime:     r,
			Scope:       scope,
			ParentOwner: owner,
			Closing:     closing,
			WaitUntil:   wait.Until,
			Seen:        wait.After,
		}
		runCtx, stop := b.Start(ctx)
		defer stop()
		err := r.WakeParent(WithBinding(runCtx, b), b, wait.Snapshot)
		if closing {
			if err == nil {
				// A failed acknowledgement is safe to retry: only transcript/stream
				// finalization can run for ParentClosePending, never the saved execution.
				_ = r.Store.Change(b.Context(context.WithoutCancel(ctx)), scope, func(g *Group) error {
					g.ParentClosePending = false
					settle(g)
					return nil
				})
			}
			return
		}
		if err != nil && ctx.Err() == nil {
			_ = r.Cancel(b.Context(context.WithoutCancel(ctx)), scope)
		}
	}()
}

// SetRunMessages and AwaitPrompt are fenced by the same child execution lease
// as tool execution. Prompt ownership is registered before it reaches the UI.
func (r *Runtime) SetRunMessages(ctx context.Context, w Work, userID, messageID string) error {
	return r.changeWork(
		ctx,
		w,
		func(_ *Group, v *Run) error { v.UserMessageID = userID; v.MessageID = messageID; return nil },
	)
}

// AwaitPrompt registers a prompt under the current child execution lease.
func (r *Runtime) AwaitPrompt(ctx context.Context, w Work, prompt PromptState) error {
	return r.changeWork(ctx, w, func(_ *Group, v *Run) error { v.PromptID, v.Prompt = prompt.ID, &prompt; return nil })
}

func (r *Runtime) changeWork(ctx context.Context, w Work, fn func(*Group, *Run) error) error {
	return r.Store.Change(ctx, w.Scope, func(g *Group) error {
		m, err := member(g, w.Member.ID)
		if err != nil {
			return err
		}
		v := current(m)
		if g.Status != "active" || v.ID != w.Run.ID || v.State != "running" || v.Owner != w.Run.Owner ||
			v.CancelRequested ||
			!time.Now().Before(v.Lease) {
			return ErrConflict
		}
		return fn(g, v)
	})
}

// ReconcilePrompt is called with broker state read outside the CAS callback.
// An executing/done prompt can never be replayed, even after a process dies.
func (r *Runtime) ReconcilePrompt(ctx context.Context, w Work, prompt PromptState) error {
	return r.Store.Change(ctx, w.Scope, func(g *Group) error {
		m, err := member(g, w.Member.ID)
		if err != nil {
			return err
		}
		v := current(m)
		if v.ID != w.Run.ID || v.PromptID != w.Run.PromptID || v.PromptID == "" || prompt.ID != v.PromptID {
			return nil
		}
		if v.State != "waiting" && (v.State != "running" || !time.Now().After(v.Lease)) {
			return nil
		}
		if g.Status != "active" || v.CancelRequested {
			v.State = "cancelled"
			publish(g, m, v)
			settle(g)
			return nil
		}
		v.Prompt = &prompt
		switch prompt.Status {
		case "pending", "suspended":
			if v.State != "waiting" {
				publishPromptState(g, m, v, "waiting")
			}
			v.State = "waiting"
			v.Owner = ""
		case "answered":
			v.State = "queued"
			v.Owner = ""
			publishPromptState(g, m, v, "resumed")
		default:
			closeChildRun(g, m, v, prompt.Status)
		}
		return nil
	})
}

// CloseChildPrompt settles the child run that owns a broker prompt. The prompt
// session is the child conversation, so callers locate the parent group first.
// A prompt that closed before the tool ran stays continuable; an in-flight or
// failed execution stays needs_attention and cannot be replayed.
func (r *Runtime) CloseChildPrompt(
	ctx context.Context, scope Scope, sessionID, messageID, promptID, status string,
) error {
	if sessionID == "" || (messageID == "" && promptID == "") {
		return nil
	}
	return r.Store.Change(ctx, scope, func(g *Group) error {
		for _, m := range g.Members {
			if m.SessionID != sessionID {
				continue
			}
			for _, v := range m.Runs {
				if v.PromptID != promptID && (messageID == "" || v.MessageID != messageID) {
					continue
				}
				if !active(v) && v.PromptID == "" && v.State != "needs_attention" {
					return nil
				}
				closeChildRun(g, m, v, status)
				return nil
			}
		}
		return nil
	})
}

func closeChildRun(g *Group, m *Member, v *Run, status string) {
	if promptClosedBeforeExecution(status, v.State) {
		if v.State != "interrupted" {
			v.State = "interrupted"
			v.Owner = ""
			v.CancelRequested = false
			v.Result.Error = closedBeforeExecutionError
			publish(g, m, v)
		}
	} else if active(v) || v.State == "needs_attention" {
		if v.State != "needs_attention" {
			v.State = "needs_attention"
			v.Owner = ""
			v.Result.Error = uncertainExecutionError
			publish(g, m, v)
		}
	}
	v.PromptID = ""
	v.Prompt = nil
	settle(g)
}

// ClearCanceledPrompt removes a canceled run’s prompt after the broker closes it.
func (r *Runtime) ClearCanceledPrompt(ctx context.Context, w Work) error {
	return r.Store.Change(ctx, w.Scope, func(g *Group) error {
		m, err := member(g, w.Member.ID)
		if err != nil {
			return err
		}
		var v *Run
		for _, candidate := range m.Runs {
			if candidate.ID == w.Run.ID {
				v = candidate
				break
			}
		}
		if v == nil || v.PromptID != w.Run.PromptID || (g.Status == "active" && !v.CancelRequested) {
			return nil
		}
		v.PromptID = ""
		v.Prompt = nil
		settle(g)
		return nil
	})
}

// RecordProgress coalesces telemetry without adding mailbox events or waking a
// model on each tool. Old workers and canceled turns cannot publish late updates.
func (r *Runtime) RecordProgress(ctx context.Context, w Work, p Progress) error {
	p.ToolName = truncate(p.ToolName, 128)
	p.ToolCallID = truncate(p.ToolCallID, 128)
	p.UpdatedAt = time.Now()
	return r.changeWork(ctx, w, func(_ *Group, v *Run) error { v.Progress = &p; return nil })
}

// CheckParentFinalization authorizes only the claimed terminal delivery. It is
// deliberately separate from CheckParent, which rejects a closing group.
func (r *Runtime) CheckParentFinalization(ctx context.Context, scope Scope, owner string) error {
	g, err := r.Store.Read(ctx, scope)
	if err != nil {
		return err
	}
	if !g.ParentClosePending || g.Status != "cancelling" || g.ParentOwner != owner ||
		!time.Now().Before(g.ParentLease) {
		return ErrConflict
	}
	return nil
}
