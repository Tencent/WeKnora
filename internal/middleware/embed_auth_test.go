package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

type fakeEmbedChannelService struct {
	channels map[string]*types.EmbedChannel
	sessions map[string]string
}

func (f *fakeEmbedChannelService) Create(
	_ context.Context, _ uint64, _ string, _ *types.EmbedChannel,
) (*types.EmbedChannel, string, error) {
	return nil, "", nil
}

func (f *fakeEmbedChannelService) ListByAgent(
	_ context.Context, _ uint64, _ string,
) ([]*types.EmbedChannel, error) {
	return nil, nil
}

func (f *fakeEmbedChannelService) ListByTenant(
	_ context.Context, _ uint64,
) ([]*types.EmbedChannel, error) {
	return nil, nil
}

func (f *fakeEmbedChannelService) Update(
	_ context.Context, _ uint64, _ string, _ *types.EmbedChannel,
	_ *bool, _ *bool, _ *bool, _ *bool,
	_ *string, _ *string, _ *string,
) (*types.EmbedChannel, error) {
	return nil, nil
}

func (f *fakeEmbedChannelService) GetOwnedChannel(
	_ context.Context, tenantID uint64, id string,
) (*types.EmbedChannel, error) {
	ch := f.channels[id]
	if ch == nil || ch.TenantID != tenantID {
		return nil, service.ErrEmbedChannelNotFound
	}
	return ch, nil
}

func (f *fakeEmbedChannelService) Delete(_ context.Context, _ uint64, _ string) error {
	return nil
}

func (f *fakeEmbedChannelService) RotateToken(
	_ context.Context, _ uint64, _ string,
) (*types.EmbedChannel, string, error) {
	return nil, "", nil
}

func (f *fakeEmbedChannelService) LookupForEmbed(
	_ context.Context, channelID, token string,
) (*types.EmbedChannel, error) {
	ch := f.channels[channelID]
	if ch == nil || ch.PublishToken != token {
		return nil, service.ErrEmbedTokenInvalid
	}
	if !ch.Enabled {
		return nil, service.ErrEmbedChannelDisabled
	}
	return ch, nil
}

func (f *fakeEmbedChannelService) LookupEnabledChannel(
	_ context.Context, channelID string,
) (*types.EmbedChannel, error) {
	ch := f.channels[channelID]
	if ch == nil {
		return nil, service.ErrEmbedTokenInvalid
	}
	if !ch.Enabled {
		return nil, service.ErrEmbedChannelDisabled
	}
	return ch, nil
}

func (f *fakeEmbedChannelService) IssueSessionToken(
	_ context.Context, _ string,
) (string, int, error) {
	return "ems_testtoken", 1800, nil
}

func (f *fakeEmbedChannelService) ResolveSessionToken(_ context.Context, token string) (string, error) {
	channelID, ok := f.sessions[token]
	if !ok {
		return "", service.ErrEmbedTokenInvalid
	}
	return channelID, nil
}

func (f *fakeEmbedChannelService) PublicConfig(
	_ context.Context, ch *types.EmbedChannel,
) types.EmbedChannelPublicConfig {
	return types.EmbedChannelPublicConfig{ChannelID: ch.ID}
}

func (f *fakeEmbedChannelService) SuggestedQuestions(
	_ context.Context, _ *types.EmbedChannel, _ int,
) ([]types.SuggestedQuestion, error) {
	return nil, nil
}

func (f *fakeEmbedChannelService) EmbedChunk(
	_ context.Context, _ *types.EmbedChannel, _ string,
) (*types.Chunk, error) {
	return nil, nil
}

func (f *fakeEmbedChannelService) IssuePreviewSession(
	_ context.Context, _ uint64, _ string,
) (string, int, error) {
	return "", 0, nil
}

func (f *fakeEmbedChannelService) EmbedDisplayTitle(_ context.Context, _ *types.EmbedChannel) string {
	return ""
}

type fakeTenantService struct {
	tenant *types.Tenant
}

func (f *fakeTenantService) GetTenantByID(_ context.Context, _ uint64) (*types.Tenant, error) {
	return f.tenant, nil
}

func (f *fakeTenantService) CreateTenant(_ context.Context, _ *types.Tenant) (*types.Tenant, error) {
	return nil, nil
}

func (f *fakeTenantService) GetTenantsByIDs(_ context.Context, _ []uint64) (map[uint64]*types.Tenant, error) {
	return nil, nil
}

func (f *fakeTenantService) UpdateTenant(_ context.Context, _ *types.Tenant) (*types.Tenant, error) {
	return nil, nil
}

func (f *fakeTenantService) DeleteTenant(_ context.Context, _ uint64) error {
	return nil
}

func (f *fakeTenantService) ListTenants(_ context.Context) ([]*types.Tenant, error) {
	return nil, nil
}

func (f *fakeTenantService) ListAllTenants(_ context.Context) ([]*types.Tenant, error) {
	return nil, nil
}

func (f *fakeTenantService) BulkSetStorageQuota(_ context.Context, _ int64) (int64, error) {
	return 0, nil
}

func (f *fakeTenantService) SearchTenants(
	_ context.Context, _ string, _ uint64, _, _ int,
) ([]*types.Tenant, int64, error) {
	return nil, 0, nil
}

func (f *fakeTenantService) GetTenantByIDForUser(
	_ context.Context, _ uint64, _ string,
) (*types.Tenant, error) {
	return f.tenant, nil
}

func (f *fakeTenantService) GetWeKnoraCloudCredentials(_ context.Context) *types.WeKnoraCloudCredentials {
	return nil
}

var (
	_ interfaces.EmbedChannelService = (*fakeEmbedChannelService)(nil)
	_ interfaces.TenantService       = (*fakeTenantService)(nil)
)

func TestEmbedGlobalPerMinute(t *testing.T) {
	tests := []struct {
		perIP int
		want  int
	}{
		{perIP: 0, want: 120},
		{perIP: 1, want: 120},
		{perIP: 6, want: 120},
		{perIP: 7, want: 140},
		{perIP: 10, want: 200},
	}
	for _, tt := range tests {
		name := fmt.Sprintf("perIP=%d", tt.perIP)
		t.Run(name, func(t *testing.T) {
			if got := embedGlobalPerMinute(tt.perIP); got != tt.want {
				t.Fatalf("embedGlobalPerMinute(%d) = %d, want %d", tt.perIP, got, tt.want)
			}
		})
	}
}

func TestOriginAllowed(t *testing.T) {
	tests := []struct {
		name    string
		origin  string
		allowed []string
		want    bool
	}{
		{name: "empty allow list", origin: "https://evil.com", allowed: nil, want: false},
		{
			name:    "exact match",
			origin:  "https://app.example.com",
			allowed: []string{"https://app.example.com"},
			want:    true,
		},
		{name: "wildcard star", origin: "https://any.example.com", allowed: []string{"*"}, want: true},
		{name: "subdomain suffix", origin: "https://app.example.com", allowed: []string{"*.example.com"}, want: true},
		{name: "missing origin", origin: "", allowed: []string{"https://app.example.com"}, want: false},
		{name: "not allowed", origin: "https://evil.com", allowed: []string{"https://app.example.com"}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := originAllowed(tt.origin, tt.allowed); got != tt.want {
				t.Fatalf("originAllowed(%q, %v) = %v, want %v", tt.origin, tt.allowed, got, tt.want)
			}
		})
	}
}

func TestExtractEmbedToken(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name   string
		header string
		query  string
		want   string
	}{
		{name: "authorization header", header: "Embed em_publish", want: "em_publish"},
		{name: "query param rejected", query: "ems_session", want: ""},
		{name: "header preferred", header: "Embed em_header", query: "em_query", want: "em_header"},
		{name: "missing", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			req := httptest.NewRequest(http.MethodGet, "/?token="+tt.query, nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			c.Request = req
			if got := extractEmbedToken(c); got != tt.want {
				t.Fatalf("extractEmbedToken() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestEmbedAuthSessionTokenPath(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const channelID = "ch-1"
	svc := &fakeEmbedChannelService{
		channels: map[string]*types.EmbedChannel{
			channelID: {
				ID:                 channelID,
				TenantID:           42,
				Enabled:            true,
				AllowedOrigins:     []byte(`["https://app.example.com"]`),
				RateLimitPerMinute: 0,
			},
		},
		sessions: map[string]string{
			"ems_valid": channelID,
		},
	}
	tenantSvc := &fakeTenantService{tenant: &types.Tenant{ID: 42}}

	r := gin.New()
	r.GET("/api/v1/embed/:channel_id/config", EmbedAuth(svc, tenantSvc, nil), func(c *gin.Context) {
		ch, ok := EmbedChannelFromContext(c.Request.Context())
		if !ok || ch.ID != channelID {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "missing channel"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true})
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/embed/"+channelID+"/config", nil)
	req.Header.Set("Authorization", "Embed ems_valid")
	req.Header.Set("Origin", "https://app.example.com")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if body["success"] != true {
		t.Fatalf("expected success response, got %v", body)
	}
}

func TestEmbedAuthPublishTokenValid(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const (
		channelID    = "ch-pub-1"
		publishToken = "em_publish_ok"
	)
	svc := &fakeEmbedChannelService{
		channels: map[string]*types.EmbedChannel{
			channelID: {
				ID:                 channelID,
				TenantID:           11,
				Enabled:            true,
				PublishToken:       publishToken,
				AllowedOrigins:     []byte(`["https://app.example.com"]`),
				RateLimitPerMinute: 0,
			},
		},
	}
	tenantSvc := &fakeTenantService{tenant: &types.Tenant{ID: 11}}

	r := gin.New()
	r.GET("/api/v1/embed/:channel_id/config", EmbedAuth(svc, tenantSvc, nil), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"success": true})
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/embed/"+channelID+"/config", nil)
	req.Header.Set("Authorization", "Embed "+publishToken)
	req.Header.Set("Origin", "https://app.example.com")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
}

func TestEmbedAuthPublishTokenInvalid(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const channelID = "ch-pub-1"
	svc := &fakeEmbedChannelService{
		channels: map[string]*types.EmbedChannel{
			channelID: {
				ID:             channelID,
				TenantID:       11,
				Enabled:        true,
				PublishToken:   "em_real_token",
				AllowedOrigins: []byte(`["https://app.example.com"]`),
			},
		},
	}
	handler := EmbedAuth(svc, &fakeTenantService{tenant: &types.Tenant{ID: 11}}, nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/embed/"+channelID+"/config", nil)
	c.Request.Header.Set("Authorization", "Embed em_wrong_token")
	c.Request.Header.Set("Origin", "https://app.example.com")
	c.Params = gin.Params{{Key: "channel_id", Value: channelID}}
	handler(c)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d, body = %s", w.Code, http.StatusUnauthorized, w.Body.String())
	}
}

func TestEmbedAuthSessionTokenMismatch(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const channelID = "ch-1"
	svc := &fakeEmbedChannelService{
		sessions: map[string]string{
			"ems_other": "other-channel",
		},
	}
	handler := EmbedAuth(svc, &fakeTenantService{tenant: &types.Tenant{ID: 1}}, nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/embed/"+channelID+"/config", nil)
	c.Request.Header.Set("Authorization", "Embed ems_other")
	c.Params = gin.Params{{Key: "channel_id", Value: channelID}}
	handler(c)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d, body = %s", w.Code, http.StatusUnauthorized, w.Body.String())
	}
}
