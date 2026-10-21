package background

import (
	"context"
	"sync"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// Listeners is the ordered set of session lifecycle listeners. Feature
// listeners run in registration order; on delete the shared activity
// registry and inbox are cleared after all of them have run.
type Listeners struct {
	registry interfaces.ActivityRegistry
	inbox    interfaces.NoticeInbox
	logf     func(ctx context.Context, format string, args ...any)

	mu        sync.RWMutex
	listeners []interfaces.SessionLifecycleListener
}

// NewListeners returns an empty listener set. A nil logf discards warnings.
func NewListeners(
	registry interfaces.ActivityRegistry,
	inbox interfaces.NoticeInbox,
	logf func(ctx context.Context, format string, args ...any),
) *Listeners {
	if logf == nil {
		logf = func(context.Context, string, ...any) {}
	}
	return &Listeners{registry: registry, inbox: inbox, logf: logf}
}

// Register appends a listener.
func (l *Listeners) Register(listener interfaces.SessionLifecycleListener) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.listeners = append(l.listeners, listener)
}

func (l *Listeners) snapshot() []interfaces.SessionLifecycleListener {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return append([]interfaces.SessionLifecycleListener(nil), l.listeners...)
}

// NotifyStop calls OnStop on every listener. Errors are logged, never returned.
func (l *Listeners) NotifyStop(ctx context.Context, s interfaces.SessionRef) {
	for _, listener := range l.snapshot() {
		if err := listener.OnStop(ctx, s); err != nil {
			l.logf(ctx, "[Background] OnStop listener failed session=%s: %v", s.SessionID, err)
		}
	}
}

// NotifyDelete calls OnDelete on every listener, then clears the session's
// activity items and notices. Errors are logged, never returned.
func (l *Listeners) NotifyDelete(ctx context.Context, s interfaces.SessionRef) {
	for _, listener := range l.snapshot() {
		if err := listener.OnDelete(ctx, s); err != nil {
			l.logf(ctx, "[Background] OnDelete listener failed session=%s: %v", s.SessionID, err)
		}
	}
	if l.registry != nil {
		items, err := l.registry.List(ctx, s)
		if err != nil {
			l.logf(ctx, "[Background] list activity on delete failed session=%s: %v", s.SessionID, err)
		}
		for _, it := range items {
			if err := l.registry.Remove(ctx, s, it.ID); err != nil {
				l.logf(ctx, "[Background] remove activity on delete failed session=%s id=%s: %v",
					s.SessionID, it.ID, err)
			}
		}
	}
	if l.inbox != nil {
		if err := l.inbox.Clear(ctx, s); err != nil {
			l.logf(ctx, "[Background] clear inbox on delete failed session=%s: %v", s.SessionID, err)
		}
	}
}
