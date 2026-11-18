package sandbox

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// MemoryActivityRegistry shares the binding store's background leases and lock,
// so single-process deployments also protect live work from rebuild and rewind.
type MemoryActivityRegistry struct {
	store *MemorySessionSandboxBindingStore
}

var _ interfaces.ActivityRegistry = (*MemoryActivityRegistry)(nil)

// NewMemoryActivityRegistry returns the activity registry of store.
func NewMemoryActivityRegistry(store *MemorySessionSandboxBindingStore) *MemoryActivityRegistry {
	return &MemoryActivityRegistry{store: store}
}

func memoryActivityKey(ctx context.Context, s interfaces.SessionRef) (SessionSandboxKey, error) {
	key := SessionSandboxKey{TenantID: s.TenantID, SessionID: s.SessionID}
	if err := key.Validate(); err != nil {
		return key, err
	}
	return key, ctx.Err()
}

// Add implements interfaces.ActivityRegistry.
func (r *MemoryActivityRegistry) Add(
	ctx context.Context, s interfaces.SessionRef, a interfaces.BackgroundActivity, lease time.Duration, kindCap int,
) error {
	key, err := memoryActivityKey(ctx, s)
	if err != nil {
		return err
	}
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	if _, held := r.store.rewinds[key]; held {
		return ErrSessionRewindLocked
	}
	return r.store.activity.Add(ctx, s, a, lease, kindCap)
}

// Renew implements interfaces.ActivityRegistry.
func (r *MemoryActivityRegistry) Renew(
	ctx context.Context, s interfaces.SessionRef, id string, lease time.Duration,
) (bool, error) {
	if _, err := memoryActivityKey(ctx, s); err != nil {
		return false, err
	}
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	return r.store.activity.Renew(ctx, s, id, lease)
}

// Remove implements interfaces.ActivityRegistry.
func (r *MemoryActivityRegistry) Remove(ctx context.Context, s interfaces.SessionRef, id string) error {
	if _, err := memoryActivityKey(ctx, s); err != nil {
		return err
	}
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	return r.store.activity.Remove(ctx, s, id)
}

// List implements interfaces.ActivityRegistry.
func (r *MemoryActivityRegistry) List(
	ctx context.Context, s interfaces.SessionRef,
) ([]interfaces.BackgroundActivity, error) {
	if _, err := memoryActivityKey(ctx, s); err != nil {
		return nil, err
	}
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	return r.store.activity.List(ctx, s)
}

// Caller holds s.mu, serializing the lease check with activity registration.
func (s *MemorySessionSandboxBindingStore) hasSessionActivityLocked(ctx context.Context, key SessionSandboxKey) bool {
	items, err := s.activity.List(ctx, interfaces.SessionRef{TenantID: key.TenantID, SessionID: key.SessionID})
	return err != nil || len(items) > 0
}
