package session

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type resumeStreamMessageService struct {
	interfaces.MessageService
	completed bool
}

func (s *resumeStreamMessageService) GetMessage(
	_ context.Context, sessionID, messageID string,
) (*types.Message, error) {
	return &types.Message{ID: messageID, SessionID: sessionID, IsCompleted: s.completed}, nil
}

func TestContinueStreamWaitsForApprovedSegment(t *testing.T) {
	for _, tt := range []struct {
		name      string
		query     string
		marker    interfaces.StreamEvent
		completed bool
		status    int
	}{
		{name: "ordinary replay", status: http.StatusOK},
		{name: "old terminal segment", query: "&resume_pending_id=p", status: http.StatusConflict},
		{
			name: "different approval", query: "&resume_pending_id=p", status: http.StatusConflict,
			marker: interfaces.StreamEvent{
				Type: types.ResponseTypeAgentQuery, Data: map[string]interface{}{"resume_pending_id": "old"},
			},
		},
		{
			name: "automation resumed", query: "&resume_pending_id=p", status: http.StatusOK,
			marker: interfaces.StreamEvent{
				Type: types.ResponseTypeAgentQuery, Data: map[string]interface{}{"resume_pending_id": "p"},
			},
		},
		{
			name: "chat resumed", query: "&resume_pending_id=p", status: http.StatusOK,
			marker: interfaces.StreamEvent{
				Type: types.ResponseTypeRunResumed, Data: map[string]interface{}{"pending_id": "p"},
			},
		},
		{
			name: "terminal failure before resume marker", query: "&resume_pending_id=p",
			completed: true, status: http.StatusOK,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			r := gin.New()
			events := []interfaces.StreamEvent{}
			if tt.marker.Type != "" {
				events = append(events, tt.marker)
			}
			events = append(events, interfaces.StreamEvent{Type: types.ResponseTypeComplete, Done: true})
			h := &Handler{
				sessionService: &stubSessionService{},
				messageService: &resumeStreamMessageService{completed: tt.completed},
				streamManager:  &stubStreamManager{events: events},
			}
			r.GET("/sessions/continue-stream/:session_id", h.ContinueStream)
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/sessions/continue-stream/s?message_id=m"+tt.query, nil)
			r.ServeHTTP(recorder, request)
			require.Equal(t, tt.status, recorder.Code, recorder.Body.String())
			if tt.status == http.StatusConflict {
				require.NotContains(t, recorder.Body.String(), `"response_type":"complete"`)
				require.NotContains(t, recorder.Header().Get("Content-Type"), "text/event-stream")
			} else {
				require.Contains(t, recorder.Body.String(), `"response_type":"complete"`)
			}
		})
	}
}
