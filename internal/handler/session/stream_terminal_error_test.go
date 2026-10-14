package session

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/storageurl"
	"github.com/Tencent/WeKnora/internal/stream"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAgentSSEStopsAfterTerminalError(t *testing.T) {
	h := &Handler{streamManager: &stubStreamManager{events: []interfaces.StreamEvent{
		{ID: "error-1", Type: types.ResponseTypeError, Content: "model unavailable", Done: true},
	}}}
	c, recorder := newTestGinContext(t, "")
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	c.Request = c.Request.WithContext(ctx)

	h.handleAgentEventsForSSE(ctx, c, "sess-1", "msg-1", "req-1", nil, false,
		storageurl.NewStreamRewriter(nil))
	require.NoError(t, ctx.Err(), "terminal error must close the live SSE stream")
	require.Contains(t, recorder.Body.String(), `"response_type":"error"`)
}

type completionPollingStream struct {
	interfaces.StreamManager
	errorRead      chan struct{}
	afterFirstRead func()
	firstRead      sync.Once
	errorOnce      sync.Once
}

func (s *completionPollingStream) GetEvents(
	ctx context.Context, sessionID, messageID string, offset int,
) ([]interfaces.StreamEvent, int, error) {
	events, next, err := s.StreamManager.GetEvents(ctx, sessionID, messageID, offset)
	s.firstRead.Do(func() {
		if s.afterFirstRead != nil {
			s.afterFirstRead()
		}
	})
	for _, evt := range events {
		if evt.Type == types.ResponseTypeError {
			s.errorOnce.Do(func() { close(s.errorRead) })
		}
	}
	return events, next, err
}

func TestErrorStreamWaitsForPersistedCompletion(t *testing.T) {
	for _, tc := range []struct {
		mode      string
		saveFails bool
	}{
		{mode: "live"},
		{mode: "replay"},
		{mode: "new events"},
		{mode: "live", saveFails: true},
		{mode: "replay", saveFails: true},
		{mode: "new events", saveFails: true},
	} {
		t.Run(tc.mode+map[bool]string{false: "/saved", true: "/save failed"}[tc.saveFails], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			manager := &completionPollingStream{
				StreamManager: stream.NewMemoryStreamManager(), errorRead: make(chan struct{}),
			}
			message := &types.Message{
				ID: "msg-1", SessionID: "sess-1", Role: "assistant", Content: "generated result",
				Artifacts: types.MessageArtifacts{{FileName: "report.txt", URL: "resource://saved-report"}},
			}
			messages := &imageCompletionMessages{beforeSave: func() {
				select {
				case <-manager.errorRead:
				case <-ctx.Done():
				}
			}}
			if tc.saveFails {
				messages.err = errors.New("database unavailable")
			}
			h := &Handler{messageService: messages, streamManager: manager}
			streamHandler := NewAgentStreamHandler(ctx, "sess-1", "msg-1", "req-1", 1,
				time.Now(), message, manager, nil, nil, nil, nil)
			// The engine prepares completion before reporting its execution error.
			require.NoError(t, streamHandler.handleComplete(ctx, event.Event{
				Data: event.AgentCompleteData{MessageID: "msg-1", Usage: &types.TokenUsage{TotalTokens: 17}},
			}))
			emitError := func() {
				require.NoError(t, streamHandler.handleError(ctx, event.Event{
					ID: "error-1", Data: event.ErrorData{Stage: "agent_execution", Error: "model unavailable"},
				}))
			}
			if tc.mode == "new events" {
				require.NoError(t, manager.AppendEvent(ctx, "sess-1", "msg-1", interfaces.StreamEvent{
					ID: "query-1", Type: types.ResponseTypeAgentQuery,
				}))
				manager.afterFirstRead = emitError
			} else {
				emitError()
			}
			saved := make(chan struct{})
			go func() {
				defer close(saved)
				h.completeStreamAssistantMessage(types.WithExecutionTenant(ctx, 1), &sseStreamContext{
					streamHandler: streamHandler, assistantMessage: message,
				}, "", "")
			}()
			var recorder *httptest.ResponseRecorder
			if tc.mode == "live" {
				c, response := newTestGinContext(t, "")
				recorder = response
				c.Request = c.Request.WithContext(ctx)
				h.handleAgentEventsForSSE(ctx, c, "sess-1", "msg-1", "req-1", nil, false,
					storageurl.NewStreamRewriter(nil))
			} else {
				recorder = httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodGet,
					"/sessions/continue-stream/sess-1?message_id=msg-1", nil).WithContext(ctx)
				errorStreamRouter(manager).ServeHTTP(recorder, req)
			}
			select {
			case <-saved:
			case <-ctx.Done():
				t.Fatal("message persistence did not finish")
			}
			require.NoError(t, ctx.Err())
			body := recorder.Body.String()
			require.Contains(t, body, `"response_type":"error"`)
			require.NotContains(t, body, "completion_pending", "internal state must not change the SSE payload")
			if tc.saveFails {
				require.Nil(t, messages.saved)
				require.Contains(t, body, `"stage":"message_persistence"`)
				require.NotContains(t, body, `"response_type":"complete"`)
				return
			}
			require.NotNil(t, messages.saved)
			require.Contains(t, body, `"response_type":"complete"`)
			require.Contains(t, body, `"final_content":"generated result"`)
			require.Contains(t, body, `"file_name":"report.txt"`)
			require.Contains(t, body, `"total_tokens":17`)
			require.Less(t, indexOf(body, `"response_type":"error"`), indexOf(body, `"response_type":"complete"`))
		})
	}
}

type followingStreamManager struct {
	*stubStreamManager
	first     interfaces.StreamEvent
	following []interfaces.StreamEvent
}

func (s *followingStreamManager) GetEvents(
	_ context.Context, _, _ string, offset int,
) ([]interfaces.StreamEvent, int, error) {
	if offset == 0 {
		return []interfaces.StreamEvent{s.first}, 1, nil
	}
	if offset == 1 {
		return s.following, 1 + len(s.following), nil
	}
	return nil, offset, nil
}

func TestAgentSSEWaitsForLateTitle(t *testing.T) {
	for _, kind := range []types.ResponseType{types.ResponseTypeError, types.ResponseTypeComplete} {
		t.Run(string(kind), func(t *testing.T) {
			manager := &followingStreamManager{
				stubStreamManager: &stubStreamManager{},
				first:             interfaces.StreamEvent{ID: "end-1", Type: kind, Done: true},
				following: []interfaces.StreamEvent{
					{ID: "title-1", Type: types.ResponseTypeSessionTitle, Content: "Generated title", Done: true},
				},
			}
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			c, recorder := newTestGinContext(t, "")
			c.Request = c.Request.WithContext(ctx)
			(&Handler{streamManager: manager}).handleAgentEventsForSSE(ctx, c, "sess-1", "msg-1", "req-1",
				nil, true, storageurl.NewStreamRewriter(nil))
			require.NoError(t, ctx.Err())
			require.Contains(t, recorder.Body.String(), `"response_type":"session_title"`)
			require.Contains(t, recorder.Body.String(), "Generated title")
		})
	}
}

func TestAgentSSETerminalErrorTitleWaitIsBounded(t *testing.T) {
	manager := &stubStreamManager{events: []interfaces.StreamEvent{
		{ID: "error-1", Type: types.ResponseTypeError, Done: true},
	}}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	c, _ := newTestGinContext(t, "")
	c.Request = c.Request.WithContext(ctx)
	(&Handler{streamManager: manager}).handleAgentEventsForSSE(ctx, c, "sess-1", "msg-1", "req-1", nil,
		true, storageurl.NewStreamRewriter(nil))
	require.NoError(t, ctx.Err(), "a missing title must not keep an errored stream open forever")
}

func TestNonTerminalErrorKeepsStreamOpen(t *testing.T) {
	for _, live := range []bool{false, true} {
		t.Run(map[bool]string{false: "replay", true: "live"}[live], func(t *testing.T) {
			manager := &followingStreamManager{
				stubStreamManager: &stubStreamManager{},
				first:             interfaces.StreamEvent{ID: "error-1", Type: types.ResponseTypeError, Done: false},
				following: []interfaces.StreamEvent{
					{ID: "answer-1", Type: types.ResponseTypeAnswer, Content: "continued answer"},
					{ID: "complete-1", Type: types.ResponseTypeComplete, Done: true},
				},
			}
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			c, recorder := newTestGinContext(t, "")
			c.Request = c.Request.WithContext(ctx)
			if live {
				(&Handler{streamManager: manager}).handleAgentEventsForSSE(ctx, c, "s", "m", "r", nil,
					false, storageurl.NewStreamRewriter(nil))
			} else {
				req := httptest.NewRequest(http.MethodGet,
					"/sessions/continue-stream/sess-1?message_id=msg-1", nil).WithContext(ctx)
				errorStreamRouter(manager).ServeHTTP(recorder, req)
			}
			require.NoError(t, ctx.Err())
			require.Contains(t, recorder.Body.String(), "continued answer")
			require.Contains(t, recorder.Body.String(), `"response_type":"complete"`)
		})
	}
}

func TestExecutionErrorCompletionStateSurvivesSerialization(t *testing.T) {
	streams := &capturingStreamManager{}
	h := NewAgentStreamHandler(t.Context(), "s", "m", "r", 1, time.Now(),
		&types.Message{ID: "m"}, streams, nil, nil, nil, nil)
	require.NoError(t, h.handleComplete(t.Context(), event.Event{Data: event.AgentCompleteData{MessageID: "m"}}))
	require.NoError(t, h.handleError(t.Context(), event.Event{
		Data: event.ErrorData{Stage: "agent_execution", Error: "failed"},
	}))
	raw, err := json.Marshal(streams.events[len(streams.events)-1])
	require.NoError(t, err)
	require.Contains(t, string(raw), `"completion_pending":true`)
	var restored interfaces.StreamEvent
	require.NoError(t, json.Unmarshal(raw, &restored))
	response, err := json.Marshal(buildStreamResponse(restored, "r"))
	require.NoError(t, err)
	require.NotContains(t, string(response), "completion_pending")
	require.Contains(t, string(response), `"done":true`)
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
		return []interfaces.StreamEvent{{
			ID: "error-1", Type: types.ResponseTypeError,
			Content: "model unavailable", Done: true,
		}}, 2, nil
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
