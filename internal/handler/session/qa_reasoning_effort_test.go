package session

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestQAReasoningEffortValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, endpoint := range []string{"knowledge-chat", "agent-chat"} {
		for _, effort := range []string{"", "off", "auto", "high", "max", "unknown"} {
			t.Run(endpoint+"/"+effort, func(t *testing.T) {
				sessions := &queryValidationSessionStub{}
				h := &Handler{sessionService: sessions}
				router := gin.New()
				router.Use(middleware.ErrorHandler())
				router.POST("/knowledge-chat/:session_id", h.KnowledgeQA)
				router.POST("/agent-chat/:session_id", h.AgentQA)
				body, err := json.Marshal(CreateKnowledgeQARequest{Query: "question", ReasoningEffort: effort})
				require.NoError(t, err)
				request := httptest.NewRequest(http.MethodPost, "/"+endpoint+"/session", bytes.NewReader(body))
				request.Header.Set("Content-Type", "application/json")
				response := httptest.NewRecorder()
				router.ServeHTTP(response, request)
				if effort == "unknown" {
					require.Equal(t, http.StatusBadRequest, response.Code)
					require.Contains(t, response.Body.String(), "reasoning_effort")
					require.Zero(t, sessions.lookupCalls)
				} else {
					require.Equal(t, 1, sessions.lookupCalls)
				}
			})
		}
	}
}

type reasoningSessionStub struct {
	interfaces.SessionService
	state *types.SessionLastRequestState
}

func (s *reasoningSessionStub) UpdateSessionLastRequestState(
	_ context.Context, _ string, state *types.SessionLastRequestState,
) error {
	s.state = state
	return nil
}

func TestQAReasoningEffortPropagationAndRestore(t *testing.T) {
	for _, effort := range []string{"", "off", "high"} {
		t.Run(effort, func(t *testing.T) {
			rc := &qaRequestContext{
				sessionID: "session", session: &types.Session{ID: "session"},
				assistantMessage: &types.Message{ID: "answer"}, reasoningEffort: effort,
			}
			require.Equal(t, effort, rc.buildQARequest().ReasoningEffort)
			sessions := &reasoningSessionStub{}
			h := &Handler{sessionService: sessions}
			h.persistLastRequestState(context.Background(), rc, qaModeAgent)
			require.NotNil(t, sessions.state)
			raw, err := sessions.state.Value()
			require.NoError(t, err)
			var restored types.SessionLastRequestState
			require.NoError(t, restored.Scan(raw))
			require.Equal(t, effort, restored.ReasoningEffort)
		})
	}
}
