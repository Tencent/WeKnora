package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type vlmUpdateKBService struct {
	interfaces.KnowledgeBaseService
	kb      *types.KnowledgeBase
	updated bool
}

func (s *vlmUpdateKBService) GetKnowledgeBaseByID(context.Context, string) (*types.KnowledgeBase, error) {
	return s.kb, nil
}

func (s *vlmUpdateKBService) UpdateKnowledgeBase(
	_ context.Context, _, _, _ string, _ *types.KnowledgeBaseConfig, _ *types.VLMConfig,
) (*types.KnowledgeBase, error) {
	s.updated = true
	return s.kb, nil
}

type vlmUpdateShareStub struct {
	interfaces.KBShareService
	permission types.OrgMemberRole
}

func (s *vlmUpdateShareStub) CheckTenantKBPermission(
	context.Context, string, uint64, types.TenantRole,
) (types.OrgMemberRole, bool, error) {
	return s.permission, true, nil
}

// PUT /knowledge-bases/:id admits share editors through KBAccessWrite, but the
// VLM model is a KB setting: like PUT /initialization/config, only the owner
// workspace or an admin share may change it.
func TestUpdateKnowledgeBaseVLMConfigRequiresSettingsAccess(t *testing.T) {
	const withVLM = `{"name":"n","vlm_config":{"enabled":true,"model_id":"vlm-1"}}`
	cases := []struct {
		name        string
		ownerTenant uint64
		permission  types.OrgMemberRole
		body        string
		wantStatus  int
	}{
		{"share editor with vlm_config", 2, types.OrgRoleEditor, withVLM, http.StatusForbidden},
		{"share editor without vlm_config", 2, types.OrgRoleEditor, `{"name":"n"}`, http.StatusOK},
		{"share admin with vlm_config", 2, types.OrgRoleAdmin, withVLM, http.StatusOK},
		{"owner workspace with vlm_config", 1, "", withVLM, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			svc := &vlmUpdateKBService{kb: &types.KnowledgeBase{ID: "kb-1", TenantID: tc.ownerTenant}}
			shares := &vlmUpdateShareStub{permission: tc.permission}
			h := &KnowledgeBaseHandler{service: svc, kbShareService: shares}
			enabled := true
			r := gin.New()
			r.Use(middleware.ErrorHandler(), func(c *gin.Context) {
				ctx := context.WithValue(c.Request.Context(), types.TenantIDContextKey, uint64(1))
				ctx = context.WithValue(ctx, types.TenantRoleContextKey, types.TenantRoleAdmin)
				c.Request = c.Request.WithContext(ctx)
				c.Set(types.TenantIDContextKey.String(), uint64(1))
				c.Next()
			})
			r.PUT("/:id", middleware.RequireKBAccess(
				middleware.KBIDFromParam("id"), types.OrgRoleEditor, svc, shares, nil,
				&config.Config{Tenant: &config.TenantConfig{EnableRBAC: &enabled}},
			), h.UpdateKnowledgeBase)

			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPut, "/kb-1", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(w, req)

			require.Equal(t, tc.wantStatus, w.Code, w.Body.String())
			require.Equal(t, tc.wantStatus == http.StatusOK, svc.updated)
		})
	}
}
