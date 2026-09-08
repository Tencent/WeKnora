package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type evaluationHandlerServiceStub struct {
	detail *types.EvaluationDetail
	args   [4]string
}

func (s *evaluationHandlerServiceStub) Evaluation(
	_ context.Context, datasetID, knowledgeBaseID, chatModelID, rerankModelID string,
) (*types.EvaluationDetail, error) {
	s.args = [4]string{datasetID, knowledgeBaseID, chatModelID, rerankModelID}
	return s.detail, nil
}

func (s *evaluationHandlerServiceStub) EvaluationResult(_ context.Context, _ string) (*types.EvaluationDetail, error) {
	return s.detail, nil
}

func evaluationWireDetail(status types.EvaluationStatue) *types.EvaluationDetail {
	detail := &types.EvaluationDetail{
		Task: &types.EvaluationTask{
			ID: "evaluation-task", TenantID: 42, DatasetID: "benchmark_v1",
			StartTime: time.Unix(1, 0).UTC(), Status: status, Total: 2, Finished: 1,
		},
		Params: &types.ChatManage{PipelineRequest: types.PipelineRequest{
			VectorThreshold: .1, EmbeddingTopK: 10, RerankModelID: "rerank", ChatModelID: "chat",
		}},
	}
	if status == types.EvaluationStatueSuccess {
		detail.Metric = &types.MetricResult{
			RetrievalMetrics:  types.RetrievalMetrics{Precision: .25, Recall: 1},
			GenerationMetrics: types.GenerationMetrics{BLEU1: .5},
		}
	}
	if status == types.EvaluationStatueFailed {
		detail.Task.ErrMsg = "evaluation failed"
	}
	return detail
}

func decodeEvaluationResponse(t *testing.T, recorder *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	return body
}

func TestEvaluationHandlerPOSTWireCompatibility(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &evaluationHandlerServiceStub{detail: evaluationWireDetail(types.EvaluationStatuePending)}
	handler := NewEvaluationHandler(stub)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set(string(types.TenantIDContextKey), uint64(42))
	ctx.Request = httptest.NewRequest(
		http.MethodPost, "/evaluation",
		strings.NewReader(
			`{"dataset_id":"benchmark_v1","knowledge_base_id":"kb","chat_id":"chat","rerank_id":"rerank"}`,
		),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")

	handler.Evaluation(ctx)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, [4]string{"benchmark_v1", "kb", "chat", "rerank"}, stub.args)
	body := decodeEvaluationResponse(t, recorder)
	require.Equal(t, true, body["success"])
	data := body["data"].(map[string]interface{})
	task := data["task"].(map[string]interface{})
	require.Equal(t, float64(0), task["status"])
	require.Contains(t, data, "params")
}

func TestEvaluationHandlerGETStatusWireCompatibility(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for status := types.EvaluationStatuePending; status <= types.EvaluationStatueFailed; status++ {
		t.Run(fmt.Sprintf("status_%d", status), func(t *testing.T) {
			stub := &evaluationHandlerServiceStub{detail: evaluationWireDetail(status)}
			handler := NewEvaluationHandler(stub)
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodGet, "/evaluation?task_id=evaluation-task", nil)

			handler.GetEvaluationResult(ctx)
			require.Equal(t, http.StatusOK, recorder.Code)
			data := decodeEvaluationResponse(t, recorder)["data"].(map[string]interface{})
			task := data["task"].(map[string]interface{})
			require.Equal(t, float64(status), task["status"])
			require.Contains(t, data, "params")
			if status == types.EvaluationStatueSuccess {
				require.Contains(t, data, "metric")
			} else {
				require.NotContains(t, data, "metric")
			}
			if status == types.EvaluationStatueFailed {
				require.Equal(t, "evaluation failed", task["err_msg"])
			}
		})
	}
}
