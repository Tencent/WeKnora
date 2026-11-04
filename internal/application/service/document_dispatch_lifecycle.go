package service

import (
	"context"
	"sync"
)

type documentDispatchRun struct {
	cancel context.CancelFunc
}

// documentDispatchLifecycle owns active handlers independently of the transport,
// including Lite's background task contexts. Its zero value accepts work.
type documentDispatchLifecycle struct {
	mu      sync.Mutex
	stopped bool
	active  map[*documentDispatchRun]struct{}
	idle    chan struct{}
}

func (l *documentDispatchLifecycle) begin(ctx context.Context) (context.Context, func(), bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.stopped {
		return ctx, nil, false
	}
	ctx, cancel := context.WithCancel(ctx)
	run := &documentDispatchRun{cancel: cancel}
	if l.active == nil {
		l.active = make(map[*documentDispatchRun]struct{})
	}
	if len(l.active) == 0 {
		l.idle = make(chan struct{})
	}
	l.active[run] = struct{}{}
	return ctx, func() {
		cancel()
		l.mu.Lock()
		defer l.mu.Unlock()
		if _, exists := l.active[run]; exists {
			delete(l.active, run)
			if len(l.active) == 0 {
				close(l.idle)
			}
		}
	}, true
}

func (l *documentDispatchLifecycle) stopping() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.stopped
}

func (l *documentDispatchLifecycle) stop(ctx context.Context) error {
	l.mu.Lock()
	l.stopped = true
	cancels := make([]context.CancelFunc, 0, len(l.active))
	for run := range l.active {
		cancels = append(cancels, run.cancel)
	}
	idle := l.idle
	l.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	if idle == nil {
		return nil
	}
	// Waiting directly on the idle channel avoids a leaked waiter goroutine if
	// an external call ignores cancellation and the shutdown deadline expires.
	select {
	case <-idle:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
