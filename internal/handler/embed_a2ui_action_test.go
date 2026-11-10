package handler

// Tests for the embed-channel delegation of the A2UI card-action endpoint
// (POST /api/v1/embed/:channel_id/sessions/:session_id/agent-actions/execute).
// Reuses the fixtures of embed_session_test.go (same package): the stub
// session service, the channel fixture and the signed-handle signer.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/handler/session"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

func newEmbedExecuteActionCtx(ch *types.EmbedChannel, pathSessionID, sig, body string,
) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost,
		"/sessions/"+pathSessionID+"/agent-actions/execute", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	if sig != "" {
		c.Request.Header.Set("X-Embed-Session", sig)
	}
	c.Params = gin.Params{{Key: "session_id", Value: pathSessionID}}
	c.Set(types.TenantIDContextKey.String(), ch.TenantID)
	ctx := context.WithValue(c.Request.Context(), types.EmbedChannelContextKey, ch)
	c.Request = c.Request.WithContext(ctx)
	return c, w
}

func newEmbedExecuteActionHandler(ch *types.EmbedChannel) *EmbedChannelHandler {
	return &EmbedChannelHandler{
		sessionService: &stubSessionServiceForEmbed{
			sessions: map[string]*types.Session{
				testEmbedSessionID: validEmbedSession(ch),
			},
		},
		// Deliberately empty: every case below must be refused BEFORE the
		// delegate is entered. If a guard regresses, the delegate nil-derefs
		// and the test fails loudly instead of passing silently.
		sessionHandler: &session.Handler{},
	}
}

func TestEmbedExecuteAgentCardActionInvalidSig(t *testing.T) {
	t.Setenv("SYSTEM_SIGNING_KEY", "")
	t.Setenv("SYSTEM_AES_KEY", "test-embed-signing-key-32-bytes!!!")
	ch := testEmbedChannel()
	h := newEmbedExecuteActionHandler(ch)
	c, w := newEmbedExecuteActionCtx(ch, testEmbedSessionID, "bad-sig", `{}`)
	h.EmbedExecuteAgentCardAction(c)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
}

func TestEmbedExecuteAgentCardActionMissingSig(t *testing.T) {
	t.Setenv("SYSTEM_SIGNING_KEY", "")
	t.Setenv("SYSTEM_AES_KEY", "test-embed-signing-key-32-bytes!!!")
	ch := testEmbedChannel()
	h := newEmbedExecuteActionHandler(ch)
	c, w := newEmbedExecuteActionCtx(ch, testEmbedSessionID, "", `{}`)
	h.EmbedExecuteAgentCardAction(c)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
}

// The body's session_id must equal the path's. Owner-scope inside the
// executor would refuse a foreign session anyway (404), but the explicit
// equality check makes it a clear 400 and never lets the lookup run against
// a session this channel handle was not minted for.
func TestEmbedExecuteAgentCardActionBodyPathMismatch(t *testing.T) {
	t.Setenv("SYSTEM_SIGNING_KEY", "")
	t.Setenv("SYSTEM_AES_KEY", "test-embed-signing-key-32-bytes!!!")
	ch := testEmbedChannel()
	sig := service.SignEmbedSessionHandle(ch, testEmbedSessionID)
	h := newEmbedExecuteActionHandler(ch)
	body := `{"session_id":"sess-elsewhere","message_id":"m1","surface_id":"s1",` +
		`"source_component_id":"b1","action_name":"confirm"}`
	c, w := newEmbedExecuteActionCtx(ch, testEmbedSessionID, sig, body)
	h.EmbedExecuteAgentCardAction(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "does not match") {
		t.Fatalf("expected mismatch refusal, got %s", w.Body.String())
	}
}
