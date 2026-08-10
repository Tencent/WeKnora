package session

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// The stubs below implement only the methods ContinueStream reaches; embedding
// each interface keeps everything else nil-panicky so an un-stubbed call fails
// loudly rather than returning a zero value.

type stubContinueStreamSessionService struct {
	interfaces.SessionService
}

func (s *stubContinueStreamSessionService) GetSession(_ context.Context, id string) (*types.Session, error) {
	return &types.Session{ID: id, TenantID: 1}, nil
}

type stubMessageServiceForStream struct {
	interfaces.MessageService
}

func (s *stubMessageServiceForStream) GetMessage(
	_ context.Context, sessionID, messageID string,
) (*types.Message, error) {
	return &types.Message{ID: messageID, SessionID: sessionID, RequestID: "req-1"}, nil
}

// stubStreamManager replays a fixed event list, mimicking a completed stream.
type stubStreamManager struct {
	events []interfaces.StreamEvent
}

func (s *stubStreamManager) AppendEvent(
	context.Context, string, string, interfaces.StreamEvent,
) error {
	return nil
}

func (s *stubStreamManager) GetEvents(
	_ context.Context, _, _ string, fromOffset int,
) ([]interfaces.StreamEvent, int, error) {
	if fromOffset >= len(s.events) {
		return nil, len(s.events), nil
	}
	return s.events[fromOffset:], len(s.events), nil
}

// completedAnswerStream is one assistant turn whose answer embeds a knowledge-base
// image, with the resource handle straddling two deltas as it does in production
// when the model-context decoder flushes mid-reference.
func completedAnswerStream() []interfaces.StreamEvent {
	return []interfaces.StreamEvent{
		{ID: "answer-1", Type: types.ResponseTypeAnswer, Content: "The diagram ![fig](resource://xifDo7"},
		{ID: "answer-1", Type: types.ResponseTypeAnswer, Content: "NTSL300Lp1goVutw) shows the flow."},
		{ID: "answer-1", Type: types.ResponseTypeAnswer, Content: "", Done: true},
		{ID: "refs-1", Type: types.ResponseTypeReferences, Data: map[string]interface{}{
			"references": types.References{{
				ID:        "chunk-1",
				Content:   "figure ![f](" + testResourceHandle + ")",
				ImageInfo: `[{"url":"` + testResourceHandle + `"}]`,
			}},
		}},
		{ID: "complete-1", Type: types.ResponseTypeComplete, Done: true},
	}
}

func newContinueStreamRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	h := &Handler{
		sessionService: &stubContinueStreamSessionService{},
		messageService: &stubMessageServiceForStream{},
		streamManager:  &stubStreamManager{events: completedAnswerStream()},
		fileService:    &stubResourceFileService{},
	}
	r.GET("/sessions/continue-stream/:session_id", h.ContinueStream)
	return r
}

func continueStream(t *testing.T, query string) (int, string) {
	t.Helper()
	recorder := httptest.NewRecorder()
	newContinueStreamRouter(t).ServeHTTP(recorder, httptest.NewRequest(
		http.MethodGet, "/sessions/continue-stream/sess1?message_id=msg1"+query, nil))
	return recorder.Code, recorder.Body.String()
}

// Default behaviour: the SSE stream keeps carrying internal handles, which the
// WeKnora frontend resolves through the authenticated /files proxy.
func TestContinueStream_DefaultEmitsHandles(t *testing.T) {
	code, body := continueStream(t, "")
	require.Equal(t, http.StatusOK, code, body)

	assert.Contains(t, body, testResourceHandle)
	assert.NotContains(t, body, "cdn.example.com")
}

// With resource_urls=public the whole stream — answer text and the references
// payload — carries URLs a third-party app can load directly.
func TestContinueStream_PublicModeEmitsLoadableURLs(t *testing.T) {
	code, body := continueStream(t, "&resource_urls=public")
	require.Equal(t, http.StatusOK, code, body)

	assert.NotContains(t, body, "resource://",
		"no internal handle may reach a client that asked for public URLs")
	assert.Contains(t, body, "https://cdn.example.com/signed.png")

	// The handle was split across two answer deltas; the reassembled Markdown
	// image must be intact rather than broken in half.
	assert.Contains(t, body, `![fig](https://cdn.example.com/signed.png) shows the flow.`)

	// The references payload carries both the chunk text and image_info.
	assert.Contains(t, body, `figure ![f](https://cdn.example.com/signed.png)`)
	assert.Contains(t, body, `[{\"url\":\"https://cdn.example.com/signed.png\"}]`)

	// Every answer delta must precede the completion marker.
	assert.Less(t, strings.LastIndex(body, `"response_type":"answer"`),
		strings.Index(body, `"response_type":"complete"`))
}

func TestContinueStream_RejectsInvalidResourceURLMode(t *testing.T) {
	code, body := continueStream(t, "&resource_urls=signed")
	assert.Equal(t, http.StatusBadRequest, code, body)
	assert.Contains(t, body, "resource_urls")
}
