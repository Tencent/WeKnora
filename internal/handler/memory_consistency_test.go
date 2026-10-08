package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/service/memory"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type memoryFailureService struct{ interfaces.MemoryService }

func (memoryFailureService) ConfirmItem(context.Context, string) (*types.MemoryItem, error) {
	return nil, fmt.Errorf("confirm: %w", types.ErrMemoryConflict)
}

func (memoryFailureService) UpdateItem(context.Context, string, string, int) (*types.MemoryItem, error) {
	return nil, fmt.Errorf("edit: %w", memory.ErrSensitiveContent)
}

func TestMemoryConsistencyHTTPFailures(t *testing.T) {
	router := gin.New()
	router.Use(middleware.ErrorHandler())
	handler := NewMemoryHandler(memoryFailureService{})
	router.POST("/memory/items/:id/confirm", handler.ConfirmItem)
	router.PUT("/memory/items/:id", handler.UpdateItem)
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{http.MethodPost, "/memory/items/stale/confirm", "", http.StatusConflict},
		{http.MethodPut, "/memory/items/item", `{"content":"sensitive"}`, http.StatusBadRequest},
	} {
		t.Run(tc.method, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			request.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(recorder, request)
			require.Equal(t, tc.status, recorder.Code, recorder.Body.String())
			require.Contains(t, recorder.Body.String(), `"success":false`)
		})
	}
}
