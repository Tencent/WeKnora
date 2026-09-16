package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type modelStatisticsServiceStub struct {
	tenantID uint64
	modelIDs []string
	price    *types.ModelPriceVersion
}

func (s *modelStatisticsServiceStub) Usage(
	_ context.Context, tenantID uint64, modelIDs []string, _, _ *time.Time,
) (*types.ModelUsageResponse, error) {
	s.tenantID, s.modelIDs = tenantID, modelIDs
	return &types.ModelUsageResponse{From: time.Now(), To: time.Now(), Items: []types.ModelUsageStatistics{}}, nil
}

func (*modelStatisticsServiceStub) Prices(context.Context, uint64, string) ([]*types.ModelPriceVersion, error) {
	return []*types.ModelPriceVersion{}, nil
}

func (s *modelStatisticsServiceStub) PutPrice(
	_ context.Context, tenantID uint64, modelID string, price *types.ModelPriceVersion,
) error {
	s.tenantID = tenantID
	price.TenantID = tenantID
	price.ModelID = modelID
	s.price = price
	return nil
}

func TestModelStatisticsHandlerBindsSingleModelUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &modelStatisticsServiceStub{}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/models/model-1/usage", nil)
	c.Params = gin.Params{{Key: "id", Value: "model-1"}}
	c.Set(types.TenantIDContextKey.String(), uint64(7))
	(&ModelStatisticsHandler{service: stub}).GetUsage(c)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, uint64(7), stub.tenantID)
	require.Equal(t, []string{"model-1"}, stub.modelIDs)
}

func TestModelStatisticsHandlerPutPriceUsesPathAndTenant(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &modelStatisticsServiceStub{}
	body, err := json.Marshal(map[string]any{
		"valid_from": "2026-09-01T00:00:00Z", "currency": "USD",
		"input_microunits_per_million": 1, "output_microunits_per_million": 2,
	})
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/models/model-1/pricing", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "id", Value: "model-1"}}
	c.Set(types.TenantIDContextKey.String(), uint64(7))
	(&ModelStatisticsHandler{service: stub}).PutPrice(c)
	require.Equal(t, http.StatusCreated, recorder.Code)
	require.NotNil(t, stub.price)
	require.Equal(t, "model-1", stub.price.ModelID)
	require.Equal(t, uint64(7), stub.price.TenantID)
}
