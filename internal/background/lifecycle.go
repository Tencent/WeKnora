package background

import (
	"context"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// Lifecycle combines the shared parts in the only orders that are safe.
// Features call these four methods and never the underlying interfaces.
type Lifecycle struct {
	Registry  interfaces.ActivityRegistry
	Inbox     interfaces.NoticeInbox
	KeepAlive interfaces.SessionKeepAliver // optional
	Events    interfaces.WorkEventEmitter  // optional
	Logf      func(ctx context.Context, format string, args ...any)
}

func (l *Lifecycle) logf(ctx context.Context, format string, args ...any) {
	if l.Logf != nil {
		l.Logf(ctx, format, args...)
	}
}

func (l *Lifecycle) keepAlive(ctx context.Context, s interfaces.SessionRef) {
	if l.KeepAlive == nil {
		return
	}
	if err := l.KeepAlive.KeepAlive(ctx, s); err != nil {
		l.logf(ctx, "[Background] keep-alive failed session=%s: %v", s.SessionID, err)
	}
}

// Begin registers a, then runs start. Registering first means lifecycle
// protection is in force the moment the work exists. If start fails the
// registration is removed. On success the sandbox is kept alive once at once,
// because nothing renewed it before the work began.
func (l *Lifecycle) Begin(
	ctx context.Context, s interfaces.SessionRef, a interfaces.BackgroundActivity,
	lease time.Duration, kindCap int, start func(context.Context) error,
) error {
	if err := l.Registry.Add(ctx, s, a, lease, kindCap); err != nil {
		return err
	}
	if err := start(ctx); err != nil {
		if rmErr := l.Registry.Remove(context.WithoutCancel(ctx), s, a.ID); rmErr != nil {
			l.logf(ctx, "[Background] remove after failed start session=%s id=%s: %v", s.SessionID, a.ID, rmErr)
		}
		return err
	}
	l.keepAlive(ctx, s)
	return nil
}

// Alive renews the registration and, when it still exists, keeps the sandbox
// alive. found=false means the item was removed or expired; the caller decides
// whether to re-register.
func (l *Lifecycle) Alive(
	ctx context.Context, s interfaces.SessionRef, id string, lease time.Duration,
) (bool, error) {
	found, err := l.Registry.Renew(ctx, s, id, lease)
	if err != nil {
		return false, err
	}
	if found {
		l.keepAlive(ctx, s)
	}
	return found, nil
}

// Finish records the completion notice, emits work_finished only if this call
// wrote the notice first, then removes the registration. Push comes first: in
// the other order there is a window where neither the registry nor the inbox
// knows the item. If Push fails the registration is kept so the item stays
// queryable and a later Finish can retry.
func (l *Lifecycle) Finish(
	ctx context.Context, s interfaces.SessionRef, n interfaces.BackgroundNotice,
	r interfaces.WorkResult, parentMessageID string,
) error {
	first, err := l.Inbox.Push(ctx, s, n)
	if err != nil {
		return fmt.Errorf("push background notice: %w", err)
	}
	if first && l.Events != nil {
		if err := l.Events.EmitWorkFinished(ctx, s, parentMessageID, r); err != nil {
			l.logf(ctx, "[Background] emit work_finished failed session=%s id=%s: %v", s.SessionID, n.ID, err)
		}
	}
	return l.Registry.Remove(ctx, s, n.ID)
}

// Discard removes the registration without a notice or event. Use it only when
// the result was already handed to the model directly.
func (l *Lifecycle) Discard(ctx context.Context, s interfaces.SessionRef, id string) error {
	return l.Registry.Remove(ctx, s, id)
}
