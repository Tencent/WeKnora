package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type summaryRefreshKnowledgeRepo struct {
	interfaces.KnowledgeRepository
	transitioned       bool
	conditionalUpdated bool
	transitionAttempt  int
	expectedStatus     string
	values             map[string]interface{}
}

func (r *summaryRefreshKnowledgeRepo) TransitionKnowledgeForAttempt(
	_ context.Context,
	_ string,
	attempt int,
	expectedStatus string,
	values map[string]interface{},
) (bool, error) {
	r.transitionAttempt = attempt
	r.expectedStatus = expectedStatus
	r.values = values
	return r.transitioned, nil
}

func (r *summaryRefreshKnowledgeRepo) UpdateKnowledgeColumnsIfUnchanged(
	_ context.Context,
	_, expectedStatus string,
	_ time.Time,
	values map[string]interface{},
) (bool, error) {
	r.expectedStatus = expectedStatus
	r.values = values
	return r.conditionalUpdated, nil
}

type summaryRefreshTenantRepo struct {
	interfaces.TenantRepository
	tenant      *types.Tenant
	err         error
	requestedID uint64
}

func (r *summaryRefreshTenantRepo) GetTenantByID(_ context.Context, id uint64) (*types.Tenant, error) {
	r.requestedID = id
	return r.tenant, r.err
}

func TestRestoreSummaryRefreshTenantInfo(t *testing.T) {
	repo := &summaryRefreshTenantRepo{tenant: &types.Tenant{ID: 42}}
	ctx, err := restoreSummaryRefreshTenantInfo(context.Background(), repo, 42)
	if err != nil {
		t.Fatalf("restore tenant context: %v", err)
	}
	if repo.requestedID != 42 {
		t.Fatalf("requested tenant ID = %d, want 42", repo.requestedID)
	}
	tenant, ok := types.TenantInfoFromContext(ctx)
	if !ok || tenant.ID != 42 {
		t.Fatalf("tenant context = %#v, %v; want tenant 42", tenant, ok)
	}
}

func TestRestoreSummaryRefreshTenantInfoRejectsMissingTenant(t *testing.T) {
	tests := []struct {
		name string
		repo interfaces.TenantRepository
	}{
		{name: "repository unavailable"},
		{name: "tenant lookup failed", repo: &summaryRefreshTenantRepo{err: errors.New("lookup failed")}},
		{name: "tenant not found", repo: &summaryRefreshTenantRepo{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, err := restoreSummaryRefreshTenantInfo(context.Background(), tt.repo, 42)
			if err == nil {
				t.Fatal("expected an error")
			}
			if _, ok := types.TenantInfoFromContext(ctx); ok {
				t.Fatal("tenant info should not be added after a failed lookup")
			}
		})
	}
}

func TestErrSummaryRefreshStaleIsDistinct(t *testing.T) {
	if ErrSummaryRefreshStale == nil {
		t.Fatal("expected stale refresh sentinel")
	}
	if ErrSummaryRefreshStale.Error() == "" {
		t.Fatal("expected non-empty stale refresh message")
	}
}

func TestUpdateSummaryRefreshStatusUsesExactAttemptTransition(t *testing.T) {
	repo := &summaryRefreshKnowledgeRepo{transitioned: true}
	err := updateSummaryRefreshStatus(
		context.Background(), repo, "knowledge-1", 8, types.SummaryStatusPending,
	)
	if err != nil {
		t.Fatal(err)
	}
	if repo.transitionAttempt != 8 || repo.expectedStatus != types.ParseStatusCompleted {
		t.Fatalf("transition attempt/status = %d/%q, want 8/%q",
			repo.transitionAttempt, repo.expectedStatus, types.ParseStatusCompleted)
	}
	if repo.values["summary_status"] != types.SummaryStatusPending {
		t.Fatalf("summary status = %#v, want pending", repo.values["summary_status"])
	}
}

func TestUpdateSummaryRefreshStatusRejectsLostOwnership(t *testing.T) {
	repo := &summaryRefreshKnowledgeRepo{}
	err := updateSummaryRefreshStatus(
		context.Background(), repo, "knowledge-1", 8, types.SummaryStatusPending,
	)
	if !errors.Is(err, ErrSummaryRefreshStale) {
		t.Fatalf("error = %v, want ErrSummaryRefreshStale", err)
	}
}

func TestUpdateSummaryRefreshStatusKeepsLegacyStatusGuard(t *testing.T) {
	repo := &summaryRefreshKnowledgeRepo{conditionalUpdated: true}
	err := updateSummaryRefreshStatus(
		context.Background(), repo, "knowledge-1", 0, types.SummaryStatusFailed,
	)
	if err != nil {
		t.Fatal(err)
	}
	if repo.expectedStatus != types.ParseStatusCompleted {
		t.Fatalf("legacy expected status = %q, want completed", repo.expectedStatus)
	}
}
