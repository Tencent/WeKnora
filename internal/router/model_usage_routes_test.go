package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeModelUsageService struct {
	called bool
	query  types.ModelUsageQuery
}

func (f *fakeModelUsageService) GetUsageReport(
	_ context.Context,
	query types.ModelUsageQuery,
) (*types.ModelUsageReport, error) {
	f.called = true
	f.query = query
	now := time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC)
	return &types.ModelUsageReport{
		Summary: types.ModelUsageSummary{
			WindowStart:    now.Add(-24 * time.Hour),
			WindowEnd:      now,
			RefreshSeconds: 5,
			SuccessRate:    1,
		},
		Models:       []types.ModelUsageModelStats{},
		Timeline:     []types.ModelUsageTimelinePoint{},
		RecentEvents: []types.ModelUsageEvent{},
	}, nil
}

func TestRegisterModelRoutesUsagePrecedesModelID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	usageSvc := &fakeModelUsageService{}
	rbacOff := false
	guards := &rbacGuards{cfg: &config.Config{Tenant: &config.TenantConfig{EnableRBAC: &rbacOff}}}

	RegisterModelRoutes(
		r.Group("/api/v1"),
		&handler.ModelHandler{},
		handler.NewModelUsageHandler(usageSvc),
		&handler.ModelCredentialsHandler{},
		guards,
	)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/models/usage?range=1h&model_type=Embedding", nil)
	ctx := req.Context()
	ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(1))
	ctx = context.WithValue(ctx, types.TenantRoleContextKey, types.TenantRoleViewer)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.True(t, usageSvc.called)
	assert.Equal(t, "1h", usageSvc.query.Range)
	assert.Equal(t, types.ModelTypeEmbedding, usageSvc.query.ModelType)
}
