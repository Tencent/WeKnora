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

func TestQAQueryValidationAcceptsUploadWithoutText(t *testing.T) {
	gin.SetMode(gin.TestMode)
	image := []ImageAttachment{{Data: "data:image/png;base64,aGk="}}
	tests := []struct {
		name               string
		body               CreateKnowledgeQARequest
		rejectBeforeLookup bool
	}{
		{name: "image only", body: CreateKnowledgeQARequest{Images: image}},
		{name: "whitespace with image", body: CreateKnowledgeQARequest{Query: " \n", Images: image}},
		{
			name: "attachment upload only",
			body: CreateKnowledgeQARequest{AttachmentUploads: []AttachmentUpload{{Data: "aGk=", FileName: "a.txt"}}},
		},
		{
			name:               "url-only image",
			body:               CreateKnowledgeQARequest{Images: []ImageAttachment{{URL: "http://127.0.0.1/a.png"}}},
			rejectBeforeLookup: true,
		},
		// Pre-uploaded documents may still fail or time out after the stream
		// starts, leaving the turn with no content to answer from.
		{
			name:               "attachment id only",
			body:               CreateKnowledgeQARequest{AttachmentIDs: []string{"doc-1"}},
			rejectBeforeLookup: true,
		},
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

					body, err := json.Marshal(tt.body)
					require.NoError(t, err)
					request := httptest.NewRequest(http.MethodPost, "/"+endpoint+"/session-1", bytes.NewReader(body))
					request.Header.Set("Content-Type", "application/json")
					response := httptest.NewRecorder()
					router.ServeHTTP(response, request)

					if tt.rejectBeforeLookup {
						assert.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
						assert.Zero(t, sessions.lookupCalls)
					} else {
						assert.Equal(t, http.StatusNotFound, response.Code, response.Body.String())
						assert.Equal(t, 1, sessions.lookupCalls)
					}
				})
			}
		})
	}
}

func TestPersistTurnMessagesStoresWhatTheUserTyped(t *testing.T) {
	msgs := &steerPersistingMessageStub{}
	h := &Handler{messageService: msgs}
	question := types.UploadOnlyQuestion("zh-CN")
	reqCtx := &qaRequestContext{
		sessionID:        "sess-1",
		query:            question,
		images:           []ImageAttachment{{Data: "data:image/png;base64,aGk=", URL: "resource://img"}},
		assistantMessage: &types.Message{SessionID: "sess-1", Role: "assistant"},
	}

	require.NoError(t, h.persistTurnMessages(context.Background(), reqCtx))

	stored := msgs.byID[reqCtx.userMessageID]
	require.NotNil(t, stored)
	assert.Empty(t, stored.Content, "an image-only message must not gain text the user never typed")
	assert.Len(t, stored.Images, 1)
	assert.Equal(t, question, reqCtx.buildQARequest().Query)
}

func TestUploadOnlyQuery(t *testing.T) {
	image := &CreateKnowledgeQARequest{Images: []ImageAttachment{{Data: "data:image/png;base64,aGk="}}}
	zh := context.WithValue(context.Background(), types.LanguageContextKey, "zh-CN")
	en := context.WithValue(context.Background(), types.LanguageContextKey, "en-US")

	assert.Equal(t, "请根据我上传的内容回答。", uploadOnlyQuery(zh, image))
	assert.Equal(t, "Please answer based on what I uploaded.", uploadOnlyQuery(en, image))
	assert.Empty(t, uploadOnlyQuery(zh, &CreateKnowledgeQARequest{}))
	assert.Empty(t, uploadOnlyQuery(zh, &CreateKnowledgeQARequest{Images: []ImageAttachment{{URL: "http://x/a.png"}}}))
}
