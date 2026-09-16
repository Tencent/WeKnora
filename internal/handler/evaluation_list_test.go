package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type stubEvaluationListService struct {
	interfaces.EvaluationService
	page *types.EvaluationTaskListPage
	err  error

	gotInput types.EvaluationTaskListInput
}

func (s *stubEvaluationListService) ListEvaluations(
	_ context.Context,
	input types.EvaluationTaskListInput,
) (*types.EvaluationTaskListPage, error) {
	s.gotInput = input
	if s.err != nil {
		return nil, s.err
	}
	return s.page, nil
}

func newEvaluationListTestRouter(svc interfaces.EvaluationService) *gin.Engine {
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
	r.GET("/api/v1/evaluation/tasks", h.ListEvaluationTasks)
	return r
}

func TestListEvaluationTasksHandlerReturnsNestedPage(t *testing.T) {
	startTime := time.Date(2026, 8, 28, 9, 0, 0, 0, time.UTC)
	svc := &stubEvaluationListService{
		page: &types.EvaluationTaskListPage{
			Items: []*types.EvaluationTaskEntity{
				{
					ID:            "evaluation-list-1",
					TenantID:      7,
					DatasetID:     "dataset",
					Status:        types.EvaluationStatueSuccess,
					StartTime:     startTime,
					CleanupErrors: types.JSON(`[]`),
					Params:        types.JSON(`{}`),
				},
			},
			NextCursor: "cursor-next",
		},
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/evaluation/tasks?status=2&dataset_id=dataset&dataset_version_id=version&"+
			"model_id=chat&started_from=2026-08-28T01%3A00%3A00Z&started_to=2026-08-29T01%3A00%3A00Z&"+
			"label=baseline&label=retrieval&page_size=5&cursor=abc",
		nil,
	)
	newEvaluationListTestRouter(svc).ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	require.NotNil(t, svc.gotInput.Status)
	assert.Equal(t, types.EvaluationStatueSuccess, *svc.gotInput.Status)
	assert.Equal(t, 5, svc.gotInput.PageSize)
	assert.Equal(t, "abc", svc.gotInput.Cursor)
	assert.Equal(t, "dataset", svc.gotInput.DatasetID)
	assert.Equal(t, "version", svc.gotInput.DatasetVersionID)
	assert.Equal(t, "chat", svc.gotInput.ModelID)
	assert.Equal(t, []string{"baseline", "retrieval"}, svc.gotInput.Labels)
	require.NotNil(t, svc.gotInput.StartedFrom)
	require.NotNil(t, svc.gotInput.StartedTo)

	var response struct {
		Success bool `json:"success"`
		Data    struct {
			Items      []types.EvaluationTask `json:"items"`
			NextCursor string                 `json:"next_cursor"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.True(t, response.Success)
	require.Len(t, response.Data.Items, 1)
	assert.Equal(t, "evaluation-list-1", response.Data.Items[0].ID)
	assert.Equal(t, "cursor-next", response.Data.NextCursor)
}

func TestListEvaluationTasksHandlerRejectsInvalidCursorAndStatus(t *testing.T) {
	svc := &stubEvaluationListService{err: service.ErrEvaluationTaskListInvalidCursor}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/evaluation/tasks?cursor=broken", nil)
	newEvaluationListTestRouter(svc).ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/evaluation/tasks?status=abc", nil)
	newEvaluationListTestRouter(&stubEvaluationListService{}).ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
}

func TestListEvaluationTasksHandlerMapsInternalErrorTo500(t *testing.T) {
	svc := &stubEvaluationListService{err: errors.New("database unavailable")}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/evaluation/tasks", nil)
	newEvaluationListTestRouter(svc).ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code, "body=%s", w.Body.String())
}
