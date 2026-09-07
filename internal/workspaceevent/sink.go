package workspaceevent

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// Sink writes domain events to tenant_webhook_outbox when the tenant has an
// enabled endpoint subscribed to the event type. Dispatch is optional and
// attached after the asynq client exists so knowledge/member services can be
// constructed earlier in the DI graph.
type Sink struct {
	outbox        interfaces.WebhookOutboxRepository
	subscriptions interfaces.WebhookSubscriptionIndex
	mu            sync.RWMutex
	dispatcher    interfaces.WebhookDispatcher
}

func NewSink(
	outbox interfaces.WebhookOutboxRepository,
	subscriptions interfaces.WebhookSubscriptionIndex,
) *Sink {
	return &Sink{outbox: outbox, subscriptions: subscriptions}
}

func (s *Sink) SetDispatcher(d interfaces.WebhookDispatcher) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.dispatcher = d
	s.mu.Unlock()
}

func (s *Sink) dispatcherOrNil() interfaces.WebhookDispatcher {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.dispatcher
}

func (s *Sink) Emit(ctx context.Context, ev types.WorkspaceEvent) {
	if s == nil || s.outbox == nil {
		return
	}
	if ev.Type == "" || ev.TenantID == 0 {
		return
	}
	if s.subscriptions != nil && !s.subscriptions.Subscribes(ctx, ev.TenantID, ev.Type) {
		return
	}
	env, err := BuildEnvelope(ev, time.Now().UTC())
	if err != nil {
		logger.Errorf(ctx, "webhook_outbox_insert_failed type=%s tenant=%d marshal=%v", ev.Type, ev.TenantID, err)
		return
	}
	payload, err := json.Marshal(env)
	if err != nil {
		logger.Errorf(ctx, "webhook_outbox_insert_failed type=%s tenant=%d envelope=%v", ev.Type, ev.TenantID, err)
		return
	}
	row := &types.TenantWebhookOutbox{
		EventID:       env.ID,
		EventType:     env.Type,
		OwnerTenantID: env.TenantID,
		Payload:       payload,
		Status:        types.WebhookOutboxPending,
		CreatedAt:     time.Now().UTC(),
	}
	var lastErr error
	for i := 0; i < types.WebhookOutboxInsertRetries; i++ {
		lastErr = s.outbox.Insert(ctx, row)
		if lastErr == nil {
			break
		}
	}
	if lastErr != nil {
		logger.Errorf(ctx, "webhook_outbox_insert_failed type=%s tenant=%d event_id=%s err=%v",
			ev.Type, ev.TenantID, env.ID, lastErr)
		return
	}
	if d := s.dispatcherOrNil(); d != nil {
		if err := d.Dispatch(ctx, env.ID); err != nil {
			logger.Warnf(ctx, "webhook dispatch after emit failed event_id=%s err=%v", env.ID, err)
		}
	}
}
