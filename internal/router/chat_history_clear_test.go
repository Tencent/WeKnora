// Package router registers HTTP routes and their permission policies.
package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestChatHistoryClearRequiresTenantSettingsCapability(t *testing.T) {
	gin.SetMode(gin.TestMode)
	g := &rbacGuards{}
	RegisterMessageRoutes(gin.New().Group("/api/v1"), &handler.MessageHandler{}, g)
	policy := mustLookupAPIKeyPolicy(t, g, http.MethodPost, "/api/v1/messages/chat-history-index/clear")
	if !policy.RequireFullAccess || !policyHasCapability(policy, types.APIKeyCapabilityManageTenantSettings) {
		t.Fatalf("clearing workspace history requires full access or manage_tenant_settings: %#v", policy)
	}
	if policyHasCapability(policy, types.APIKeyCapabilityMessageHistory) ||
		policyHasCapability(policy, types.APIKeyCapabilityChat) {
		t.Fatalf("read/search or chat capabilities must not grant workspace-wide deletion: %#v", policy)
	}
}

type routeClearHistoryService struct {
	interfaces.MessageService
	calls int
}

func (s *routeClearHistoryService) ClearChatHistoryIndex(_ context.Context) (*types.ChatHistoryKBStats, error) {
	s.calls++
	return &types.ChatHistoryKBStats{}, nil
}

func TestChatHistoryClearRequiresAdministrator(t *testing.T) {
	for _, role := range []types.TenantRole{
		types.TenantRoleViewer, types.TenantRoleContributor, types.TenantRoleAdmin, types.TenantRoleOwner,
	} {
		t.Run(string(role), func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			enforce := true
			g := &rbacGuards{cfg: &config.Config{Tenant: &config.TenantConfig{EnableRBAC: &enforce}}}
			r := gin.New()
			r.Use(middleware.ErrorHandler(), func(c *gin.Context) {
				ctx := types.WithCaller(c.Request.Context(), types.Caller{TenantID: 1, Role: role})
				c.Request = c.Request.WithContext(ctx)
				c.Next()
			})
			svc := &routeClearHistoryService{}
			RegisterMessageRoutes(r.Group("/api/v1"), &handler.MessageHandler{MessageService: svc}, g)
			recorder := httptest.NewRecorder()
			r.ServeHTTP(recorder, httptest.NewRequest(
				http.MethodPost, "/api/v1/messages/chat-history-index/clear", nil,
			))
			if role.HasPermission(types.TenantRoleAdmin) {
				require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
				require.Equal(t, 1, svc.calls)
			} else {
				require.Equal(t, http.StatusForbidden, recorder.Code, recorder.Body.String())
				require.Zero(t, svc.calls)
			}
		})
	}
}
