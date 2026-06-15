package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

type evaluationCompatibilityService struct {
	interfaces.EvaluationService
	detail *types.EvaluationDetail
}

func (s *evaluationCompatibilityService) Evaluation(context.Context, string, string, string, string) (*types.EvaluationDetail, error) {
	return s.detail, nil
}

func (s *evaluationCompatibilityService) EvaluationResult(context.Context, string) (*types.EvaluationDetail, error) {
	return s.detail, nil
}

func TestEvaluationHandlerLegacyProjectionShapes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	detail := &types.EvaluationDetail{Task: &types.EvaluationTask{ID: "run-id"}, Params: &types.ChatManage{}, Metric: &types.MetricResult{}}
	handler := NewEvaluationHandler(&evaluationCompatibilityService{detail: detail})
	router := gin.New()
	router.POST("/api/v1/evaluation/", handler.Evaluation)
	router.GET("/api/v1/evaluation/", handler.GetEvaluationResult)

	post := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/evaluation/", bytes.NewBufferString(`{"dataset_id":"default","knowledge_base_id":"kb","chat_id":"chat","rerank_id":"rerank"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(post, request)
	assertLegacyEvaluationResponse(t, post, false)

	get := httptest.NewRecorder()
	router.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/api/v1/evaluation/?task_id=run-id", nil))
	assertLegacyEvaluationResponse(t, get, true)
}

func assertLegacyEvaluationResponse(t *testing.T, recorder *httptest.ResponseRecorder, expectMetric bool) {
	t.Helper()
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Success bool                   `json:"success"`
		Data    map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.Success || response.Data["task"] == nil || response.Data["params"] == nil {
		t.Fatalf("unexpected response: %s", recorder.Body.String())
	}
	_, hasMetric := response.Data["metric"]
	if hasMetric != expectMetric {
		t.Fatalf("metric presence = %v, want %v", hasMetric, expectMetric)
	}
}
