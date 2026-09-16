package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubEvaluationLabelService struct {
	interfaces.EvaluationService
	gotTaskID string
	gotLabels []string
	labels    []string
	err       error
}

func (s *stubEvaluationLabelService) ReplaceEvaluationTaskLabels(
	_ context.Context,
	taskID string,
	labels []string,
) ([]string, error) {
	s.gotTaskID = taskID
	s.gotLabels = labels
	return s.labels, s.err
}

func newEvaluationLabelTestRouter(svc interfaces.EvaluationService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.ErrorHandler())
	router.Use(func(c *gin.Context) {
		ctx := context.WithValue(c.Request.Context(), types.TenantIDContextKey, uint64(7))
		c.Request = c.Request.WithContext(ctx)
		c.Set(string(types.TenantIDContextKey), uint64(7))
		c.Next()
	})
	h := NewEvaluationHandler(svc)
	router.PUT("/api/v1/evaluation/tasks/:task_id/labels", h.ReplaceEvaluationTaskLabels)
	return router
}

func TestReplaceEvaluationTaskLabelsHandlerReturnsNormalizedLabels(t *testing.T) {
	svc := &stubEvaluationLabelService{labels: []string{"baseline", "检索"}}
	request := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/evaluation/tasks/task-1/labels",
		strings.NewReader(`{"labels":[" Baseline ","检索"]}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	newEvaluationLabelTestRouter(svc).ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Equal(t, "task-1", svc.gotTaskID)
	assert.Equal(t, []string{" Baseline ", "检索"}, svc.gotLabels)
	var body struct {
		Success bool `json:"success"`
		Data    struct {
			TaskID string   `json:"task_id"`
			Labels []string `json:"labels"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	assert.True(t, body.Success)
	assert.Equal(t, svc.labels, body.Data.Labels)
}

func TestReplaceEvaluationTaskLabelsHandlerHidesMissingTask(t *testing.T) {
	svc := &stubEvaluationLabelService{err: interfaces.ErrEvaluationTaskNotFound}
	request := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/evaluation/tasks/missing/labels",
		strings.NewReader(`{"labels":[]}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	newEvaluationLabelTestRouter(svc).ServeHTTP(response, request)
	assert.Equal(t, http.StatusNotFound, response.Code, response.Body.String())
}
