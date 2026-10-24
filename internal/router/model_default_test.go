package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type defaultSelectionService struct {
	interfaces.ModelService
	err    error
	calls  int
	tenant uint64
}

func (s *defaultSelectionService) SetDefaultModel(ctx context.Context, id string) (*types.Model, error) {
	s.calls++
	s.tenant = types.MustTenantIDFromContext(ctx)
	return &types.Model{
		ID: id, Type: types.ModelTypeKnowledgeQA, IsBuiltin: true, IsDefault: true,
		Parameters: types.ModelParameters{APIKey: "stored-secret"},
	}, s.err
}

func TestSetDefaultModelRouteRequiresWorkspaceAdminAndRedactsSecrets(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		role   types.TenantRole
		status int
	}{
		{types.TenantRoleViewer, http.StatusForbidden},
		{types.TenantRoleContributor, http.StatusForbidden},
		{types.TenantRoleAdmin, http.StatusOK},
		{types.TenantRoleOwner, http.StatusOK},
	} {
		t.Run(string(tc.role), func(t *testing.T) {
			enforce := true
			g := &rbacGuards{cfg: &config.Config{Tenant: &config.TenantConfig{EnableRBAC: &enforce}}}
			stub := &defaultSelectionService{}
			router := gin.New()
			router.Use(func(c *gin.Context) {
				ctx := types.WithCaller(c.Request.Context(), types.Caller{TenantID: 7, UserID: "admin", Role: tc.role})
				c.Request = c.Request.WithContext(context.WithValue(ctx, types.TenantIDContextKey, uint64(7)))
			})
			RegisterModelRoutes(router.Group("/api/v1"), handler.NewModelHandler(stub), nil, g)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/v1/models/builtin/default", nil))
			require.Equal(t, tc.status, rec.Code, rec.Body.String())
			require.NotContains(t, rec.Body.String(), "stored-secret")
			if tc.status == http.StatusOK {
				require.Equal(t, uint64(7), stub.tenant)
				require.Contains(t, rec.Body.String(), `"is_default":true`)
			} else {
				require.Zero(t, stub.calls)
			}
		})
	}
}

func TestSetDefaultModelRouteReportsUnavailableModel(t *testing.T) {
	stub := &defaultSelectionService{err: apperrors.NewBadRequestError("only active models can be selected as default")}
	router := gin.New()
	router.Use(middleware.ErrorHandler())
	router.PUT("/models/:id/default", handler.NewModelHandler(stub).SetDefaultModel)
	req := httptest.NewRequest(http.MethodPut, "/models/failed/default", nil)
	req = req.WithContext(context.WithValue(req.Context(), types.TenantIDContextKey, uint64(7)))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}
