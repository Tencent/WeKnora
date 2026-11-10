package handler

import (
	"context"
	stderrors "errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestBodyKBOwnershipSkipsUnnecessaryLookup(t *testing.T) {
	for _, tt := range []struct {
		name                        string
		role                        types.TenantRole
		apiKey, superuser, disabled bool
	}{
		{name: "admin", role: types.TenantRoleAdmin},
		{name: "owner", role: types.TenantRoleOwner},
		{name: "API key", role: types.TenantRoleViewer, apiKey: true},
		{name: "cross tenant superuser", role: types.TenantRoleViewer, superuser: true},
		{name: "RBAC disabled", role: types.TenantRoleContributor, disabled: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := newKBLookupCtx(t, 1, "kb")
			ctx := context.WithValue(c.Request.Context(), types.TenantRoleContextKey, tt.role)
			ctx = context.WithValue(
				ctx,
				types.UserContextKey,
				&types.User{ID: "user", CanAccessAllTenants: tt.superuser},
			)
			if tt.apiKey {
				ctx = types.WithTenantAPIKeyScope(
					ctx,
					types.TenantAPIKeyScope{KnowledgeBaseIDs: types.StringArray{"kb"}},
				)
			}
			c.Request = c.Request.WithContext(ctx)
			enabled := !tt.disabled
			h := &KnowledgeHandler{
				cfg: &config.Config{
					Tenant: &config.TenantConfig{EnableRBAC: &enabled, EnableCrossTenantAccess: tt.superuser},
				},
				kbService: &stubKBService{get: func(context.Context, string) (*types.KnowledgeBase, error) {
					t.Fatal("body-carried KB ownership must be looked up lazily")
					return nil, stderrors.New("unavailable")
				}},
			}
			require.NoError(t, h.requireKBOwnershipOrAdmin(c, "kb"))
		})
	}
}

func TestBodyKBOwnershipPreservesStatusAndTenantBoundary(t *testing.T) {
	for _, tt := range []struct {
		name      string
		kb        *types.KnowledgeBase
		lookupErr error
		status    int
	}{
		{
			name: "creator",
			kb: &types.KnowledgeBase{
				ID:        "kb",
				TenantID:  1,
				CreatorID: "user",
			},
			status: http.StatusNoContent,
		},

		{
			name: "noncreator",
			kb: &types.KnowledgeBase{
				ID:        "kb",
				TenantID:  1,
				CreatorID: "other",
			},
			status: http.StatusForbidden,
		},

		{name: "tenant owned", kb: &types.KnowledgeBase{ID: "kb", TenantID: 1}, status: http.StatusForbidden},
		{
			name: "same creator in another tenant",
			kb: &types.KnowledgeBase{
				ID:        "kb",
				TenantID:  2,
				CreatorID: "user",
			},
			status: http.StatusNotFound,
		},

		{name: "nil resource", status: http.StatusNotFound},
		{
			name: "wrapped not found",
			lookupErr: fmt.Errorf("lookup: %w",
				repository.ErrKnowledgeBaseNotFound),
			status: http.StatusNotFound,
		},

		{
			name:      "database failure",
			lookupErr: stderrors.New("database unavailable"),
			status:    http.StatusInternalServerError,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			enabled := true
			calls := 0
			h := &KnowledgeHandler{
				cfg: &config.Config{Tenant: &config.TenantConfig{EnableRBAC: &enabled}},
				kbService: &stubKBService{get: func(context.Context, string) (*types.KnowledgeBase, error) {
					calls++
					return tt.kb, tt.lookupErr
				}},
			}
			r := gin.New()
			r.Use(middleware.ErrorHandler(), func(c *gin.Context) {
				ctx := context.WithValue(c.Request.Context(), types.TenantIDContextKey, uint64(1))
				ctx = context.WithValue(ctx, types.TenantRoleContextKey, types.TenantRoleContributor)
				ctx = context.WithValue(ctx, types.UserIDContextKey, "user")
				c.Request = c.Request.WithContext(ctx)
				c.Next()
			})
			r.POST("/write", func(c *gin.Context) {
				if err := h.requireKBOwnershipOrAdmin(c, "kb"); err != nil {
					_ = c.Error(err)
					return
				}
				c.Status(http.StatusNoContent)
			})
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/write", nil))
			require.Equal(t, tt.status, w.Code, w.Body.String())
			require.Equal(t, 1, calls)
		})
	}
}

func TestBodyWriteStillEnforcesAPIKeyKBScope(t *testing.T) {
	c := newKBLookupCtx(t, 1, "kb")
	c.Set(types.TenantIDContextKey.String(), uint64(1))
	ctx := types.WithTenantAPIKeyScope(
		c.Request.Context(),
		types.TenantAPIKeyScope{KnowledgeBaseIDs: types.StringArray{"allowed"}},
	)
	c.Request = c.Request.WithContext(ctx)
	h := &KnowledgeHandler{}
	_, _, err := h.requireKnowledgeWriteAccess(c, "outside-scope")
	require.Error(t, err, "skipping human ownership checks must not skip KB scope")
}
