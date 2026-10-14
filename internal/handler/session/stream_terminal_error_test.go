package session

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/storageurl"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAgentSSEStopsAfterTerminalError(t *testing.T) {
	for _, tc := range []struct {
		name         string
		waitForTitle bool
	}{
		{name: "existing session"},
		{name: "new session awaiting title", waitForTitle: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &Handler{streamManager: &stubStreamManager{events: []interfaces.StreamEvent{
				{ID: "error-1", Type: types.ResponseTypeError, Content: "model unavailable", Done: true},
			}}}
			c, recorder := newTestGinContext(t, "")
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			c.Request = c.Request.WithContext(ctx)

			h.handleAgentEventsForSSE(ctx, c, "sess-1", "msg-1", "req-1", nil, tc.waitForTitle,
				storageurl.NewStreamRewriter(nil))
			require.NoError(t, ctx.Err(), "terminal error must close the live SSE stream")
			require.Contains(t, recorder.Body.String(), `"response_type":"error"`)
		})
	}
}

func errorStreamRouter(manager interfaces.StreamManager) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	h := &Handler{
		sessionService: &stubSessionService{}, messageService: &stubMessageServiceForStream{},
		streamManager: manager, fileService: &stubResourceFileService{},
	}
	r.GET("/sessions/continue-stream/:session_id", h.ContinueStream)
	return r
}

func TestContinueStreamStopsWhenReplayedErrorIsTerminal(t *testing.T) {
	manager := &stubStreamManager{events: []interfaces.StreamEvent{
		{ID: "error-1", Type: types.ResponseTypeError, Content: "model unavailable", Done: true},
	}}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet,
		"/sessions/continue-stream/sess-1?message_id=msg-1", nil).WithContext(ctx)
	recorder := httptest.NewRecorder()

	errorStreamRouter(manager).ServeHTTP(recorder, req)
	require.NoError(t, ctx.Err(), "replayed terminal error must close the resumed stream")
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"response_type":"error"`)
}

type delayedErrorStreamManager struct{ *stubStreamManager }

func (s *delayedErrorStreamManager) GetEvents(
	_ context.Context, _, _ string, fromOffset int,
) ([]interfaces.StreamEvent, int, error) {
	switch fromOffset {
	case 0:
		return []interfaces.StreamEvent{{ID: "query-1", Type: types.ResponseTypeAgentQuery}}, 1, nil
	case 1:
		return []interfaces.StreamEvent{{ID: "error-1", Type: types.ResponseTypeError,
			Content: "model unavailable", Done: true}}, 2, nil
	default:
		return nil, 2, nil
	}
}

func TestContinueStreamStopsWhenNewErrorIsTerminal(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet,
		"/sessions/continue-stream/sess-1?message_id=msg-1", nil).WithContext(ctx)
	recorder := httptest.NewRecorder()

	errorStreamRouter(&delayedErrorStreamManager{&stubStreamManager{}}).ServeHTTP(recorder, req)
	require.NoError(t, ctx.Err(), "new terminal error must close the resumed stream")
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"response_type":"error"`)
}
