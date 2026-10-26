package service

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// backgroundKeepAliveTimeout bounds one provider refresh.
const backgroundKeepAliveTimeout = 10 * time.Second

// backgroundKeepAlivePruneAt is how many tracked sessions trigger dropping
// entries whose refresh is already due.
const backgroundKeepAlivePruneAt = 1024

// sessionKeepAliveTarget is the sandbox side BackgroundKeepAliver drives.
// *PinnedSessionSandbox implements it.
type sessionKeepAliveTarget interface {
	KeepAliveSession(ctx context.Context, sessionID string) (time.Duration, error)
}

// BackgroundKeepAliver keeps the sandbox of a session with live background
// work from being reclaimed by its provider. Callers may invoke it on every
// poll; it reaches the provider only once the interval the last successful
// refresh reported has passed.
type BackgroundKeepAliver struct {
	target sessionKeepAliveTarget
	now    func() time.Time

	mu   sync.Mutex
	next map[interfaces.SessionRef]time.Time
}

var _ interfaces.SessionKeepAliver = (*BackgroundKeepAliver)(nil)

// NewBackgroundKeepAliver returns a keep-aliver over the session's pinned sandbox.
func NewBackgroundKeepAliver(pinned *PinnedSessionSandbox) *BackgroundKeepAliver {
	return newBackgroundKeepAliver(pinned, time.Now)
}

func newBackgroundKeepAliver(target sessionKeepAliveTarget, now func() time.Time) *BackgroundKeepAliver {
	return &BackgroundKeepAliver{
		target: target,
		now:    now,
		next:   make(map[interfaces.SessionRef]time.Time),
	}
}

// KeepAlive implements interfaces.SessionKeepAliver.
func (k *BackgroundKeepAliver) KeepAlive(ctx context.Context, s interfaces.SessionRef) error {
	if k == nil || k.target == nil || s.TenantID == 0 || strings.TrimSpace(s.SessionID) == "" {
		return nil
	}
	now := k.now()
	k.mu.Lock()
	due := k.next[s]
	k.mu.Unlock()
	if now.Before(due) {
		return nil
	}

	// Pollers have no request tenant. The pin lookup reads the execution
	// tenant; the binding key reads the sandbox tenant. Both are the owner.
	ctx = types.WithSandboxTenantID(types.WithExecutionTenant(ctx, s.TenantID), s.TenantID)
	ctx, cancel := context.WithTimeout(ctx, backgroundKeepAliveTimeout)
	defer cancel()
	interval, err := k.target.KeepAliveSession(ctx, s.SessionID)
	if err != nil {
		return err
	}

	k.mu.Lock()
	defer k.mu.Unlock()
	if interval > 0 {
		k.next[s] = now.Add(interval)
	} else {
		delete(k.next, s)
	}
	if len(k.next) >= backgroundKeepAlivePruneAt {
		for ref, at := range k.next {
			if !now.Before(at) {
				delete(k.next, ref)
			}
		}
	}
	return nil
}
