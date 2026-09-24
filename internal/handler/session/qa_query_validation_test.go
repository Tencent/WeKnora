package session

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type queryValidationSessionStub struct {
	interfaces.SessionService
	lookupCalls int
}

func (s *queryValidationSessionStub) GetOwnedSession(context.Context, string) (*types.Session, error) {
	s.lookupCalls++
	return nil, errors.New("stop at session lookup")
}

func TestQAQueryValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name               string
		query              string
		rejectBeforeLookup bool
	}{
		{name: "empty", rejectBeforeLookup: true},
		{name: "whitespace", query: " \t\r\n ", rejectBeforeLookup: true},
		{name: "unicode whitespace", query: "\u3000\u00a0", rejectBeforeLookup: true},
		{name: "null byte", query: "hello\x00world", rejectBeforeLookup: true},
		{name: "escape character", query: "hello\x1bworld", rejectBeforeLookup: true},
		{name: "script element", query: "<script>alert(1)</script>"},
		{name: "event handler", query: `<img src=x onerror="alert(1)">`},
		{name: "script URL", query: "javascript:alert(1)"},
		{name: "agent frontend snippet", query: `Why doesn't my <button onclick="save()">Save</button> fire?`},
		{name: "agent javascript snippet", query: "What does javascript:void(0) do?"},
		{name: "English", query: "What is retrieval augmented generation?"},
		{name: "Unicode", query: "请解释厄尔尼诺现象 🌊"},
		{name: "multiline", query: "Compare:\n\tGo\r\n\tPython"},
		{name: "padded text", query: "  Explain retrieval.\n"},
	}
	for _, endpoint := range []string{"knowledge-chat", "agent-chat"} {
		t.Run(endpoint, func(t *testing.T) {
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					sessions := &queryValidationSessionStub{}
					h := &Handler{sessionService: sessions}
					router := gin.New()
					router.Use(middleware.ErrorHandler())
					router.POST("/knowledge-chat/:session_id", h.KnowledgeQA)
					router.POST("/agent-chat/:session_id", h.AgentQA)

					body, err := json.Marshal(CreateKnowledgeQARequest{Query: tt.query})
					require.NoError(t, err)
					request := httptest.NewRequest(http.MethodPost, "/"+endpoint+"/session-1", bytes.NewReader(body))
					request.Header.Set("Content-Type", "application/json")
					response := httptest.NewRecorder()
					router.ServeHTTP(response, request)

					if tt.rejectBeforeLookup {
						assert.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
						assert.Contains(t, response.Header().Get("Content-Type"), "application/json")
						assert.Zero(t, sessions.lookupCalls, "reject the query before session lookup or QA work")
					} else {
						// Stop at a controlled not-found response: valid queries must
						// reach the ordinary session lookup without invoking a model.
						assert.Equal(t, http.StatusNotFound, response.Code, response.Body.String())
						assert.Equal(t, 1, sessions.lookupCalls)
					}
				})
			}
		})
	}
}
