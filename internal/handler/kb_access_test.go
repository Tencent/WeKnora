package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type handlerKBShareStub struct {
	interfaces.KBShareService
	calls  int
	caller uint64
}

func (s *handlerKBShareStub) CheckTenantKBPermission(
	_ context.Context,
	_ string,
	caller uint64,
	_ types.TenantRole,
) (types.OrgMemberRole, bool, error) {
	s.calls++
	s.caller = caller
	return types.OrgRoleViewer, true, nil
}

type handlerKnowledgeAccessStub struct {
	interfaces.KnowledgeService
	knowledge *types.Knowledge
}

func (s *handlerKnowledgeAccessStub) GetKnowledgeByIDOnly(context.Context, string) (*types.Knowledge, error) {
	return s.knowledge, nil
}

func TestKBGuardAndHandlersShareResolution(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, ginCallerKey := range []bool{true, false} {
		name := "request context only"
		if ginCallerKey {
			name = "both context surfaces"
		}
		t.Run(name, func(t *testing.T) {
			kb := &types.KnowledgeBase{ID: "kb", TenantID: 2}
			lookups := 0
			svc := &stubKBOnlyService{getByID: func(context.Context, string) (*types.KnowledgeBase, error) {
				lookups++
				return kb, nil
			}}
			shares := &handlerKBShareStub{}
			kg := &handlerKnowledgeAccessStub{
				knowledge: &types.Knowledge{ID: "doc", KnowledgeBaseID: "kb", TenantID: 2},
			}
			h := &KnowledgeHandler{kbService: svc, kgService: kg, kbShareService: shares}
			kbHandler := &KnowledgeBaseHandler{service: svc, kbShareService: shares}
			r := gin.New()
			r.Use(middleware.ErrorHandler(), func(c *gin.Context) {
				ctx := context.WithValue(c.Request.Context(), types.TenantIDContextKey, uint64(1))
				ctx = context.WithValue(ctx, types.TenantRoleContextKey, types.TenantRoleViewer)
				c.Request = c.Request.WithContext(ctx)
				if ginCallerKey {
					c.Set(types.TenantIDContextKey.String(), uint64(1))
				}
				c.Next()
			})
			enabled := true
			r.GET("/:id", middleware.RequireKBAccess(
				middleware.KBIDFromParam("id"),
				types.OrgRoleViewer,
				svc,
				shares,
				nil,
				&config.Config{Tenant: &config.TenantConfig{EnableRBAC: &enabled}},
			), func(c *gin.Context) {
				_, _, effective, permission, err := kbHandler.validateAndGetKnowledgeBase(c)
				require.NoError(t, err)
				require.Equal(t, uint64(2), effective)
				require.Equal(t, types.OrgRoleViewer, permission)
				_, _, _, permission, err = h.validateKnowledgeBaseAccess(c)
				require.NoError(t, err)
				require.Equal(t, types.OrgRoleViewer, permission)
				_, ctx, err := h.resolveKnowledgeAndValidateKBAccess(c, "doc", types.OrgRoleViewer)
				require.NoError(t, err)
				require.Equal(t, uint64(2), types.MustTenantIDFromContext(ctx))
				require.Equal(t, uint64(1), types.CallerFromContext(ctx).TenantID)
				require.Equal(t, 1, lookups, "handlers must reuse the guarded KB")
				require.Equal(t, 1, shares.calls, "handlers must reuse the permission decision")
				// The effective tenant is 2, but the write must still be checked
				// as caller 1 and rejected, not upgraded to resource ownership.
				_, _, err = h.resolveKnowledgeAndValidateKBAccess(c, "doc", types.OrgRoleEditor)
				require.Error(t, err)
				require.Equal(t, uint64(1), shares.caller)
				c.Status(http.StatusNoContent)
			})
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/kb", nil))
			require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
		})
	}
}

func TestKBHandlerDoesNotReuseGrantForAnotherResourceOrCaller(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tt := range []struct {
		name, requestedID string
		caller            uint64
	}{
		{name: "another KB", requestedID: "other", caller: 1},
		{name: "another caller", requestedID: "kb", caller: 3},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
			c.Set(types.TenantIDContextKey.String(), tt.caller)
			c.Set(middleware.KBAccessContextKey, &middleware.KBAccess{
				KnowledgeBase: &types.KnowledgeBase{ID: "kb", TenantID: 2},
				Caller: types.Caller{
					TenantID: 1,
					Role:     types.TenantRoleViewer,
				}, EffectiveTenantID: 2, Permission: types.OrgRoleAdmin,
			})
			svc := &stubKBOnlyService{getByID: func(context.Context, string) (*types.KnowledgeBase, error) {
				return &types.KnowledgeBase{ID: tt.requestedID, TenantID: 2}, nil
			}}
			_, err := resolveHandlerKBAccess(c, tt.requestedID, svc, nil, nil)
			require.Error(t, err)
		})
	}
}

func TestKBHandlerAPIKeyScopeStillAppliesToCachedGrant(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx := types.WithTenantAPIKeyScope(
		context.Background(),
		types.TenantAPIKeyScope{KnowledgeBaseIDs: types.StringArray{"other"}},
	)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
	c.Set(types.TenantIDContextKey.String(), uint64(1))
	c.Set(middleware.KBAccessContextKey, &middleware.KBAccess{
		KnowledgeBase: &types.KnowledgeBase{ID: "kb", TenantID: 1},
		Caller: types.Caller{
			TenantID: 1,
			Role:     types.TenantRoleViewer,
		}, EffectiveTenantID: 1, Permission: types.OrgRoleAdmin,
	})
	_, err := resolveHandlerKBAccess(c, "kb", nil, nil, nil)
	require.Error(t, err)
}

type handlerAgentAccessStub struct {
	interfaces.AgentShareService
	anyCalls int
}

func (s *handlerAgentAccessStub) GetSharedAgentForTenant(
	context.Context,
	uint64,
	types.TenantRole,
	string,
	...uint64,
) (*types.CustomAgent, error) {
	return &types.CustomAgent{TenantID: 2, Config: types.CustomAgentConfig{KBSelectionMode: "none"}}, nil
}

func (s *handlerAgentAccessStub) TenantCanAccessKBViaSomeSharedAgent(
	context.Context,
	uint64,
	types.TenantRole,
	*types.KnowledgeBase,
) (bool, error) {
	s.anyCalls++
	return true, nil
}

func TestKBHandlersHonorExplicitAgentWithoutRouteGuard(t *testing.T) {
	for _, query := range []string{"", "?agent_id=restricted"} {
		t.Run(query, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodGet, "/kb"+query, nil)
			c.Set(types.TenantIDContextKey.String(), uint64(1))
			c.Params = gin.Params{{Key: "id", Value: "kb"}}
			svc := &stubKBOnlyService{getByID: func(context.Context, string) (*types.KnowledgeBase, error) {
				return &types.KnowledgeBase{ID: "kb", TenantID: 2}, nil
			}}
			agents := &handlerAgentAccessStub{}
			h := &KnowledgeHandler{kbService: svc, agentShareService: agents}
			kbHandler := &KnowledgeBaseHandler{service: svc, agentShareService: agents}
			_, _, _, _, kbErr := kbHandler.validateAndGetKnowledgeBase(c)
			_, _, _, _, knowledgeErr := h.validateKnowledgeBaseAccessWithKBID(c, "kb")
			if query == "" {
				require.NoError(t, kbErr)
				require.NoError(t, knowledgeErr)
				require.Equal(t, 2, agents.anyCalls)
			} else {
				require.Error(t, kbErr)
				require.Error(t, knowledgeErr)
				require.Zero(t, agents.anyCalls)
			}
		})
	}
}

func TestKBHandlerEnforcesAccessWhenRouteRBACIsDisabled(t *testing.T) {
	svc := &stubKBOnlyService{getByID: func(context.Context, string) (*types.KnowledgeBase, error) {
		return &types.KnowledgeBase{ID: "kb", TenantID: 2}, nil
	}}
	r := gin.New()
	r.Use(middleware.ErrorHandler(), func(c *gin.Context) {
		c.Set(types.TenantIDContextKey.String(), uint64(1))
		c.Next()
	})
	disabled := false
	r.GET("/:id", middleware.RequireKBAccess(middleware.KBIDFromParam("id"), types.OrgRoleViewer,
		svc, nil, nil, &config.Config{Tenant: &config.TenantConfig{EnableRBAC: &disabled}}), func(c *gin.Context) {
		_, err := resolveHandlerKBAccess(c, c.Param("id"), svc, nil, nil)
		require.Error(t, err)
		_ = c.Error(err)
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/kb", nil))
	require.Equal(t, http.StatusForbidden, w.Code)
}
