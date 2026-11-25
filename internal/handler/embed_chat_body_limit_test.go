package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// countingReader reports how many bytes were actually pulled, so the cap is
// measured as a read rather than inferred from a response.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// embedSessionStub implements only the SessionService calls the embed chat path
// reaches. Every other method stays unimplemented through the embedded
// interface, so a new call there panics instead of silently succeeding.
type embedSessionStub struct {
	interfaces.SessionService
	session *types.Session
}

func (s *embedSessionStub) GetSessionByID(context.Context, uint64, string) (*types.Session, error) {
	return s.session, nil
}

func (s *embedSessionStub) SetSessionOwnerID(context.Context, uint64, string, string) error {
	return nil
}

const embedChatTestSessionID = "session-1"

// embedChatTestChannel is the minimum channel both the session check and the
// payload rewrite read.
func embedChatTestChannel() *types.EmbedChannel {
	ch := &types.EmbedChannel{
		ID:           "channel-1",
		TenantID:     7,
		AgentID:      "agent-1",
		Enabled:      true,
		PublishToken: "publish-token",
	}
	return ch
}

// newEmbedChatTestHandler wires a session the stub accepts plus the signed
// handle ensureEmbedSession requires, so a test reaches the payload read.
func newEmbedChatTestHandler(ch *types.EmbedChannel) *EmbedChannelHandler {
	return &EmbedChannelHandler{
		sessionService: &embedSessionStub{
			session: &types.Session{
				ID:          embedChatTestSessionID,
				TenantID:    ch.TenantID,
				Description: service.EmbedSessionDescription(ch.ID),
			},
		},
	}
}

// embedChatRequest builds a request already past EmbedAuth: the channel is in
// the context and the visitor presents a valid session handle.
func embedChatRequest(t *testing.T, body io.Reader, ch *types.EmbedChannel) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	// The session handle is an HMAC over a deployment secret; set one so the
	// test can mint a handle the real verifier accepts.
	t.Setenv("SYSTEM_SIGNING_KEY", "embed-chat-body-limit-test-key")
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/embed/"+ch.ID+"/knowledge-chat/"+embedChatTestSessionID, body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Embed-Session", service.SignEmbedSessionHandle(ch, embedChatTestSessionID))
	req = req.WithContext(context.WithValue(req.Context(), types.EmbedChannelContextKey, ch))
	c.Request = req
	c.Params = gin.Params{
		{Key: "channel_id", Value: ch.ID},
		{Key: "session_id", Value: embedChatTestSessionID},
	}
	return c, w
}

// The embed chat routes rewrite the visitor's payload, so the body is buffered
// before the delegated QA handler caps anything, and embed visitors are
// anonymous. An oversized body must stop at the cap and be reported as a size
// limit instead of being buffered whole.
func TestReadEmbedChatPayloadRefusesOversizedBody(t *testing.T) {
	ch := embedChatTestChannel()
	body := `{"query":"` + strings.Repeat("x", embedChatJSONMaxBytes+1024) + `"}`
	counter := &countingReader{r: strings.NewReader(body)}
	c, w := embedChatRequest(t, counter, ch)

	h := newEmbedChatTestHandler(ch)
	if _, ok := h.readEmbedChatPayload(c, ch, false); ok {
		t.Fatal("expected the oversized body to be refused")
	}

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d (body: %s)", w.Code, http.StatusRequestEntityTooLarge, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "request body is too large") {
		t.Fatalf("body = %s, want a size-limit error", w.Body.String())
	}
	// MaxBytesReader stops one read past the cap, so the whole body cannot have
	// been buffered.
	if counter.n > embedChatJSONMaxBytes+4096 {
		t.Fatalf("read %d bytes, want at most cap+slack (%d)", counter.n, embedChatJSONMaxBytes+4096)
	}
	if counter.n < embedChatJSONMaxBytes {
		t.Fatalf("read %d bytes, want it to reach the cap", counter.n)
	}
}

// A legal body must still reach the rewrite: the cap may not refuse a request
// for its envelope.
func TestReadEmbedChatPayloadAcceptsLegalBody(t *testing.T) {
	ch := embedChatTestChannel()
	c, _ := embedChatRequest(t, strings.NewReader(`{"query":"hello","agent_id":"client-override"}`), ch)

	h := newEmbedChatTestHandler(ch)
	patched, ok := h.readEmbedChatPayload(c, ch, false)
	if !ok {
		t.Fatalf("legal body refused (status %d)", c.Writer.Status())
	}
	var payload map[string]any
	if err := json.Unmarshal(patched, &payload); err != nil {
		t.Fatalf("rewritten payload is not JSON: %v", err)
	}
	if payload["agent_id"] != ch.AgentID {
		t.Fatalf("agent_id = %v, want the channel agent %q", payload["agent_id"], ch.AgentID)
	}
}

// A malformed body inside the cap must stay a parse error, not become a size
// error: the two answers lead the client to different fixes.
func TestReadEmbedChatPayloadKeepsMalformedBodyDistinct(t *testing.T) {
	ch := embedChatTestChannel()
	c, w := embedChatRequest(t, strings.NewReader(`{not-json`), ch)

	h := newEmbedChatTestHandler(ch)
	if _, ok := h.readEmbedChatPayload(c, ch, false); ok {
		t.Fatal("expected a parse error")
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if strings.Contains(w.Body.String(), "too large") {
		t.Fatalf("body = %s, want a parse error rather than a size limit", w.Body.String())
	}
}

// The cap has to sit above the base64 encoding of the attachment budget the QA
// handler accepts; a smaller cap would refuse a request that handler allows.
func TestEmbedChatCapCoversAttachmentBudget(t *testing.T) {
	const attachmentTotalBytes = int64(100 * 1024 * 1024)
	encoded := attachmentTotalBytes*4/3 + 2*1024*1024
	if int64(embedChatJSONMaxBytes) < encoded {
		t.Fatalf("embedChatJSONMaxBytes = %d, want at least the encoded attachment budget %d",
			int64(embedChatJSONMaxBytes), encoded)
	}
}

// The read error must stay reachable through the wrapper, otherwise an
// oversized body is reported as a malformed one.
func TestPatchEmbedChatPayloadKeepsReadErrorReachable(t *testing.T) {
	ch := embedChatTestChannel()
	capped := http.MaxBytesReader(httptest.NewRecorder(), io.NopCloser(strings.NewReader(`{"query":"hello"}`)), 4)
	_, err := patchEmbedChatPayload(capped, ch, false)
	if err == nil {
		t.Fatal("expected an error from a capped body")
	}
	var tooLarge *http.MaxBytesError
	if !errors.As(err, &tooLarge) {
		t.Fatalf("errors.As did not reach http.MaxBytesError through %v", err)
	}
}
