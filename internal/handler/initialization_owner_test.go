package handler

import (
	"context"
	stderrors "errors"
	"net/http"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type stubInitializationKBService struct {
	interfaces.KnowledgeBaseService
	kb *types.KnowledgeBase
}

func (s *stubInitializationKBService) GetKnowledgeBaseByID(context.Context, string) (*types.KnowledgeBase, error) {
	return s.kb, nil
}

func requireForbidden(t *testing.T, err error) {
	t.Helper()
	var appErr *apperrors.AppError
	if !stderrors.As(err, &appErr) || appErr.HTTPCode != http.StatusForbidden {
		t.Fatalf("err = %v, want 403", err)
	}
}

// A shared-KB editor reaches the handler with execution moved into the owner
// workspace; the KB must still be refused because the caller does not own it.
func TestInitializationRejectsKBOfAnotherWorkspace(t *testing.T) {
	h := &InitializationHandler{kbService: &stubInitializationKBService{
		kb: &types.KnowledgeBase{ID: "kb-1", TenantID: 7},
	}}
	ctx := types.WithCaller(context.Background(), types.Caller{TenantID: 42, UserID: "u", Role: types.TenantRoleOwner})
	ctx = types.WithExecutionTenant(ctx, 7)

	_, err := h.getKnowledgeBaseForInitialization(ctx, "kb-1")
	requireForbidden(t, err)
}

func TestInitializationAllowsOwnKB(t *testing.T) {
	h := &InitializationHandler{kbService: &stubInitializationKBService{
		kb: &types.KnowledgeBase{ID: "kb-1", TenantID: 42},
	}}
	ctx := types.WithCaller(context.Background(),
		types.Caller{TenantID: 42, UserID: "u", Role: types.TenantRoleContributor})

	if _, err := h.getKnowledgeBaseForInitialization(ctx, "kb-1"); err != nil {
		t.Fatalf("own KB rejected: %v", err)
	}
}

// Rewriting an already-stored model needs the same authority as PUT
// /models/:id; creating the KB's first models does not.
func TestInitializationExistingModelUpdateRequiresModelAuthority(t *testing.T) {
	enforced := true
	stored := &types.Model{ID: "m-existing", Type: types.ModelTypeKnowledgeQA, TenantID: 42}
	kb := &types.KnowledgeBase{ID: "kb-1", TenantID: 42, SummaryModelID: "m-existing"}
	caller := func(role types.TenantRole) context.Context {
		return types.WithCaller(context.Background(), types.Caller{TenantID: 42, UserID: "u", Role: role})
	}
	scopedKey := func(capability types.APIKeyCapability) context.Context {
		scope := types.TenantAPIKeyScope{Capabilities: types.StringArray{string(capability)}}
		return types.WithTenantAPIKeyScope(caller(types.TenantRoleViewer), scope)
	}

	cases := []struct {
		name    string
		ctx     context.Context
		allowed bool
	}{
		{"contributor", caller(types.TenantRoleContributor), false},
		{"admin", caller(types.TenantRoleAdmin), true},
		{"scoped key without manage_models", scopedKey(types.APIKeyCapabilityManageKnowledgeBases), false},
		{"scoped key with manage_models", scopedKey(types.APIKeyCapabilityManageModels), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := &stubTenantStampModelService{getModelByID: func(_ context.Context, id string) (*types.Model, error) {
				if id == stored.ID {
					copied := *stored
					return &copied, nil
				}
				return nil, nil
			}}
			h := &InitializationHandler{
				modelService: svc,
				config:       &config.Config{Tenant: &config.TenantConfig{EnableRBAC: &enforced}},
			}
			_, err := h.processInitializationModels(tc.ctx, kb, "kb-1", newTenantStampRequest())
			if tc.allowed {
				if err != nil || len(svc.updated) != 1 {
					t.Fatalf("err = %v, updated = %d, want one update", err, len(svc.updated))
				}
				return
			}
			requireForbidden(t, err)
			if len(svc.updated) != 0 {
				t.Fatalf("model was updated despite missing authority")
			}
		})
	}
}
