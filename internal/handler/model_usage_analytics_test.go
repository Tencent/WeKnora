package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type modelUsageAnalyticsServiceStub struct {
	query  types.ModelUsageAnalyticsQuery
	result *types.ModelUsageAnalyticsResult
}

func (s *modelUsageAnalyticsServiceStub) GetAnalytics(
	_ context.Context, query types.ModelUsageAnalyticsQuery,
) (*types.ModelUsageAnalyticsResult, error) {
	s.query = query
	return s.result, nil
}

func TestModelUsageAnalyticsHandlerValidation(t *testing.T) {
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		url  string
	}{
		{name: "invalid start RFC3339", url: "/api/v1/model-usage/analytics?start_time=nope"},
		{name: "invalid end RFC3339", url: "/api/v1/model-usage/analytics?end_time=nope"},
		{
			name: "start equals end",
			url: "/api/v1/model-usage/analytics?start_time=2026-09-05T00:00:00Z" +
				"&end_time=2026-09-05T00:00:00Z",
		},
		{
			name: "start after end",
			url: "/api/v1/model-usage/analytics?start_time=2026-09-06T00:00:00Z" +
				"&end_time=2026-09-05T00:00:00Z",
		},
		{name: "invalid interval", url: "/api/v1/model-usage/analytics?interval=week"},
		{name: "empty model ID", url: "/api/v1/model-usage/analytics?model_id="},
		{name: "tenant override", url: "/api/v1/model-usage/analytics?tenant_id=999"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stub := &modelUsageAnalyticsServiceStub{}
			h := NewModelUsageAnalyticsHandler(stub)
			h.now = func() time.Time { return now }
			router := gin.New()
			router.Use(middleware.ErrorHandler())
			router.GET("/api/v1/model-usage/analytics", h.GetAnalytics)
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, tc.url, nil))
			require.Equal(t, http.StatusBadRequest, recorder.Code)
			var body map[string]any
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
			require.Equal(t, false, body["success"])
		})
	}
}

func TestModelUsageAnalyticsHandlerDefaultsValidModelAndSerialization(t *testing.T) {
	gin.SetMode(gin.TestMode)
	now := time.Date(2026, 9, 5, 12, 34, 56, 789, time.UTC)
	stub := &modelUsageAnalyticsServiceStub{result: &types.ModelUsageAnalyticsResult{
		TimeBasis: types.ModelUsageAnalyticsTimeBasis,
		Interval:  types.ModelUsageAnalyticsIntervalDay,
		StartTime: time.Date(2026, 8, 6, 12, 34, 56, 0, time.UTC),
		EndTime:   time.Date(2026, 9, 5, 12, 34, 56, 0, time.UTC),
		ModelID:   "model-a",
		Summary: types.ModelUsageAnalyticsAggregate{
			Calls:          types.CallCounts{Total: 1, Chat: 1},
			CostByCurrency: []types.CurrencyCostAggregate{},
		},
		Trend: []types.ModelUsageAnalyticsBucket{{
			BucketStart: time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC),
			ModelUsageAnalyticsAggregate: types.ModelUsageAnalyticsAggregate{
				Calls:          types.CallCounts{Total: 1, Chat: 1},
				CostByCurrency: []types.CurrencyCostAggregate{},
			},
		}},
	}}
	h := NewModelUsageAnalyticsHandler(stub)
	h.now = func() time.Time { return now }
	router := gin.New()
	router.Use(middleware.ErrorHandler())
	router.GET("/api/v1/model-usage/analytics", h.GetAnalytics)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(
		http.MethodGet, "/api/v1/model-usage/analytics?model_id=model-a", nil,
	))

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "model-a", stub.query.ModelID)
	require.Equal(t, types.ModelUsageAnalyticsIntervalDay, stub.query.Interval)
	require.Equal(t, now.UTC().Truncate(time.Second), stub.query.EndTime)
	require.Equal(t, now.UTC().Truncate(time.Second).Add(-30*24*time.Hour), stub.query.StartTime)
	var body struct {
		Success bool                            `json:"success"`
		Data    types.ModelUsageAnalyticsResult `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	require.True(t, body.Success)
	require.Equal(t, "created_at", body.Data.TimeBasis)
	require.Equal(t, "model-a", body.Data.ModelID)
	require.Equal(t, int64(1), body.Data.Summary.Calls.Total)
	require.Len(t, body.Data.Trend, 1)
	require.Equal(t, time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC), body.Data.Trend[0].BucketStart)
	require.Equal(t, int64(1), body.Data.Trend[0].Calls.Total,
		"bucket metrics must serialize alongside bucket_start, not as raw usage rows")
}

func TestModelUsageAnalyticsHandlerExplicitOffsetTimesNormalizeToUTC(t *testing.T) {
	stub := &modelUsageAnalyticsServiceStub{result: &types.ModelUsageAnalyticsResult{}}
	h := NewModelUsageAnalyticsHandler(stub)
	router := gin.New()
	router.Use(middleware.ErrorHandler())
	router.GET("/api/v1/model-usage/analytics", h.GetAnalytics)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(
		http.MethodGet,
		"/api/v1/model-usage/analytics?start_time=2026-09-05T08:00:00%2B08:00"+
			"&end_time=2026-09-05T10:00:00%2B08:00&interval=hour",
		nil,
	))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC), stub.query.StartTime)
	require.Equal(t, time.Date(2026, 9, 5, 2, 0, 0, 0, time.UTC), stub.query.EndTime)
	require.Equal(t, types.ModelUsageAnalyticsIntervalHour, stub.query.Interval)
}
