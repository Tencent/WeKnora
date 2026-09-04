package workspaceevent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

type memEndpoints struct {
	rows []*types.TenantWebhookEndpoint
	err  error
}

func (m *memEndpoints) Create(context.Context, *types.TenantWebhookEndpoint) error { return nil }
func (m *memEndpoints) Update(context.Context, *types.TenantWebhookEndpoint) error { return nil }
func (m *memEndpoints) SoftDelete(context.Context, uint64, string) error           { return nil }
func (m *memEndpoints) GetByID(context.Context, uint64, string) (*types.TenantWebhookEndpoint, error) {
	return nil, errors.New("not found")
}
func (m *memEndpoints) GetByIDUnscoped(context.Context, string) (*types.TenantWebhookEndpoint, error) {
	return nil, errors.New("not found")
}
func (m *memEndpoints) ListByTenant(context.Context, uint64) ([]*types.TenantWebhookEndpoint, error) {
	return m.rows, m.err
}
func (m *memEndpoints) ListEnabledByTenant(context.Context, uint64) ([]*types.TenantWebhookEndpoint, error) {
	if m.err != nil {
		return nil, m.err
	}
	out := make([]*types.TenantWebhookEndpoint, 0, len(m.rows))
	for _, r := range m.rows {
		if r != nil && r.Enabled {
			out = append(out, r)
		}
	}
	return out, nil
}
func (m *memEndpoints) CountByTenant(context.Context, uint64) (int64, error) {
	return int64(len(m.rows)), nil
}
func (m *memEndpoints) FindByTenantURL(context.Context, uint64, string) (*types.TenantWebhookEndpoint, error) {
	return nil, nil
}

type sinkOutbox struct {
	inserts int
}

func (m *sinkOutbox) Insert(context.Context, *types.TenantWebhookOutbox) error {
	m.inserts++
	return nil
}
func (m *sinkOutbox) GetByEventID(context.Context, string) (*types.TenantWebhookOutbox, error) {
	return nil, errors.New("not found")
}
func (m *sinkOutbox) ListPending(context.Context, int) ([]*types.TenantWebhookOutbox, error) {
	return nil, nil
}
func (m *sinkOutbox) MarkProcessed(context.Context, string) error                 { return nil }
func (m *sinkOutbox) MarkFailed(context.Context, string, string) error             { return nil }
func (m *sinkOutbox) BumpAttempts(context.Context, string, string) error           { return nil }
func (m *sinkOutbox) DeleteProcessedBefore(context.Context, time.Time) (int64, error) {
	return 0, nil
}

func newTestRedis(t *testing.T) *redis.Client {
	t.Helper()
	mini := miniredis.RunT(t)
	return redis.NewClient(&redis.Options{Addr: mini.Addr(), MaxRetries: -1})
}

func TestSubscriptionIndexSkipsUnconfiguredTenant(t *testing.T) {
	rdb := newTestRedis(t)
	idx := NewSubscriptionIndex(rdb, &memEndpoints{})
	if idx.Subscribes(context.Background(), 42, types.EventKnowledgeCreated) {
		t.Fatal("empty tenant must not subscribe")
	}
	if idx.Subscribes(context.Background(), 42, types.EventKBCreated) {
		t.Fatal("negative cache must stay false")
	}
}

func TestSubscriptionIndexMatchesEnabledEvents(t *testing.T) {
	rdb := newTestRedis(t)
	ep := &memEndpoints{rows: []*types.TenantWebhookEndpoint{
		{
			ID: "e1", TenantID: 7, Enabled: true,
			Events: types.WebhookEvents{types.EventKnowledgeCreated, types.EventKBCreated},
		},
		{
			ID: "e2", TenantID: 7, Enabled: false,
			Events: types.WebhookEvents{types.EventKnowledgeDeleted},
		},
	}}
	idx := NewSubscriptionIndex(rdb, ep)
	ctx := context.Background()
	if !idx.Subscribes(ctx, 7, types.EventKnowledgeCreated) {
		t.Fatal("expected knowledge.created")
	}
	if !idx.Subscribes(ctx, 7, types.EventKBCreated) {
		t.Fatal("expected kb.created")
	}
	if idx.Subscribes(ctx, 7, types.EventKnowledgeDeleted) {
		t.Fatal("disabled endpoint types must not count")
	}
}

func TestSubscriptionIndexInvalidateAndWarm(t *testing.T) {
	rdb := newTestRedis(t)
	ep := &memEndpoints{}
	idx := NewSubscriptionIndex(rdb, ep)
	ctx := context.Background()
	if idx.Subscribes(ctx, 9, types.EventKnowledgeCreated) {
		t.Fatal("start empty")
	}
	ep.rows = []*types.TenantWebhookEndpoint{{
		ID: "e1", TenantID: 9, Enabled: true,
		Events: types.WebhookEvents{types.EventKnowledgeCreated},
	}}
	if idx.Subscribes(ctx, 9, types.EventKnowledgeCreated) {
		t.Fatal("negative cache should still block before invalidate")
	}
	if err := idx.Invalidate(ctx, 9); err != nil {
		t.Fatal(err)
	}
	idx.Warm(ctx, 9)
	if !idx.Subscribes(ctx, 9, types.EventKnowledgeCreated) {
		t.Fatal("after warm must subscribe")
	}
}

func TestSubscriptionIndexDBFallbackWithoutRedis(t *testing.T) {
	ep := &memEndpoints{rows: []*types.TenantWebhookEndpoint{{
		ID: "e1", TenantID: 3, Enabled: true,
		Events: types.WebhookEvents{types.EventKBDeleted},
	}}}
	idx := NewSubscriptionIndex(nil, ep)
	if !idx.Subscribes(context.Background(), 3, types.EventKBDeleted) {
		t.Fatal("lite/DB path must see subscription")
	}
	if idx.Subscribes(context.Background(), 3, types.EventKBCreated) {
		t.Fatal("unsubscribed type must be false")
	}
}

func TestSubscriptionIndexDBErrorFailsOpen(t *testing.T) {
	idx := NewSubscriptionIndex(nil, &memEndpoints{err: errors.New("db down")})
	if !idx.Subscribes(context.Background(), 1, types.EventKnowledgeCreated) {
		t.Fatal("DB error should allow emit (dispatcher still filters)")
	}
}

type gateIndex struct {
	allow map[string]bool
}

func (g *gateIndex) Subscribes(_ context.Context, _ uint64, eventType string) bool {
	return g.allow[eventType]
}
func (g *gateIndex) Invalidate(context.Context, uint64) error { return nil }
func (g *gateIndex) Warm(context.Context, uint64)             {}

func TestSinkEmitSkipsWhenNotSubscribed(t *testing.T) {
	out := &sinkOutbox{}
	sink := NewSink(out, &gateIndex{allow: map[string]bool{
		types.EventKBCreated: true,
	}})
	sink.Emit(context.Background(), types.WorkspaceEvent{
		Type: types.EventKnowledgeCreated, TenantID: 1,
		Data: types.WebhookKnowledgeData{Resource: "knowledge"},
	})
	if out.inserts != 0 {
		t.Fatalf("unsubscribed emit wrote outbox: %d", out.inserts)
	}
	sink.Emit(context.Background(), types.WorkspaceEvent{
		Type: types.EventKBCreated, TenantID: 1,
		Data: types.WebhookKBData{Resource: "knowledge_base"},
	})
	if out.inserts != 1 {
		t.Fatalf("subscribed emit inserts=%d", out.inserts)
	}
}
