package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

type stubWebhookEndpoints struct {
	count int64
	row   *types.TenantWebhookEndpoint
}

func (s *stubWebhookEndpoints) Create(_ context.Context, ep *types.TenantWebhookEndpoint) error {
	cp := *ep
	s.row = &cp
	return nil
}
func (s *stubWebhookEndpoints) Update(_ context.Context, ep *types.TenantWebhookEndpoint) error {
	cp := *ep
	s.row = &cp
	return nil
}
func (s *stubWebhookEndpoints) SoftDelete(context.Context, uint64, string) error { return nil }
func (s *stubWebhookEndpoints) GetByID(_ context.Context, _ uint64, id string) (*types.TenantWebhookEndpoint, error) {
	if s.row != nil && s.row.ID == id {
		return s.row, nil
	}
	return nil, ErrWebhookEventsRequired
}
func (s *stubWebhookEndpoints) GetByIDUnscoped(context.Context, string) (*types.TenantWebhookEndpoint, error) {
	return s.row, nil
}
func (s *stubWebhookEndpoints) ListByTenant(context.Context, uint64) ([]*types.TenantWebhookEndpoint, error) {
	return nil, nil
}
func (s *stubWebhookEndpoints) ListEnabledByTenant(context.Context, uint64) ([]*types.TenantWebhookEndpoint, error) {
	return nil, nil
}
func (s *stubWebhookEndpoints) CountByTenant(context.Context, uint64) (int64, error) {
	return s.count, nil
}
func (s *stubWebhookEndpoints) FindByTenantURL(context.Context, uint64, string) (*types.TenantWebhookEndpoint, error) {
	return nil, nil
}

type stubWebhookDeliveries struct{}

func (stubWebhookDeliveries) Claim(context.Context, *types.TenantWebhookDelivery) (bool, *types.TenantWebhookDelivery, error) {
	return false, nil, nil
}
func (stubWebhookDeliveries) GetByEventEndpoint(context.Context, string, string) (*types.TenantWebhookDelivery, error) {
	return nil, nil
}
func (stubWebhookDeliveries) UpdateAttempt(context.Context, string, int, int, int, string, string, bool) error {
	return nil
}
func (stubWebhookDeliveries) ListByEndpoint(context.Context, string, int) ([]*types.TenantWebhookDelivery, error) {
	return nil, nil
}
func (stubWebhookDeliveries) ListEndpointIDs(context.Context) ([]string, error) { return nil, nil }
func (stubWebhookDeliveries) PruneEndpointKeepLatest(context.Context, string, int) (int64, error) {
	return 0, nil
}
func (stubWebhookDeliveries) DeleteOlderThanDays(context.Context, int) (int64, error) { return 0, nil }

type fakeWebhookAudit struct {
	entries []*types.AuditLog
}

func (f *fakeWebhookAudit) Log(_ context.Context, entry *types.AuditLog) error {
	cp := *entry
	f.entries = append(f.entries, &cp)
	return nil
}
func (f *fakeWebhookAudit) LogDenied(context.Context, *gin.Context, uint64, string, string, types.TenantRole) error {
	return nil
}
func (f *fakeWebhookAudit) List(context.Context, uint64, *interfaces.AuditLogQuery) ([]*types.AuditLog, error) {
	return nil, nil
}
func (f *fakeWebhookAudit) Purge(context.Context, int) (int64, error) { return 0, nil }

func TestNormalizeWebhookEventsRejectsEmptyAndTestType(t *testing.T) {
	if _, err := normalizeWebhookEvents(nil); err == nil {
		t.Fatal("expected empty events to fail")
	}
	if _, err := normalizeWebhookEvents([]string{types.EventWebhookTest}); err == nil {
		t.Fatal("expected webhook.test to be rejected as a subscription type")
	}
	got, err := normalizeWebhookEvents([]string{types.EventKnowledgeCreated, types.EventKnowledgeCreated})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != types.EventKnowledgeCreated {
		t.Fatalf("dedup: got %#v", got)
	}
}

func TestWebhookEndpointCreateWritesAudit(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", "01234567890123456789012345678901")
	repo := &stubWebhookEndpoints{}
	audit := &fakeWebhookAudit{}
	svc := NewWebhookEndpointService(repo, stubWebhookDeliveries{}, nil, nil, audit)
	enabled := true
	got, err := svc.Create(context.Background(), 42, interfaces.WebhookEndpointCreate{
		Name:    "prod",
		URL:     "http://127.0.0.1:9/weknora",
		Secret:  "0123456789abcdef",
		Events:  []string{types.EventKnowledgeCreated},
		Enabled: &enabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.URL != "http://127.0.0.1:9/weknora" {
		t.Fatalf("unexpected endpoint: %#v", got)
	}
	if len(audit.entries) != 1 || audit.entries[0].Action != types.AuditActionWebhookEndpointCreated {
		t.Fatalf("audit: %#v", audit.entries)
	}
	if audit.entries[0].TargetType != "webhook_endpoint" || audit.entries[0].TargetID != got.ID {
		t.Fatalf("audit target: %#v", audit.entries[0])
	}
}

func TestWebhookEndpointCreateRejectsEmptyEvents(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", "01234567890123456789012345678901")
	svc := NewWebhookEndpointService(&stubWebhookEndpoints{}, stubWebhookDeliveries{}, nil, nil, nil)
	_, err := svc.Create(context.Background(), 42, interfaces.WebhookEndpointCreate{
		Name:   "prod",
		URL:    "https://hooks.example.com/weknora",
		Secret: "0123456789abcdef",
	})
	if err == nil {
		t.Fatal("expected empty events to fail create")
	}
}
