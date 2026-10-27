package session

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type automationConversationStub struct{ interfaces.SessionService }

func (*automationConversationStub) GetOwnedSession(_ context.Context, id string) (*types.Session, error) {
	return &types.Session{ID: id, Kind: types.SessionKindAutomation}, nil
}

func TestAutomationConversationRejectsManualMessages(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &Handler{sessionService: &automationConversationStub{}}
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	r.POST("/knowledge-chat/:session_id", h.KnowledgeQA)
	r.POST("/agent-chat/:session_id", h.AgentQA)
	r.POST("/sessions/:session_id/steer", h.SteerMessage)
	r.POST("/sessions/:session_id/steer/:steer_id/inject", h.PromoteSteerMessage)
	for _, path := range []string{
		"/knowledge-chat/session", "/agent-chat/session",
		"/sessions/session/steer", "/sessions/session/steer/queued/inject",
	} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"query":"manual message"}`))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
			require.Contains(t, w.Body.String(), "automation conversations")
		})
	}
}
