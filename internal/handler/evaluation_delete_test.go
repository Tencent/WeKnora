package handler

import (
	"context"
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

type stubEvaluationDeleteService struct {
	interfaces.EvaluationService
	err       error
	gotTaskID string
	calls     int
}

func (s *stubEvaluationDeleteService) DeleteEvaluation(_ context.Context, taskID string) error {
	s.calls++
	s.gotTaskID = taskID
	return s.err
}

func newEvaluationDeleteTestRouter(svc interfaces.EvaluationService) *gin.Engine {
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
	r.DELETE("/api/v1/evaluation/:task_id", h.DeleteEvaluation)
	return r
}

func TestDeleteEvaluationHandlerReturns204(t *testing.T) {
	svc := &stubEvaluationDeleteService{}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/evaluation/evaluation-terminal", nil)
	newEvaluationDeleteTestRouter(svc).ServeHTTP(w, req)

	require.Equal(t, http.StatusNoContent, w.Code, "body=%s", w.Body.String())
	assert.Equal(t, 1, svc.calls)
	assert.Equal(t, "evaluation-terminal", svc.gotTaskID)
}

func TestDeleteEvaluationHandlerRejectsActiveTaskWith409(t *testing.T) {
	svc := &stubEvaluationDeleteService{err: interfaces.ErrEvaluationTaskStateConflict}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/evaluation/evaluation-active", nil)
	newEvaluationDeleteTestRouter(svc).ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code, "body=%s", w.Body.String())
}

func TestDeleteEvaluationHandlerMapsInternalErrorTo500(t *testing.T) {
	svc := &stubEvaluationDeleteService{err: errors.New("database unavailable")}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/evaluation/some-task", nil)
	newEvaluationDeleteTestRouter(svc).ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code, "body=%s", w.Body.String())
}
