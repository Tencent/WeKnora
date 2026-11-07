// Package background holds the shared background-work parts: in-memory
// implementations, the lifecycle-listener registry and the lifecycle helpers.
// It must not branch on activity kind.
package background

import (
	"context"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// Clock abstracts time so tests can move it.
type Clock interface {
	Now() time.Time
}

// SystemClock is the real wall clock.
type SystemClock struct{}

// Now returns time.Now().
func (SystemClock) Now() time.Time { return time.Now() }

type memoryActivity struct {
	item     interfaces.BackgroundActivity
	deadline time.Time
}

// MemoryActivityRegistry is the single-process ActivityRegistry used by Lite.
type MemoryActivityRegistry struct {
	clock Clock
	mu    sync.Mutex
	items map[interfaces.SessionRef]map[string]memoryActivity
}

var _ interfaces.ActivityRegistry = (*MemoryActivityRegistry)(nil)

// NewMemoryActivityRegistry returns an empty registry. A nil clock means SystemClock.
func NewMemoryActivityRegistry(clock Clock) *MemoryActivityRegistry {
	if clock == nil {
		clock = SystemClock{}
	}
	return &MemoryActivityRegistry{
		clock: clock,
		items: make(map[interfaces.SessionRef]map[string]memoryActivity),
	}
}

// liveLocked drops expired items of s and returns what remains. Caller holds mu.
func (r *MemoryActivityRegistry) liveLocked(s interfaces.SessionRef, now time.Time) map[string]memoryActivity {
	m := r.items[s]
	for id, it := range m {
		if !now.Before(it.deadline) {
			delete(m, id)
		}
	}
	if len(m) == 0 {
		delete(r.items, s)
		return nil
	}
	return m
}

// Add implements interfaces.ActivityRegistry.
func (r *MemoryActivityRegistry) Add(
	_ context.Context, s interfaces.SessionRef, a interfaces.BackgroundActivity, lease time.Duration, kindCap int,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.clock.Now()
	m := r.liveLocked(s, now)
	if existing, ok := m[a.ID]; ok {
		existing.deadline = now.Add(lease)
		m[a.ID] = existing
		return nil
	}
	if kindCap > 0 {
		count := 0
		for _, it := range m {
			if it.item.Kind == a.Kind {
				count++
			}
		}
		if count >= kindCap {
			return interfaces.ErrActivityLimit
		}
	}
	if m == nil {
		m = make(map[string]memoryActivity)
		r.items[s] = m
	}
	m[a.ID] = memoryActivity{item: a, deadline: now.Add(lease)}
	return nil
}

// Renew implements interfaces.ActivityRegistry.
func (r *MemoryActivityRegistry) Renew(
	_ context.Context, s interfaces.SessionRef, id string, lease time.Duration,
) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.clock.Now()
	m := r.liveLocked(s, now)
	it, ok := m[id]
	if !ok {
		return false, nil
	}
	it.deadline = now.Add(lease)
	m[id] = it
	return true, nil
}

// Remove implements interfaces.ActivityRegistry.
func (r *MemoryActivityRegistry) Remove(_ context.Context, s interfaces.SessionRef, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if m := r.items[s]; m != nil {
		delete(m, id)
		if len(m) == 0 {
			delete(r.items, s)
		}
	}
	return nil
}

// List implements interfaces.ActivityRegistry.
func (r *MemoryActivityRegistry) List(
	_ context.Context, s interfaces.SessionRef,
) ([]interfaces.BackgroundActivity, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	m := r.liveLocked(s, r.clock.Now())
	out := make([]interfaces.BackgroundActivity, 0, len(m))
	for _, it := range m {
		out = append(out, it.item)
	}
	return out, nil
}
