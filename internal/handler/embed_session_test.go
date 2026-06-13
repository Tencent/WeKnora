package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/handler/session"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

const (
	testEmbedChannelID  = "ch-session-test"
	testEmbedTenantID   = uint64(42)
	testEmbedSessionID  = "11111111-2222-3333-4444-555555555555"
	testEmbedPublishTok = "em_publish_session_test"
)

type stubSessionServiceForEmbed struct {
	interfaces.SessionService
	sessions map[string]*types.Session
	created  *types.Session
}

func (s *stubSessionServiceForEmbed) GetSession(_ context.Context, id string) (*types.Session, error) {
	sess, ok := s.sessions[id]
	if !ok {
		return nil, errors.New("session not found")
	}
	return sess, nil
}

func (s *stubSessionServiceForEmbed) CreateSession(_ context.Context, session *types.Session) (*types.Session, error) {
	created := *session
	if created.ID == "" {
		created.ID = testEmbedSessionID
	}
	s.created = &created
	return &created, nil
}

type sessionEmbedSvc struct {
	interfaces.EmbedChannelService
	embedChunk func(context.Context, *types.EmbedChannel, string) (*types.Chunk, error)
}

func (s *sessionEmbedSvc) Create(context.Context, uint64, string, *types.EmbedChannel) (*types.EmbedChannel, string, error) {
	return nil, "", nil
}
func (s *sessionEmbedSvc) ListByAgent(context.Context, uint64, string) ([]*types.EmbedChannel, error) {
	return nil, nil
}
func (s *sessionEmbedSvc) ListByTenant(context.Context, uint64) ([]*types.EmbedChannel, error) {
	return nil, nil
}
func (s *sessionEmbedSvc) Update(context.Context, uint64, string, *types.EmbedChannel, *bool, *bool, *bool, *bool, *bool, *string, *string, *string) (*types.EmbedChannel, error) {
	return nil, nil
}
func (s *sessionEmbedSvc) GetOwnedChannel(context.Context, uint64, string) (*types.EmbedChannel, error) {
	return nil, service.ErrEmbedChannelNotFound
}
func (s *sessionEmbedSvc) Delete(context.Context, uint64, string) error { return nil }
func (s *sessionEmbedSvc) RotateToken(context.Context, uint64, string) (*types.EmbedChannel, string, error) {
	return nil, "", nil
}
func (s *sessionEmbedSvc) LookupForEmbed(context.Context, string, string) (*types.EmbedChannel, error) {
	return nil, nil
}
func (s *sessionEmbedSvc) LookupEnabledChannel(context.Context, string) (*types.EmbedChannel, error) {
	return nil, nil
}
func (s *sessionEmbedSvc) IssueSessionToken(context.Context, string) (string, int, error) {
	return "", 0, nil
}
func (s *sessionEmbedSvc) IssuePreviewSession(context.Context, uint64, string) (string, int, error) {
	return "", 0, nil
}
func (s *sessionEmbedSvc) ResolveSessionToken(context.Context, string) (string, error) {
	return "", nil
}
func (s *sessionEmbedSvc) PublicConfig(context.Context, *types.EmbedChannel) types.EmbedChannelPublicConfig {
	return types.EmbedChannelPublicConfig{}
}
func (s *sessionEmbedSvc) SuggestedQuestions(context.Context, *types.EmbedChannel, int) ([]types.SuggestedQuestion, error) {
	return nil, nil
}
func (s *sessionEmbedSvc) EmbedChunk(ctx context.Context, ch *types.EmbedChannel, chunkID string) (*types.Chunk, error) {
	if s.embedChunk != nil {
		return s.embedChunk(ctx, ch, chunkID)
	}
	return nil, nil
}
func (s *sessionEmbedSvc) EmbedDisplayTitle(context.Context, *types.EmbedChannel) string {
	return ""
}

func testEmbedChannel() *types.EmbedChannel {
	return &types.EmbedChannel{
		ID:           testEmbedChannelID,
		TenantID:     testEmbedTenantID,
		PublishToken: testEmbedPublishTok,
		Enabled:      true,
	}
}

func validEmbedSession(ch *types.EmbedChannel) *types.Session {
	return &types.Session{
		ID:          testEmbedSessionID,
		TenantID:    ch.TenantID,
		Description: service.EmbedSessionDescription(ch.ID),
	}
}

func newEnsureEmbedSessionCtx(ch *types.EmbedChannel, sessionID, sig string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/sessions/"+sessionID+"/messages", nil)
	if sig != "" {
		c.Request.Header.Set("X-Embed-Session", sig)
	}
	c.Params = gin.Params{{Key: "session_id", Value: sessionID}}
	ctx := context.WithValue(c.Request.Context(), middleware.EmbedChannelContextKey, ch)
	c.Request = c.Request.WithContext(ctx)
	return c, w
}

func TestEnsureEmbedSessionValid(t *testing.T) {
	ch := testEmbedChannel()
	sig := service.SignEmbedSessionHandle(ch, testEmbedSessionID)
	h := &EmbedChannelHandler{
		sessionService: &stubSessionServiceForEmbed{
			sessions: map[string]*types.Session{
				testEmbedSessionID: validEmbedSession(ch),
			},
		},
	}
	c, w := newEnsureEmbedSessionCtx(ch, testEmbedSessionID, sig)
	if err := h.ensureEmbedSession(c); err != nil {
		t.Fatalf("ensureEmbedSession() = %v, want nil", err)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want no response written on success", w.Code)
	}
}

func TestEnsureEmbedSessionWrongTenant(t *testing.T) {
	ch := testEmbedChannel()
	sig := service.SignEmbedSessionHandle(ch, testEmbedSessionID)
	h := &EmbedChannelHandler{
		sessionService: &stubSessionServiceForEmbed{
			sessions: map[string]*types.Session{
				testEmbedSessionID: {
					ID:          testEmbedSessionID,
					TenantID:    999,
					Description: service.EmbedSessionDescription(ch.ID),
				},
			},
		},
	}
	c, w := newEnsureEmbedSessionCtx(ch, testEmbedSessionID, sig)
	if err := h.ensureEmbedSession(c); err == nil {
		t.Fatal("expected error for cross-tenant session")
	}
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
}

func TestEnsureEmbedSessionWrongDescription(t *testing.T) {
	ch := testEmbedChannel()
	sig := service.SignEmbedSessionHandle(ch, testEmbedSessionID)
	h := &EmbedChannelHandler{
		sessionService: &stubSessionServiceForEmbed{
			sessions: map[string]*types.Session{
				testEmbedSessionID: {
					ID:          testEmbedSessionID,
					TenantID:    ch.TenantID,
					Description: "not-an-embed-session",
				},
			},
		},
	}
	c, w := newEnsureEmbedSessionCtx(ch, testEmbedSessionID, sig)
	if err := h.ensureEmbedSession(c); err == nil {
		t.Fatal("expected error for wrong description marker")
	}
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
}

func TestEnsureEmbedSessionInvalidSig(t *testing.T) {
	ch := testEmbedChannel()
	h := &EmbedChannelHandler{
		sessionService: &stubSessionServiceForEmbed{
			sessions: map[string]*types.Session{
				testEmbedSessionID: validEmbedSession(ch),
			},
		},
	}
	c, w := newEnsureEmbedSessionCtx(ch, testEmbedSessionID, "invalid-signature")
	if err := h.ensureEmbedSession(c); err == nil {
		t.Fatal("expected error for invalid signature")
	}
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
}

func TestEnsureEmbedSessionNotFound(t *testing.T) {
	ch := testEmbedChannel()
	h := &EmbedChannelHandler{sessionService: &stubSessionServiceForEmbed{sessions: map[string]*types.Session{}}}
	c, w := newEnsureEmbedSessionCtx(ch, testEmbedSessionID, "anything")
	if err := h.ensureEmbedSession(c); err == nil {
		t.Fatal("expected error for missing session")
	}
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
}

func TestCreateEmbedSessionSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ch := testEmbedChannel()
	stub := &stubSessionServiceForEmbed{sessions: map[string]*types.Session{}}
	h := &EmbedChannelHandler{sessionService: stub}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/sessions", nil)
	c.Set(types.TenantIDContextKey.String(), ch.TenantID)
	ctx := context.WithValue(c.Request.Context(), middleware.EmbedChannelContextKey, ch)
	c.Request = c.Request.WithContext(ctx)

	h.CreateEmbedSession(c)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			ID  string `json:"id"`
			Sig string `json:"sig"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Success || resp.Data.ID != testEmbedSessionID {
		t.Fatalf("unexpected response: %#v", resp)
	}
	if stub.created == nil || stub.created.Description != service.EmbedSessionDescription(ch.ID) {
		t.Fatalf("created session marker = %q", stub.created.Description)
	}
	if !service.VerifyEmbedSessionHandle(ch, resp.Data.ID, resp.Data.Sig) {
		t.Fatal("returned sig must verify against created session")
	}
}

func TestGetEmbedChunkForbidden(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ch := testEmbedChannel()
	h := &EmbedChannelHandler{
		embedSvc: &sessionEmbedSvc{
			embedChunk: func(_ context.Context, _ *types.EmbedChannel, _ string) (*types.Chunk, error) {
				return nil, service.ErrEmbedChunkForbidden
			},
		},
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/chunks/chunk-1", nil)
	c.Params = gin.Params{{Key: "chunk_id", Value: "chunk-1"}}
	ctx := context.WithValue(c.Request.Context(), middleware.EmbedChannelContextKey, ch)
	c.Request = c.Request.WithContext(ctx)

	h.GetEmbedChunk(c)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
}

func TestGetEmbedChunkSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ch := testEmbedChannel()
	h := &EmbedChannelHandler{
		embedSvc: &sessionEmbedSvc{
			embedChunk: func(_ context.Context, _ *types.EmbedChannel, chunkID string) (*types.Chunk, error) {
				return &types.Chunk{ID: chunkID, Content: "hello"}, nil
			},
		},
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/chunks/chunk-ok", nil)
	c.Params = gin.Params{{Key: "chunk_id", Value: "chunk-ok"}}
	ctx := context.WithValue(c.Request.Context(), middleware.EmbedChannelContextKey, ch)
	c.Request = c.Request.WithContext(ctx)

	h.GetEmbedChunk(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
}

func newEmbedStopSessionCtx(ch *types.EmbedChannel, sessionID, sig, body string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/sessions/"+sessionID+"/stop", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	if sig != "" {
		c.Request.Header.Set("X-Embed-Session", sig)
	}
	c.Params = gin.Params{{Key: "session_id", Value: sessionID}}
	c.Set(types.TenantIDContextKey.String(), ch.TenantID)
	ctx := context.WithValue(c.Request.Context(), middleware.EmbedChannelContextKey, ch)
	c.Request = c.Request.WithContext(ctx)
	return c, w
}

func TestEmbedStopSessionInvalidSig(t *testing.T) {
	ch := testEmbedChannel()
	h := &EmbedChannelHandler{
		sessionService: &stubSessionServiceForEmbed{
			sessions: map[string]*types.Session{
				testEmbedSessionID: validEmbedSession(ch),
			},
		},
		sessionHandler: &session.Handler{},
	}
	c, w := newEmbedStopSessionCtx(ch, testEmbedSessionID, "bad-sig", `{"message_id":"msg-1"}`)
	h.EmbedStopSession(c)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
}

func TestEmbedStopSessionMissingMessageID(t *testing.T) {
	ch := testEmbedChannel()
	sig := service.SignEmbedSessionHandle(ch, testEmbedSessionID)
	h := &EmbedChannelHandler{
		sessionService: &stubSessionServiceForEmbed{
			sessions: map[string]*types.Session{
				testEmbedSessionID: validEmbedSession(ch),
			},
		},
		sessionHandler: &session.Handler{},
	}
	c, w := newEmbedStopSessionCtx(ch, testEmbedSessionID, sig, `{}`)
	h.EmbedStopSession(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
}
