package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func setupEvaluationComparisonHandler(svc *stubEvaluationService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(middleware.ErrorHandler())
	handler := NewEvaluationHandler(svc)
	engine.POST("/api/v1/evaluation/comparisons", handler.CompareEvaluationTasks)
	return engine
}

func TestCompareEvaluationTasksHandlerReturnsNestedData(t *testing.T) {
	svc := &stubEvaluationService{comparisonResult: &types.EvaluationComparisonResponse{
		SchemaVersion: 1, BaselineTaskID: "task-a",
		Runs:       []types.EvaluationComparisonRun{},
		Parameters: []types.EvaluationComparisonParameter{},
		Metrics:    []types.EvaluationComparisonMetric{},
	}}
	engine := setupEvaluationComparisonHandler(svc)
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/evaluation/comparisons",
		strings.NewReader(`{"task_ids":["task-a","task-b"],"baseline_task_id":"task-b"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code)
	require.JSONEq(t, `{
		"success":true,
		"data":{"schema_version":1,"baseline_task_id":"task-a","runs":[],"parameters":[],"metrics":[]}
	}`, response.Body.String())
	require.NotNil(t, svc.comparisonRequest)
	require.Equal(t, "task-b", svc.comparisonRequest.BaselineTaskID)
}

func TestCompareEvaluationTasksHandlerMapsDomainErrors(t *testing.T) {
	for _, fixture := range []struct {
		err  error
		want int
	}{
		{err: types.ErrEvaluationComparisonInvalid, want: http.StatusBadRequest},
		{err: types.ErrEvaluationComparisonTaskNotFound, want: http.StatusNotFound},
		{err: types.ErrEvaluationComparisonConflict, want: http.StatusConflict},
	} {
		svc := &stubEvaluationService{err: fixture.err}
		engine := setupEvaluationComparisonHandler(svc)
		request := httptest.NewRequest(
			http.MethodPost,
			"/api/v1/evaluation/comparisons",
			strings.NewReader(`{"task_ids":["task-a","task-b"]}`),
		)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		require.Equal(t, fixture.want, response.Code)
	}
}
