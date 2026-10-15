package session

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestQAAssistantScopeValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, value := range []types.AssistantScope{"", "page", "knowledge_base", "workspace", "unknown", "PAGE"} {
		t.Run(string(value), func(t *testing.T) {
			sessions := &queryValidationSessionStub{}
			h := &Handler{sessionService: sessions}
			router := gin.New()
			router.Use(middleware.ErrorHandler())
			router.POST("/agent-chat/:session_id", h.AgentQA)
			body, err := json.Marshal(CreateKnowledgeQARequest{Query: "question", AssistantScope: value})
			require.NoError(t, err)
			request := httptest.NewRequest(http.MethodPost, "/agent-chat/session", bytes.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if !value.WithDefault().Valid() {
				require.Equal(t, http.StatusBadRequest, response.Code)
				require.Contains(t, response.Body.String(), "assistant_scope")
				require.Zero(t, sessions.lookupCalls)
			} else {
				require.Equal(t, 1, sessions.lookupCalls)
			}
		})
	}
}

func TestQAAssistantScopeDefaultAndPropagation(t *testing.T) {
	for _, value := range []types.AssistantScope{"", "page", "knowledge_base", "workspace"} {
		rc := &qaRequestContext{
			session:          &types.Session{ID: "s"},
			assistantMessage: &types.Message{ID: "a"},
			assistantScope:   value,
		}
		require.Equal(t, value.WithDefault(), rc.buildQARequest().AssistantScope)
	}
}
