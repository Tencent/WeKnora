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

func (memoryFailureService) GetEpisode(context.Context, string) (*types.MemoryEpisode, error) {
	return nil, fmt.Errorf("load: %w", memory.ErrNotFound)
}

func (memoryFailureService) AddNote(context.Context, string) (*types.MemoryNote, error) {
	return nil, fmt.Errorf("add: %w", memory.ErrNotesFull)
}

func (memoryFailureService) SaveProfile(context.Context, string) (int64, error) {
	return 0, fmt.Errorf("save: %w", memory.ErrContentTooLong)
}

// A refusal the user can act on has to arrive as a 4xx. A full note store and
// an over-long profile are both states they can fix themselves, and reporting
// either as a server error would send them to support instead.
func TestMemoryRefusalsReachTheCallerAsClientErrors(t *testing.T) {
	router := gin.New()
	router.Use(middleware.ErrorHandler())
	handler := NewMemoryHandler(memoryFailureService{})
	router.GET("/memory/episodes/:id", handler.GetEpisode)
	router.POST("/memory/notes", handler.CreateNote)
	router.PUT("/memory/profile", handler.SaveProfile)
	for _, tc := range []struct {
		name               string
		method, path, body string
		status             int
	}{
		{"another subject's episode", http.MethodGet, "/memory/episodes/someone-else", "", http.StatusNotFound},
		{"note store full", http.MethodPost, "/memory/notes", `{"content":"我只用中文"}`, http.StatusBadRequest},
		{"profile too long", http.MethodPut, "/memory/profile", `{"body":"很长的画像"}`, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			request.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(recorder, request)
			require.Equal(t, tc.status, recorder.Code, recorder.Body.String())
			require.Contains(t, recorder.Body.String(), `"success":false`)
		})
	}
}
