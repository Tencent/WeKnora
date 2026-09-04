package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
)

var (
	_ interfaces.WebhookOutboxRepository   = (*memOutbox)(nil)
	_ interfaces.WebhookEndpointRepository = (*memEndpoints)(nil)
)

type memOutbox struct {
	rows map[string]*types.TenantWebhookOutbox
}

func (m *memOutbox) Insert(_ context.Context, row *types.TenantWebhookOutbox) error {
	if m.rows == nil {
		m.rows = map[string]*types.TenantWebhookOutbox{}
	}
	cp := *row
	m.rows[row.EventID] = &cp
	return nil
}
func (m *memOutbox) GetByEventID(_ context.Context, eventID string) (*types.TenantWebhookOutbox, error) {
	row, ok := m.rows[eventID]
	if !ok {
		return nil, errors.New("not found")
	}
	cp := *row
	return &cp, nil
}
func (m *memOutbox) ListPending(_ context.Context, _ int) ([]*types.TenantWebhookOutbox, error) {
	var out []*types.TenantWebhookOutbox
	for _, row := range m.rows {
		if row.Status == types.WebhookOutboxPending {
			cp := *row
			out = append(out, &cp)
		}
	}
	return out, nil
}
func (m *memOutbox) MarkProcessed(_ context.Context, eventID string) error {
	if row, ok := m.rows[eventID]; ok {
		row.Status = types.WebhookOutboxProcessed
	}
	return nil
}
func (m *memOutbox) MarkFailed(_ context.Context, eventID, lastError string) error {
	if row, ok := m.rows[eventID]; ok {
		row.Status = types.WebhookOutboxFailed
		row.LastError = lastError
	}
	return nil
}
func (m *memOutbox) BumpAttempts(_ context.Context, eventID, lastError string) error {
	if row, ok := m.rows[eventID]; ok {
		row.Attempts++
		row.LastError = lastError
	}
	return nil
}
func (m *memOutbox) DeleteProcessedBefore(_ context.Context, _ time.Time) (int64, error) {
	return 0, nil
}

type memEndpoints struct {
	enabled []*types.TenantWebhookEndpoint
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
	return nil, nil
}
func (m *memEndpoints) ListEnabledByTenant(context.Context, uint64) ([]*types.TenantWebhookEndpoint, error) {
	return m.enabled, nil
}
func (m *memEndpoints) CountByTenant(context.Context, uint64) (int64, error) { return 0, nil }
func (m *memEndpoints) FindByTenantURL(context.Context, uint64, string) (*types.TenantWebhookEndpoint, error) {
	return nil, nil
}

func TestDispatchZeroEndpointsMarksProcessed(t *testing.T) {
	outbox := &memOutbox{rows: map[string]*types.TenantWebhookOutbox{
		"evt_1": {
			EventID:       "evt_1",
			EventType:     types.EventKnowledgeCreated,
			OwnerTenantID: 42,
			Status:        types.WebhookOutboxPending,
		},
	}}
	d := NewWebhookDispatcher(outbox, &memEndpoints{}, nil, nil)
	if err := d.Dispatch(context.Background(), "evt_1"); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if outbox.rows["evt_1"].Status != types.WebhookOutboxProcessed {
		t.Fatalf("status = %s, want processed", outbox.rows["evt_1"].Status)
	}
}

func TestWebhookBusyIsNotSkipRetry(t *testing.T) {
	if errors.Is(ErrWebhookInFlightBusy, asynq.SkipRetry) {
		t.Fatal("in-flight busy must not be treated as SkipRetry")
	}
}
