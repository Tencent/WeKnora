package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type evaluationMetricHandlerService struct {
	interfaces.EvaluationService
	definitions []types.EvaluationMetricDefinition
}

func (s *evaluationMetricHandlerService) EvaluationMetricDefinitions() []types.EvaluationMetricDefinition {
	return s.definitions
}

func TestListEvaluationMetricsReturnsVersionedDefinitions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &evaluationMetricHandlerService{definitions: []types.EvaluationMetricDefinition{{
		Key: "retrieval.ndcg", Version: "1.0.0", Kind: "retrieval",
		DefaultConfig: json.RawMessage(`{"k":3}`), ConfigSchema: json.RawMessage(`{"type":"object"}`),
	}}}
	handler := NewEvaluationHandler(service)
	router := gin.New()
	router.GET("/api/v1/evaluation/metrics", handler.ListEvaluationMetrics)

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/evaluation/metrics", nil))
	require.Equal(t, http.StatusOK, response.Code)
	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			Items []types.EvaluationMetricDefinition `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
	require.True(t, payload.Success)
	require.Equal(t, "retrieval.ndcg", payload.Data.Items[0].Key)
}
