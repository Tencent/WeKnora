package collaboration

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type parentOwnerKey struct{}

// AcquireParent attaches a new engine to an unfinished task after its prior
// engine lease expires. Completed groups never revive. Automatic recovery uses
// wakeParent and an owned safe checkpoint; this path also lets a new user turn
// take over unfinished work without replaying old tool calls.
func (r *Runtime) AcquireParent(ctx context.Context, requested Scope) (*Binding, error) {
	messageID := requested.ID
	var row Record
	err := r.Store.db.WithContext(ctx).
		Where("tenant_id = ? AND user_id = ? AND session_id = ? AND status = ?",
			requested.TenantID,
			requested.UserID,
			requested.SessionID,
			"active").
		Order("updated_at DESC").
		First(&row).
		Error
	recovered := false
	if err == nil {
		recovered = row.ID != requested.ID
		requested.ID = row.ID
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	} else {
		members, err := r.previousChildren(ctx, &requested)
		if err != nil {
			return nil, err
		}
		if err := r.Store.ensure(ctx, requested, members); err != nil {
			return nil, err
		}
	}
	owner := uuid.NewString()
	err = r.Store.Change(ctx, requested, func(g *Group) error {
		if g.Status != "active" {
			return ErrClosed
		}
		if g.ParentOwner != "" && time.Now().Before(g.ParentLease) {
			return errors.New("the parent task is already running")
		}
		g.ParentWait = nil
		g.ParentMessageID = messageID
		g.ParentOwner = owner
		g.ParentLease = time.Now().Add(r.Lease)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &Binding{Runtime: r, Scope: requested, ParentOwner: owner, Recovered: recovered}, nil
}

// Context adds the parent ownership fence to store operations.
func (b *Binding) Context(ctx context.Context) context.Context {
	if b.ParentOwner != "" {
		return context.WithValue(ctx, parentOwnerKey{}, b.ParentOwner)
	}
	return ctx
}

// Start renews the parent lease until the returned cleanup function is called.
func (b *Binding) Start(ctx context.Context) (context.Context, func()) {
	if b.ParentOwner == "" {
		return ctx, func() {}
	}
	ctx, cancel := context.WithCancel(b.Context(ctx))
	done := make(chan struct{})
	go func() {
		defer close(done)
		tick := time.NewTicker(b.Runtime.heartbeatInterval())
		defer tick.Stop()
		lease := time.Now().Add(b.Runtime.Lease)
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				completed := false
				err := b.Runtime.Store.Change(ctx, b.Scope, func(g *Group) error {
					completed = g.Status == "completed"
					if completed {
						return nil
					}
					closing := b.Closing && g.ParentClosePending && g.Status == "cancelling"
					if g.Status != "active" && !closing {
						return ErrClosed
					}
					now := time.Now()
					if b.Runtime.renewalDue(g.ParentLease, now) {
						g.ParentLease = now.Add(b.Runtime.Lease)
						touchDeadline(g, now)
					}
					lease = g.ParentLease
					return nil
				})
				if err == nil && completed {
					return
				} // normal completion must not cancel final event delivery
				if err != nil && !isFenceError(err) && time.Now().Before(lease) && ctx.Err() == nil {
					continue
				}
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()
	return ctx, func() { cancel(); <-done }
}

// CheckParent rejects expired, replaced or closed parent executions.
func (r *Runtime) CheckParent(ctx context.Context, scope Scope, owner string) error {
	g, err := r.Store.Read(ctx, scope)
	if err != nil {
		return err
	}
	if g.Status != "active" || g.ParentOwner != owner || time.Now().After(g.ParentLease) {
		return ErrConflict
	}
	return nil
}

// A new human turn may continue a completed child's conversation. Copy only
// idle instance identities into a fresh group: old events, approvals, leases and
// mail remain fenced in the old group and can never wake the new parent.
func (r *Runtime) previousChildren(ctx context.Context, scope *Scope) ([]*Member, error) {
	var row Record
	err := r.Store.db.WithContext(ctx).
		Where("tenant_id = ? AND user_id = ? AND session_id = ? AND id <> ?",
			scope.TenantID, scope.UserID, scope.SessionID, scope.ID).
		Order("updated_at DESC").First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	// The initial active-group lookup may have raced another caller's ensure.
	if row.Status == "active" {
		scope.ID = row.ID
		return nil, nil
	}
	// A cancelled/uncertain newer group is a barrier. Never reach backwards
	// past it to revive an older copy of the same child session.
	if row.Status != "completed" {
		return nil, nil
	}
	previous := *scope
	previous.ID = row.ID
	g, err := r.Store.Read(ctx, previous)
	if err != nil {
		return nil, err
	}
	// Concurrent new human turns must not clone this child session into two
	// independently runnable groups. The completed predecessor is a durable
	// CAS admission record; a crash before ensure is repaired by the next caller.
	nextID := scope.ID
	if err := r.Store.Change(ctx, previous, func(g *Group) error {
		if g.Status != "completed" {
			return ErrConflict
		}
		if g.SuccessorID == "" {
			g.SuccessorID = nextID
		}
		nextID = g.SuccessorID
		return nil
	}); err != nil {
		return nil, err
	}
	scope.ID = nextID
	var members []*Member
	for _, original := range g.Members {
		last := current(original)
		if last == nil || active(last) {
			continue
		}
		m, run := *original, *last
		m.Inherited, m.Mail = true, nil
		run.Inherited = true
		run.Owner, run.Lease, run.PromptID, run.Prompt = "", time.Time{}, "", nil
		m.Runs = []*Run{&run}
		members = append(members, &m)
	}
	return members, nil
}
