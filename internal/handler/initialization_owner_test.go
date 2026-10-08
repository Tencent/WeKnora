package handler

import (
	"context"
	stderrors "errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/config"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
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

type stubInitializationKBRepo struct {
	interfaces.KnowledgeBaseRepository
	updated *types.KnowledgeBase
}

func (r *stubInitializationKBRepo) UpdateKnowledgeBase(_ context.Context, kb *types.KnowledgeBase) error {
	r.updated = kb
	return nil
}

// PUT /initialization/config shares the route guard (KBAccessWrite) that
// admits share editors. KB settings belong to the owner and to admin shares
// only, and a share admin still cannot rebind the owner's storage.
func TestUpdateKBConfigFromAnotherWorkspace(t *testing.T) {
	backend := "backend-of-owner"
	cases := []struct {
		name       string
		permission types.OrgMemberRole
		body       string
		wantStatus int
	}{
		{
			name: "share editor", permission: types.OrgRoleEditor,
			body: `{"llmModelId":"m-llm"}`, wantStatus: http.StatusForbidden,
		},
		{
			name: "share admin rebinding storage", permission: types.OrgRoleAdmin,
			body: `{"llmModelId":"m-llm","storageBackendId":"backend-of-receiver"}`, wantStatus: http.StatusForbidden,
		},
		{
			name: "share admin keeping storage", permission: types.OrgRoleAdmin,
			body:       `{"llmModelId":"m-llm","storageBackendId":"backend-of-owner","storageProvider":"minio"}`,
			wantStatus: http.StatusOK,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kb := &types.KnowledgeBase{ID: "kb-1", TenantID: 7, StorageBackendID: &backend}
			repo := &stubInitializationKBRepo{}
			models := &stubTenantStampModelService{getModelByID: func(context.Context, string) (*types.Model, error) {
				return &types.Model{ID: "m-llm", TenantID: 7}, nil
			}}
			h := &InitializationHandler{
				kbService:    &stubInitializationKBService{kb: kb},
				kbRepository: repo,
				modelService: models,
			}

			gin.SetMode(gin.TestMode)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Params = gin.Params{{Key: "kbId", Value: "kb-1"}}
			req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			caller := types.Caller{TenantID: 42, UserID: "u", Role: types.TenantRoleAdmin}
			ctx := types.WithExecutionTenant(types.WithCaller(req.Context(), caller), 7)
			c.Request = req.WithContext(ctx)
			c.Set(middleware.KBAccessContextKey, &access.KBAccess{
				KnowledgeBase: kb, Caller: caller, EffectiveTenantID: 7, Permission: tc.permission,
			})

			h.UpdateKBConfig(c)

			if tc.wantStatus == http.StatusOK {
				require.Empty(t, c.Errors)
				require.NotNil(t, repo.updated)
				require.Equal(t, backend, *repo.updated.StorageBackendID)
				return
			}
			require.Len(t, c.Errors, 1)
			requireForbidden(t, c.Errors[0].Err)
			require.Nil(t, repo.updated, "KB must not be written")
		})
	}
}
