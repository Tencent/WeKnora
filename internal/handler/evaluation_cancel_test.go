package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type stubEvaluationCancelService struct {
	interfaces.EvaluationService
	detail *types.EvaluationDetail
	err    error

	gotTenant uint64
	gotTaskID string
	calls     int
}

func (s *stubEvaluationCancelService) CancelEvaluation(
	ctx context.Context,
	taskID string,
) (*types.EvaluationDetail, error) {
	s.calls++
	s.gotTaskID = taskID
	tenantID, _ := ctx.Value(types.TenantIDContextKey).(uint64)
	s.gotTenant = tenantID
	if s.err != nil {
		return nil, s.err
	}
	return s.detail, nil
}

func newEvaluationCancelTestRouter(svc interfaces.EvaluationService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	r.Use(func(c *gin.Context) {
		ctx := context.WithValue(c.Request.Context(), types.TenantIDContextKey, uint64(7))
		c.Request = c.Request.WithContext(ctx)
		c.Set(string(types.TenantIDContextKey), uint64(7))
		c.Next()
	})
	h := NewEvaluationHandler(svc)
	r.POST("/api/v1/evaluation/:task_id/cancel", h.CancelEvaluation)
	return r
}

func TestCancelEvaluationHandlerReturnsCurrentTask(t *testing.T) {
	svc := &stubEvaluationCancelService{
		detail: &types.EvaluationDetail{
			Task: &types.EvaluationTask{
				ID:        "evaluation-cancel-task",
				TenantID:  7,
				DatasetID: "dataset",
				Status:    types.EvaluationStatueRunning,
			},
			Params: &types.ChatManage{},
		},
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/evaluation/evaluation-cancel-task/cancel", nil)
	newEvaluationCancelTestRouter(svc).ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	assert.Equal(t, 1, svc.calls)
	assert.Equal(t, uint64(7), svc.gotTenant)
	assert.Equal(t, "evaluation-cancel-task", svc.gotTaskID)

	var response struct {
		Success bool                    `json:"success"`
		Data    *types.EvaluationDetail `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.True(t, response.Success)
	require.NotNil(t, response.Data)
	assert.Equal(t, "evaluation-cancel-task", response.Data.Task.ID)
}

func TestCancelEvaluationHandlerMapsNotFoundTo404(t *testing.T) {
	svc := &stubEvaluationCancelService{err: interfaces.ErrEvaluationTaskNotFound}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/evaluation/missing-task/cancel", nil)
	newEvaluationCancelTestRouter(svc).ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code, "body=%s", w.Body.String())
}

func TestCancelEvaluationHandlerMapsInternalErrorTo500(t *testing.T) {
	svc := &stubEvaluationCancelService{err: errors.New("database unavailable")}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/evaluation/some-task/cancel", nil)
	newEvaluationCancelTestRouter(svc).ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code, "body=%s", w.Body.String())
}
