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
	} else if err = r.Store.Ensure(ctx, requested); err != nil {
		return nil, err
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
		tick := time.NewTicker(min(b.Runtime.Poll, b.Runtime.Lease/3))
		defer tick.Stop()
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
					if g.Status != "active" {
						return ErrClosed
					}
					g.ParentLease = time.Now().Add(b.Runtime.Lease)
					return nil
				})
				if err == nil && completed {
					return
				} // normal completion must not cancel final event delivery
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
