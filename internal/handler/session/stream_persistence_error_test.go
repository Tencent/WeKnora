package session

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func runPersistenceErrorStream(t *testing.T, mode string, events []interfaces.StreamEvent) string {
	t.Helper()
	var streams interfaces.StreamManager = &stubStreamManager{events: events}
	if mode == "poll" || mode == "live" {
		streams = &growingStreamManager{stubStreamManager: stubStreamManager{events: events}, visible: 1}
	}
	h := &Handler{
		sessionService: &stubSessionService{}, messageService: &stubMessageServiceForStream{},
		streamManager: streams, fileService: &stubResourceFileService{},
	}
	c, recorder := newTestGinContext(t, "?message_id=msg1&resource_urls=public")
	c.Params = gin.Params{{Key: "session_id", Value: "sess1"}}
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()
	c.Request = c.Request.WithContext(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		if mode == "live" {
			h.handleAgentEventsForSSE(ctx, c, "sess1", "msg1", "req-1", nil, true, publicStreamRewriter())
		} else {
			h.ContinueStream(c)
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("SSE handler did not stop after request cancellation")
		}
		t.Fatal("SSE kept polling after the final message-persistence failure")
	}
	require.NoError(t, ctx.Err(), "the stream must finish without disconnecting the client")
	require.Empty(t, c.Errors)
	return recorder.Body.String()
}

func TestStreamStopsAfterMessagePersistenceError(t *testing.T) {
	for _, mode := range []string{"live", "replay", "poll"} {
		t.Run(mode, func(t *testing.T) {
			ctx := types.WithExecutionTenant(context.Background(), 1)
			streams := &completionEventRecorder{}
			message := &types.Message{ID: "msg1", SessionID: "sess1", Role: "assistant"}
			h := &Handler{messageService: &imageCompletionMessages{err: errors.New("database unavailable")}}
			handler := NewAgentStreamHandler(ctx, "sess1", "msg1", "req-1", 1, time.Now(),
				message, streams, nil, nil, nil, nil)
			require.NoError(t, streams.AppendEvent(ctx, "sess1", "msg1", interfaces.StreamEvent{
				ID: "answer-1", Type: types.ResponseTypeAnswer, Content: "tail ![fig](resource://xifDo7",
			}))
			h.completeStreamAssistantMessage(ctx, &sseStreamContext{
				assistantMessage: message, streamHandler: handler,
			}, "", "")
			require.Len(t, streams.events, 2)
			require.Equal(t, "message_persistence", streams.events[1].Data["stage"])
			streams.events = append(streams.events, interfaces.StreamEvent{
				ID: "late-answer", Type: types.ResponseTypeAnswer, Content: "unexpected post-terminal content",
			})

			body := runPersistenceErrorStream(t, mode, streams.events)
			require.Contains(t, body, `"response_type":"error"`)
			require.Contains(t, body, "Failed to save assistant message")
			require.NotContains(t, body, `"response_type":"complete"`)
			require.NotContains(t, body, "unexpected post-terminal content")
			require.Contains(t, body, "![fig](resource://xifDo7", "buffered output must not be lost")
			require.Less(t, strings.LastIndex(body, `"response_type":"answer"`),
				strings.Index(body, `"response_type":"error"`))
		})
	}
}

func TestStreamKeepsOtherErrorsUntilCompletion(t *testing.T) {
	cases := []struct {
		name string
		kind types.ResponseType
		done bool
		data map[string]interface{}
	}{
		{"execution", types.ResponseTypeError, true, map[string]interface{}{"stage": "agent_execution"}},
		{"retrieval", types.ResponseTypeError, true, map[string]interface{}{"stage": "retrieval"}},
		{"nil metadata", types.ResponseTypeError, true, nil},
		{"missing stage", types.ResponseTypeError, true, map[string]interface{}{"error": "failed"}},
		{"unfinished", types.ResponseTypeError, false, map[string]interface{}{"stage": "message_persistence"}},
		{"tool result", types.ResponseTypeToolResult, true, map[string]interface{}{"stage": "message_persistence"}},
	}
	for _, mode := range []string{"live", "replay", "poll"} {
		for _, tc := range cases {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				events := []interfaces.StreamEvent{
					{ID: "error-1", Type: tc.kind, Done: tc.done, Data: tc.data},
					{
						ID: "title-1", Type: types.ResponseTypeSessionTitle,
						Data: map[string]interface{}{"title": "Conversation title"},
					},
					{
						ID: "complete-1", Type: types.ResponseTypeComplete, Done: true,
						Data: map[string]interface{}{
							"final_content": "saved final answer",
							"artifacts":     []map[string]interface{}{{"file_name": "report.txt"}},
						},
						Usage: &types.TokenUsage{TotalTokens: 7},
					},
				}
				body := runPersistenceErrorStream(t, mode, events)
				require.Contains(t, body, `"response_type":"complete"`)
				require.Contains(t, body, "saved final answer")
				require.Contains(t, body, "report.txt")
				require.Contains(t, body, `"total_tokens":7`)
			})
		}
	}
}
