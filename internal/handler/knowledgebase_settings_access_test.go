package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestKnowledgeBaseUpdateSettingsAccess(t *testing.T) {
	for _, method := range []string{http.MethodPut, http.MethodPatch} {
		for _, caller := range []struct {
			name       string
			owner      uint64
			permission types.OrgMemberRole
		}{
			{"owner", 1, types.OrgRoleAdmin},
			{"share admin", 2, types.OrgRoleAdmin},
			{"share editor", 2, types.OrgRoleEditor},
		} {
			for _, setting := range []struct {
				field  string
				value  any
				rebind bool
			}{
				{"name", "renamed", false},
				{"embedding_model_id", "embedding", false},
				{"summary_model_id", "summary", false},
				{"asr_config", map[string]any{"enabled": false}, false},
				{"vlm_config", map[string]any{"enabled": false}, false},
				{"storage_backend_id", "receiver-store", true},
				{"storage_backend_id", "owner-store", false},
			} {
				name := method + "/" + caller.name + "/" + setting.field
				if setting.rebind {
					name += "/rebind"
				}
				t.Run(name, func(t *testing.T) {
					backend := "owner-store"
					svc := &vlmUpdateKBService{kb: &types.KnowledgeBase{
						ID: "kb-1", TenantID: caller.owner, StorageBackendID: &backend,
					}}
					shares := &vlmUpdateShareStub{permission: caller.permission}
					h := &KnowledgeBaseHandler{service: svc, kbShareService: shares}
					gin.SetMode(gin.TestMode)
					r := gin.New()
					r.Use(middleware.ErrorHandler(), func(c *gin.Context) {
						ctx := context.WithValue(c.Request.Context(), types.TenantIDContextKey, uint64(1))
						ctx = context.WithValue(ctx, types.TenantRoleContextKey, types.TenantRoleAdmin)
						c.Request = c.Request.WithContext(ctx)
						c.Set(types.TenantIDContextKey.String(), uint64(1))
						c.Next()
					})
					enabled := true
					handle := h.UpdateKnowledgeBase
					if method == http.MethodPatch {
						handle = h.PatchKnowledgeBase
					}
					r.Handle(method, "/:id", middleware.RequireKBAccess(
						middleware.KBIDFromParam("id"), types.OrgRoleEditor, svc, shares, nil,
						&config.Config{Tenant: &config.TenantConfig{EnableRBAC: &enabled}},
					), handle)
					body := map[string]any{"name": "renamed"}
					if method == http.MethodPut && setting.field != "name" && setting.field != "vlm_config" {
						body["config"] = map[string]any{setting.field: setting.value}
					} else {
						body[setting.field] = setting.value
					}
					encoded, err := json.Marshal(body)
					require.NoError(t, err)
					req := httptest.NewRequest(method, "/kb-1", strings.NewReader(string(encoded)))
					req.Header.Set("Content-Type", "application/json")
					w := httptest.NewRecorder()
					r.ServeHTTP(w, req)

					want := http.StatusOK
					if caller.owner != 1 && setting.field != "name" &&
						(caller.permission != types.OrgRoleAdmin || setting.rebind) {
						want = http.StatusForbidden
					}
					require.Equal(t, want, w.Code, w.Body.String())
					require.Equal(t, want == http.StatusOK, svc.updated)
				})
			}
		}
	}
}
