package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type updateEmbedChannelRepo struct {
	interfaces.EmbedChannelRepository
	ch *types.EmbedChannel
}

func (r *updateEmbedChannelRepo) GetByID(_ context.Context, id string) (*types.EmbedChannel, error) {
	if r.ch == nil || r.ch.ID != id {
		return nil, nil
	}
	cp := *r.ch
	return &cp, nil
}

func (r *updateEmbedChannelRepo) Update(_ context.Context, ch *types.EmbedChannel) error {
	cp := *ch
	r.ch = &cp
	return nil
}

func putEmbedChannel(t *testing.T, repo *updateEmbedChannelRepo, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set(types.TenantIDContextKey.String(), uint64(42)) })
	h := &EmbedChannelHandler{embedSvc: service.NewEmbedChannelService(repo, nil, nil, nil)}
	r.PUT("/embed-channels/:channel_id", h.UpdateEmbedChannel)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/embed-channels/ch-1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	return rec
}

// A partial update that does not mention allowed_origins must leave the
// allowlist alone. An empty allowlist denies every origin, so wiping it turns
// the widget off for all visitors while the request still returns 200.
func TestUpdateEmbedChannelKeepsAllowedOriginsWhenOmitted(t *testing.T) {
	for _, body := range []string{
		`{"rate_limit_per_minute":600}`,
		`{"allowed_origins":null,"rate_limit_per_minute":600}`,
	} {
		repo := &updateEmbedChannelRepo{ch: &types.EmbedChannel{
			ID:                 "ch-1",
			TenantID:           42,
			AgentID:            "agent-1",
			AllowedOrigins:     []byte(`["https://site.example"]`),
			RateLimitPerMinute: 60,
		}}
		rec := putEmbedChannel(t, repo, body)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d: %s", body, rec.Code, rec.Body.String())
		}
		if repo.ch.RateLimitPerMinute != 600 {
			t.Errorf("%s: rate limit = %d, want 600", body, repo.ch.RateLimitPerMinute)
		}
		if got, want := repo.ch.AllowedOriginsList(), []string{"https://site.example"}; !reflect.DeepEqual(got, want) {
			t.Errorf("%s: allowed origins = %v, want %v", body, got, want)
		}
	}
}
