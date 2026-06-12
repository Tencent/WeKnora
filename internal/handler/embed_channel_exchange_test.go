package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

type exchangeEmbedSvc struct {
	sessionToken string
	expiresIn    int
	err          error
}

func (f *exchangeEmbedSvc) Create(context.Context, uint64, string, *types.EmbedChannel) (*types.EmbedChannel, string, error) {
	return nil, "", nil
}
func (f *exchangeEmbedSvc) ListByAgent(context.Context, uint64, string) ([]*types.EmbedChannel, error) {
	return nil, nil
}
func (f *exchangeEmbedSvc) ListByTenant(context.Context, uint64) ([]*types.EmbedChannel, error) {
	return nil, nil
}
func (f *exchangeEmbedSvc) Update(context.Context, uint64, string, *types.EmbedChannel, *bool, *bool, *bool, *bool, *bool, *string, *string, *string) (*types.EmbedChannel, error) {
	return nil, nil
}
func (f *exchangeEmbedSvc) GetOwnedChannel(context.Context, uint64, string) (*types.EmbedChannel, error) {
	return nil, service.ErrEmbedChannelNotFound
}
func (f *exchangeEmbedSvc) Delete(context.Context, uint64, string) error { return nil }
func (f *exchangeEmbedSvc) RotateToken(context.Context, uint64, string) (*types.EmbedChannel, string, error) {
	return nil, "", nil
}
func (f *exchangeEmbedSvc) LookupForEmbed(context.Context, string, string) (*types.EmbedChannel, error) {
	return nil, nil
}
func (f *exchangeEmbedSvc) LookupEnabledChannel(context.Context, string) (*types.EmbedChannel, error) {
	return nil, nil
}
func (f *exchangeEmbedSvc) IssueSessionToken(context.Context, string) (string, int, error) {
	if f.err != nil {
		return "", 0, f.err
	}
	return f.sessionToken, f.expiresIn, nil
}
func (f *exchangeEmbedSvc) IssuePreviewSession(context.Context, uint64, string) (string, int, error) {
	return f.IssueSessionToken(context.Background(), "")
}
func (f *exchangeEmbedSvc) ResolveSessionToken(context.Context, string) (string, error) {
	return "", nil
}
func (f *exchangeEmbedSvc) PublicConfig(context.Context, *types.EmbedChannel) types.EmbedChannelPublicConfig {
	return types.EmbedChannelPublicConfig{}
}
func (f *exchangeEmbedSvc) SuggestedQuestions(context.Context, *types.EmbedChannel, int) ([]types.SuggestedQuestion, error) {
	return nil, nil
}
func (f *exchangeEmbedSvc) EmbedChunk(context.Context, *types.EmbedChannel, string) (*types.Chunk, error) {
	return nil, nil
}
func (f *exchangeEmbedSvc) EmbedDisplayTitle(context.Context, *types.EmbedChannel) string {
	return "AI Assistant"
}

func TestExchangeEmbedSessionSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &EmbedChannelHandler{embedSvc: &exchangeEmbedSvc{
		sessionToken: "ems_test_token",
		expiresIn:    1800,
	}}

	r := gin.New()
	r.POST("/exchange", func(c *gin.Context) {
		ch := &types.EmbedChannel{ID: "channel-1", Enabled: true}
		ctx := context.WithValue(c.Request.Context(), middleware.EmbedChannelContextKey, ch)
		c.Request = c.Request.WithContext(ctx)
		h.ExchangeEmbedSession(c)
	})

	req := httptest.NewRequest(http.MethodPost, "/exchange", nil)
	req.Header.Set("Authorization", "Embed em_publish_token")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			SessionToken string `json:"session_token"`
			ExpiresIn    int    `json:"expires_in"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Success || resp.Data.SessionToken != "ems_test_token" || resp.Data.ExpiresIn != 1800 {
		t.Fatalf("unexpected response: %#v", resp)
	}
}

func TestExchangeEmbedSessionUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &EmbedChannelHandler{embedSvc: &exchangeEmbedSvc{err: service.ErrEmbedSessionUnavailable}}

	r := gin.New()
	r.POST("/exchange", func(c *gin.Context) {
		ch := &types.EmbedChannel{ID: "channel-1", Enabled: true}
		ctx := context.WithValue(c.Request.Context(), middleware.EmbedChannelContextKey, ch)
		c.Request = c.Request.WithContext(ctx)
		h.ExchangeEmbedSession(c)
	})

	req := httptest.NewRequest(http.MethodPost, "/exchange", nil)
	req.Header.Set("Authorization", "Embed em_publish_token")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
}

func TestExchangeEmbedSessionRejectsSessionToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &EmbedChannelHandler{embedSvc: &exchangeEmbedSvc{sessionToken: "ems_new", expiresIn: 1800}}

	r := gin.New()
	r.POST("/exchange", func(c *gin.Context) {
		ch := &types.EmbedChannel{ID: "channel-1", Enabled: true}
		ctx := context.WithValue(c.Request.Context(), middleware.EmbedChannelContextKey, ch)
		c.Request = c.Request.WithContext(ctx)
		h.ExchangeEmbedSession(c)
	})

	req := httptest.NewRequest(http.MethodPost, "/exchange", nil)
	req.Header.Set("Authorization", "Embed ems_existing_session")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
}
