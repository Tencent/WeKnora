package container

import (
	"context"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// fakeAuditSvc records Log calls.
type fakeAuditSvc struct {
	logged []*types.AuditLog
	err    error
}

func (f *fakeAuditSvc) Log(_ context.Context, e *types.AuditLog) error {
	f.logged = append(f.logged, e)
	return f.err
}
func (f *fakeAuditSvc) LogDenied(context.Context, *gin.Context, uint64, string, string, types.TenantRole) error {
	return nil
}
func (f *fakeAuditSvc) List(context.Context, uint64, *interfaces.AuditLogQuery) ([]*types.AuditLog, error) {
	return nil, nil
}
func (f *fakeAuditSvc) Purge(context.Context, int) (int64, error) { return 0, nil }

func ctxWithTenant(id uint64) context.Context {
	return context.WithValue(context.Background(), types.TenantIDContextKey, id)
}

func TestAuditSinkAdapter_EmitsWithTenant(t *testing.T) {
	f := &fakeAuditSvc{}
	sink := newAuditSinkAdapter(f)

	sink.EmitIndexCreated(ctxWithTenant(42), "weknora_768", 768)
	if len(f.logged) != 1 {
		t.Fatalf("want 1 audit entry, got %d", len(f.logged))
	}
	e := f.logged[0]
	if e.TenantID != 42 {
		t.Errorf("tenant: want 42, got %d", e.TenantID)
	}
	if e.Action != types.AuditActionOpenSearchIndexCreated {
		t.Errorf("action: want %s, got %s", types.AuditActionOpenSearchIndexCreated, e.Action)
	}
	if e.Details == nil {
		t.Error("details should be populated")
	}

	sink.EmitReindexExecuted(ctxWithTenant(7), "src", "dst", 9)
	if len(f.logged) != 2 || f.logged[1].Action != types.AuditActionOpenSearchReindexExecuted {
		t.Errorf("reindex audit not recorded: %+v", f.logged)
	}
}

func TestAuditSinkAdapter_SkipsWithoutTenant(t *testing.T) {
	f := &fakeAuditSvc{}
	sink := newAuditSinkAdapter(f)
	// background ctx carries no tenant → adapter must skip (never write tenant=0)
	sink.EmitIndexCreated(context.Background(), "weknora_768", 768)
	sink.EmitReindexExecuted(context.Background(), "a", "b", 1)
	if len(f.logged) != 0 {
		t.Errorf("want 0 audit entries without tenant, got %d", len(f.logged))
	}
}

func TestAuditSinkAdapter_NilServiceNoPanic(t *testing.T) {
	sink := newAuditSinkAdapter(nil)
	sink.EmitIndexCreated(ctxWithTenant(1), "x", 1) // must not panic
}
