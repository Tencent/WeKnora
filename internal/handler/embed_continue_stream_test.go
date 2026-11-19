package handler

// Rejection guards for the embed plane's stream re-attach endpoint
// (GET /api/v1/embed/:channel_id/sessions/:session_id/continue-stream).
//
// Why this endpoint needs guards at all: the embed page has no other way to
// watch a server-started run. A confirm click (or an ask_user answer) hands
// execution to kickConfirmRun, which runs executeQA with skipSSE — the run's
// events go only to the Redis replay buffer, and the messages row keeps
// content = "" until the completion path (completeAssistantMessage in
// internal/handler/session/qa.go) persists it. So until this route existed,
// the embed page could only poll the message list and repaint once the row
// settled: nothing at all on screen for the whole run.
//
// The delegate reads the replay buffer for ONE (session, message) pair, so the
// only thing standing between a channel token and another visitor's stream is
// ensureEmbedSession: tenant + channel marker + the signed HMAC handle. These
// tests pin that it refuses BEFORE delegating. The positive path cannot be
// asserted here — delegating into a zero-value session.Handler would panic on
// asserted here — delegating into a zero-value session.Handler would panic on
// its first nil service. What the unit tests do pin is that every rejection
// lands before the delegate is reached, and, in internal/router, that the
// route is registered at all.

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

func newEmbedContinueStreamCtx(ch *types.EmbedChannel, sessionID, sig string,
) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet,
		"/sessions/"+sessionID+"/continue-stream?message_id=msg-1", nil)
	if sig != "" {
		c.Request.Header.Set("X-Embed-Session", sig)
	}
	c.Params = gin.Params{{Key: "session_id", Value: sessionID}}
	if ch != nil {
		c.Set(types.TenantIDContextKey.String(), ch.TenantID)
		c.Request = c.Request.WithContext(
			context.WithValue(c.Request.Context(), types.EmbedChannelContextKey, ch))
	}
	return c, w
}

// embedContinueStreamHandler wires the handler the way the thin shell needs:
// a session service holding exactly one valid embed session, and a session
// Handler that would panic if the shell ever delegated before refusing.
func embedContinueStreamHandler(_ *types.EmbedChannel, sess *types.Session) *EmbedChannelHandler {
	return &EmbedChannelHandler{
		sessionService: &stubSessionServiceForEmbed{
			sessions: map[string]*types.Session{sess.ID: sess},
		},
		sessionHandler: &session.Handler{},
	}
}

func TestEmbedContinueStreamRefusesWithoutChannel(t *testing.T) {
	c, w := newEmbedContinueStreamCtx(nil, testEmbedSessionID, "whatever")
	embedContinueStreamHandler(testEmbedChannel(), validEmbedSession(testEmbedChannel())).EmbedContinueStream(c)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body = %s", w.Code, w.Body.String())
	}
}

func TestEmbedContinueStreamRefusesBadSig(t *testing.T) {
	t.Setenv("SYSTEM_SIGNING_KEY", "")
	t.Setenv("SYSTEM_AES_KEY", "test-embed-signing-key-32-bytes!!!")
	ch := testEmbedChannel()
	c, w := newEmbedContinueStreamCtx(ch, testEmbedSessionID, "not-the-signed-handle")
	embedContinueStreamHandler(ch, validEmbedSession(ch)).EmbedContinueStream(c)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body = %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatal("refusal must not have opened an SSE response")
	}
}

func TestEmbedContinueStreamRefusesMissingSigHeader(t *testing.T) {
	t.Setenv("SYSTEM_SIGNING_KEY", "")
	t.Setenv("SYSTEM_AES_KEY", "test-embed-signing-key-32-bytes!!!")
	ch := testEmbedChannel()
	c, w := newEmbedContinueStreamCtx(ch, testEmbedSessionID, "")
	embedContinueStreamHandler(ch, validEmbedSession(ch)).EmbedContinueStream(c)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body = %s", w.Code, w.Body.String())
	}
}

// A session that exists in the same tenant but carries another channel's marker
// must be refused: the replay buffer is keyed only by (session, message), so
// the marker check is what keeps one channel's visitors out of another's.
func TestEmbedContinueStreamRefusesForeignChannel(t *testing.T) {
	t.Setenv("SYSTEM_SIGNING_KEY", "")
	t.Setenv("SYSTEM_AES_KEY", "test-embed-signing-key-32-bytes!!!")
	ch := testEmbedChannel()
	other := &types.EmbedChannel{ID: "other-channel-id", TenantID: ch.TenantID, Enabled: true}
	c, w := newEmbedContinueStreamCtx(other, testEmbedSessionID,
		service.SignEmbedSessionHandle(other, testEmbedSessionID))
	embedContinueStreamHandler(ch, validEmbedSession(ch)).EmbedContinueStream(c)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body = %s", w.Code, w.Body.String())
	}
}
