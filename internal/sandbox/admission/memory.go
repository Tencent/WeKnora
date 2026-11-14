package admission

import (
	"context"
	"sync"
	"time"
)

// MemoryRegistry is a process-local Registry for tests and single-process
// deployments. User mode is never enabled without Redis, so multi-replica
// production never uses it. Leases never expire on their own; tests force
// loss with ExpireMember and LoseBarrier.
type MemoryRegistry struct {
	mu       sync.Mutex
	members  map[Scope]map[string]*lease
	barriers map[Scope]*barrier
}

// NewMemory builds an empty registry.
func NewMemory() *MemoryRegistry {
	return &MemoryRegistry{members: map[Scope]map[string]*lease{}, barriers: map[Scope]*barrier{}}
}

// Hold registers member until Release. The memory registry does not expire on its own.
func (r *MemoryRegistry) Hold(_ context.Context, scope Scope, member string, _ time.Duration) (Lease, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, closed := r.barriers[scope]; closed {
		return nil, ErrClosed
	}
	set := r.members[scope]
	if set == nil {
		set = map[string]*lease{}
		r.members[scope] = set
	}
	l := &lease{signal: newSignal(), member: member}
	l.release = func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.members[scope][member] == l {
			delete(r.members[scope], member)
		}
	}
	set[member] = l
	return l, nil
}

// ActiveExcept counts members of scope other than except.
func (r *MemoryRegistry) ActiveExcept(_ context.Context, scope Scope, except ...string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	skip := make(map[string]struct{}, len(except))
	for _, m := range except {
		skip[m] = struct{}{}
	}
	n := 0
	for m := range r.members[scope] {
		if _, ok := skip[m]; !ok {
			n++
		}
	}
	return n, nil
}

// Close stops new holds on scope until the barrier is released.
func (r *MemoryRegistry) Close(_ context.Context, scope Scope, _ time.Duration) (Barrier, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, closed := r.barriers[scope]; closed {
		return nil, ErrClosed
	}
	b := &barrier{signal: newSignal()}
	b.release = func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.barriers[scope] == b {
			delete(r.barriers, scope)
		}
	}
	r.barriers[scope] = b
	return b, nil
}

// ExpireScope marks every member of scope lost. Tests use it when the
// member token is chosen inside the call under test.
func (r *MemoryRegistry) ExpireScope(scope Scope) {
	r.mu.Lock()
	set := r.members[scope]
	delete(r.members, scope)
	lost := make([]*lease, 0, len(set))
	for _, l := range set {
		lost = append(lost, l)
	}
	r.mu.Unlock()
	for _, l := range lost {
		l.markLost()
	}
}

// ExpireMember simulates a lease that expired server-side.
func (r *MemoryRegistry) ExpireMember(scope Scope, member string) {
	r.mu.Lock()
	l := r.members[scope][member]
	delete(r.members[scope], member)
	r.mu.Unlock()
	if l != nil {
		l.markLost()
	}
}

// LoseBarrier simulates a barrier whose ownership could not be renewed.
func (r *MemoryRegistry) LoseBarrier(scope Scope) {
	r.mu.Lock()
	b := r.barriers[scope]
	delete(r.barriers, scope)
	r.mu.Unlock()
	if b != nil {
		b.markLost()
	}
}

var _ Registry = (*MemoryRegistry)(nil)
