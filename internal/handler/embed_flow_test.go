package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

type flowEmbedSvc struct {
	sessionToken string
	expiresIn    int
	issueErr     error
	channels     map[string]*types.EmbedChannel
}

func (f *flowEmbedSvc) Create(context.Context, uint64, string, *types.EmbedChannel) (*types.EmbedChannel, string, error) {
	return nil, "", nil
}
func (f *flowEmbedSvc) ListByAgent(context.Context, uint64, string) ([]*types.EmbedChannel, error) {
	return nil, nil
}
func (f *flowEmbedSvc) ListByTenant(context.Context, uint64) ([]*types.EmbedChannel, error) {
	return nil, nil
}
func (f *flowEmbedSvc) Update(context.Context, uint64, string, *types.EmbedChannel, *bool, *bool, *bool, *bool, *bool, *string, *string, *string) (*types.EmbedChannel, error) {
	return nil, nil
}
func (f *flowEmbedSvc) GetOwnedChannel(_ context.Context, tenantID uint64, id string) (*types.EmbedChannel, error) {
	ch := f.channels[id]
	if ch == nil || ch.TenantID != tenantID {
		return nil, service.ErrEmbedChannelNotFound
	}
	return ch, nil
}
func (f *flowEmbedSvc) Delete(context.Context, uint64, string) error { return nil }
func (f *flowEmbedSvc) RotateToken(context.Context, uint64, string) (*types.EmbedChannel, string, error) {
	return nil, "", nil
}
func (f *flowEmbedSvc) LookupForEmbed(_ context.Context, channelID, token string) (*types.EmbedChannel, error) {
	ch := f.channels[channelID]
	if ch == nil || ch.PublishToken != token {
		return nil, service.ErrEmbedTokenInvalid
	}
	if !ch.Enabled {
		return nil, service.ErrEmbedChannelDisabled
	}
	return ch, nil
}
func (f *flowEmbedSvc) LookupEnabledChannel(context.Context, string) (*types.EmbedChannel, error) {
	return nil, nil
}
func (f *flowEmbedSvc) IssueSessionToken(context.Context, string) (string, int, error) {
	if f.issueErr != nil {
		return "", 0, f.issueErr
	}
	return f.sessionToken, f.expiresIn, nil
}
func (f *flowEmbedSvc) IssuePreviewSession(context.Context, uint64, string) (string, int, error) {
	return f.IssueSessionToken(context.Background(), "")
}
func (f *flowEmbedSvc) ResolveSessionToken(context.Context, string) (string, error) {
	return "", nil
}
func (f *flowEmbedSvc) PublicConfig(context.Context, *types.EmbedChannel) types.EmbedChannelPublicConfig {
	return types.EmbedChannelPublicConfig{}
}
func (f *flowEmbedSvc) SuggestedQuestions(context.Context, *types.EmbedChannel, int) ([]types.SuggestedQuestion, error) {
	return nil, nil
}
func (f *flowEmbedSvc) EmbedChunk(context.Context, *types.EmbedChannel, string) (*types.Chunk, error) {
	return nil, nil
}
func (f *flowEmbedSvc) EmbedDisplayTitle(context.Context, *types.EmbedChannel) string {
	return "AI Assistant"
}

type flowTenantSvc struct {
	tenant *types.Tenant
}

func (f *flowTenantSvc) GetTenantByID(context.Context, uint64) (*types.Tenant, error) {
	return f.tenant, nil
}
func (f *flowTenantSvc) CreateTenant(context.Context, *types.Tenant) (*types.Tenant, error) {
	return nil, nil
}
func (f *flowTenantSvc) GetTenantsByIDs(context.Context, []uint64) (map[uint64]*types.Tenant, error) {
	return nil, nil
}
func (f *flowTenantSvc) UpdateTenant(context.Context, *types.Tenant) (*types.Tenant, error) {
	return nil, nil
}
func (f *flowTenantSvc) DeleteTenant(context.Context, uint64) error { return nil }
func (f *flowTenantSvc) ListTenants(context.Context) ([]*types.Tenant, error) {
	return nil, nil
}
func (f *flowTenantSvc) ListAllTenants(context.Context) ([]*types.Tenant, error) {
	return nil, nil
}
func (f *flowTenantSvc) BulkSetStorageQuota(context.Context, int64) (int64, error) {
	return 0, nil
}
func (f *flowTenantSvc) SearchTenants(context.Context, string, uint64, int, int) ([]*types.Tenant, int64, error) {
	return nil, 0, nil
}
func (f *flowTenantSvc) GetTenantByIDForUser(context.Context, uint64, string) (*types.Tenant, error) {
	return f.tenant, nil
}
func (f *flowTenantSvc) GetWeKnoraCloudCredentials(context.Context) *types.WeKnoraCloudCredentials {
	return nil
}

func TestEmbedExchangeFlowIntegration(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const (
		channelID    = "ch-flow-1"
		publishToken = "em_publish_valid"
	)
	svc := &flowEmbedSvc{
		sessionToken: "ems_integration_token",
		expiresIn:    1800,
		channels: map[string]*types.EmbedChannel{
			channelID: {
				ID:                 channelID,
				TenantID:           7,
				AgentID:            "agent-flow-1",
				Enabled:            true,
				PublishToken:       publishToken,
				AllowedOrigins:     []byte(`["https://partner.example.com"]`),
				RateLimitPerMinute: 0,
			},
		},
	}
	h := &EmbedChannelHandler{embedSvc: svc}
	tenantSvc := &flowTenantSvc{tenant: &types.Tenant{ID: 7}}

	r := gin.New()
	r.POST(
		"/api/v1/embed/:channel_id/exchange",
		middleware.EmbedAuth(svc, tenantSvc, nil),
		h.ExchangeEmbedSession,
	)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/embed/"+channelID+"/exchange", nil)
	req.Header.Set("Authorization", "Embed "+publishToken)
	req.Header.Set("Origin", "https://partner.example.com")
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
	if !resp.Success {
		t.Fatalf("expected success, got %#v", resp)
	}
	if !strings.HasPrefix(resp.Data.SessionToken, "ems_") {
		t.Fatalf("session_token = %q, want ems_ prefix", resp.Data.SessionToken)
	}
	if resp.Data.SessionToken != "ems_integration_token" || resp.Data.ExpiresIn != 1800 {
		t.Fatalf("unexpected exchange payload: %#v", resp.Data)
	}
}

func TestPatchEmbedChatPayloadInjectsAgentID(t *testing.T) {
	ch := &types.EmbedChannel{AgentID: "agent-embed-42"}
	body := `{"query":"hello","agent_id":"client-override","web_search_enabled":true}`

	patched, err := patchEmbedChatPayload(strings.NewReader(body), ch, false)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(patched, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["agent_id"] != "agent-embed-42" {
		t.Fatalf("agent_id = %v, want channel agent", payload["agent_id"])
	}
	if payload["query"] != "hello" {
		t.Fatalf("query = %v, want preserved client field", payload["query"])
	}
	if payload["web_search_enabled"] != false {
		t.Fatalf("web_search_enabled = %v, want false", payload["web_search_enabled"])
	}
	if payload["agent_enabled"] != false {
		t.Fatalf("agent_enabled = %v, want false for knowledge mode", payload["agent_enabled"])
	}
	kbIDs, ok := payload["knowledge_base_ids"].([]any)
	if !ok || len(kbIDs) != 0 {
		t.Fatalf("knowledge_base_ids = %v, want empty slice", payload["knowledge_base_ids"])
	}
}

func TestPatchEmbedChatPayloadWebSearchRequiresClientOptIn(t *testing.T) {
	ch := &types.EmbedChannel{AgentID: "agent-1", AllowWebSearch: true}
	body := `{"query":"hello","web_search_enabled":false,"enable_memory":true}`

	patched, err := patchEmbedChatPayload(strings.NewReader(body), ch, false)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(patched, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["web_search_enabled"] != false {
		t.Fatalf("web_search_enabled = %v, want false when visitor did not opt in", payload["web_search_enabled"])
	}
	if payload["enable_memory"] != false {
		t.Fatalf("enable_memory = %v, want false (embed memory is disabled)", payload["enable_memory"])
	}

	bodyOn := `{"query":"hello","web_search_enabled":true}`
	patchedOn, err := patchEmbedChatPayload(strings.NewReader(bodyOn), ch, false)
	if err != nil {
		t.Fatal(err)
	}
	var payloadOn map[string]any
	if err := json.Unmarshal(patchedOn, &payloadOn); err != nil {
		t.Fatal(err)
	}
	if payloadOn["web_search_enabled"] != true {
		t.Fatalf("web_search_enabled = %v, want true when channel allows and visitor opted in", payloadOn["web_search_enabled"])
	}
}

func TestPatchEmbedChatPayloadWebSearchBlockedWhenChannelDisabled(t *testing.T) {
	ch := &types.EmbedChannel{AgentID: "agent-1", AllowWebSearch: false}
	body := `{"query":"hello","web_search_enabled":true}`

	patched, err := patchEmbedChatPayload(strings.NewReader(body), ch, false)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(patched, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["web_search_enabled"] != false {
		t.Fatalf("web_search_enabled = %v, want false when channel disallows web search", payload["web_search_enabled"])
	}
}

func TestPatchEmbedChatPayloadStripsAttachmentsWhenUploadDisabled(t *testing.T) {
	ch := &types.EmbedChannel{AgentID: "agent-1", AllowFileUpload: false}
	body := `{"query":"hello","images":[{"data":"x"}],"attachment_uploads":[{"file_name":"a.pdf"}],"attachment_ids":["doc-1"]}`

	patched, err := patchEmbedChatPayload(strings.NewReader(body), ch, false)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(patched, &payload); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"images", "attachment_uploads", "attachment_ids"} {
		if _, ok := payload[key]; ok {
			t.Fatalf("%s should be stripped when allow_file_upload is false, got %v", key, payload[key])
		}
	}
}

func TestPatchEmbedChatPayloadKeepsAttachmentIDsWhenUploadAllowed(t *testing.T) {
	ch := &types.EmbedChannel{AgentID: "agent-1", AllowFileUpload: true}
	body := `{"query":"hello","attachment_ids":["doc-1","doc-2"]}`

	patched, err := patchEmbedChatPayload(strings.NewReader(body), ch, false)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(patched, &payload); err != nil {
		t.Fatal(err)
	}
	ids, ok := payload["attachment_ids"].([]any)
	if !ok || len(ids) != 2 {
		t.Fatalf("attachment_ids = %v, want preserved when upload allowed", payload["attachment_ids"])
	}
}

func TestPatchEmbedChatPayloadAgentMode(t *testing.T) {
	ch := &types.EmbedChannel{AgentID: "agent-embed-99"}
	patched, err := patchEmbedChatPayload(bytes.NewReader(nil), ch, true)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(patched, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["agent_id"] != "agent-embed-99" {
		t.Fatalf("agent_id = %v", payload["agent_id"])
	}
	if payload["agent_enabled"] != true {
		t.Fatalf("agent_enabled = %v, want true", payload["agent_enabled"])
	}
}

func TestPatchEmbedChatPayloadInvalidJSON(t *testing.T) {
	ch := &types.EmbedChannel{AgentID: "agent-1"}
	_, err := patchEmbedChatPayload(strings.NewReader("{not-json"), ch, false)
	if err == nil || !strings.Contains(err.Error(), "invalid embed chat json") {
		t.Fatalf("expected invalid json error, got %v", err)
	}
}

func TestPatchEmbedChatPayloadInvalidBody(t *testing.T) {
	ch := &types.EmbedChannel{AgentID: "agent-1"}
	_, err := patchEmbedChatPayload(badReader{}, ch, false)
	if err == nil || !strings.Contains(err.Error(), "invalid embed chat request body") {
		t.Fatalf("expected invalid body error, got %v", err)
	}
}

type badReader struct{}

func (badReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
