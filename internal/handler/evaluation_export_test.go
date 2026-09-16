package handler

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func setupEvaluationExportHandler(svc *stubEvaluationService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(middleware.ErrorHandler())
	handler := NewEvaluationHandler(svc)
	engine.GET("/api/v1/evaluation/tasks/:task_id/export", handler.ExportEvaluationTask)
	return engine
}

func TestExportEvaluationTaskStreamsPreparedFileAndRemovesIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prepared.json")
	payload := []byte(`{"schema_version":1}`)
	require.NoError(t, os.WriteFile(path, payload, 0o600))
	svc := &stubEvaluationService{exportResult: &types.EvaluationPreparedExport{
		Path: path, Filename: "evaluation-task-a.json", ContentType: "application/json; charset=utf-8",
		Size: int64(len(payload)),
	}}
	engine := setupEvaluationExportHandler(svc)
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/evaluation/tasks/task-a/export?format=json",
		nil,
	)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, payload, response.Body.Bytes())
	require.Contains(t, response.Header().Get("Content-Disposition"), "evaluation-task-a.json")
	_, err := os.Stat(path)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestExportEvaluationTaskMapsErrorsBeforeResponseHeaders(t *testing.T) {
	for _, fixture := range []struct {
		err  error
		want int
	}{
		{err: types.ErrEvaluationExportFormatInvalid, want: http.StatusBadRequest},
		{err: interfaces.ErrEvaluationTaskNotFound, want: http.StatusNotFound},
		{err: types.ErrEvaluationExportTaskConflict, want: http.StatusConflict},
		{err: types.ErrEvaluationExportLimitExceeded, want: http.StatusRequestEntityTooLarge},
		{err: errors.New("storage unavailable"), want: http.StatusInternalServerError},
	} {
		svc := &stubEvaluationService{err: fixture.err}
		engine := setupEvaluationExportHandler(svc)
		request := httptest.NewRequest(
			http.MethodGet,
			"/api/v1/evaluation/tasks/task-a/export?format=json",
			nil,
		)
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		require.Equal(t, fixture.want, response.Code)
		require.NotEqual(t, "attachment", response.Header().Get("Content-Disposition"))
	}
}
