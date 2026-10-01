package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/mcp"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
	sdkmcp "github.com/mark3labs/mcp-go/mcp"
	sdkserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTestMCPServiceToolDiscovery(t *testing.T) {
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
	const oneTool = `{"tools":[{"name":"lookup","inputSchema":{"type":"object"}}]}`
	for _, tt := range []struct {
		name       string
		noTools    bool
		tools      string
		code       int
		secondPage bool
		cancel     bool
		initError  bool
		wantOK     bool
		wantTools  int
		wantCalls  int32
	}{
		{name: "tools available", tools: oneTool, wantOK: true, wantTools: 1, wantCalls: 1},
		{name: "empty directory", tools: `{"tools":[]}`, wantOK: true, wantCalls: 1},
		{name: "no tools capability", noTools: true, code: -32601, wantOK: true},
		{name: "tools RPC error", code: -32603, wantCalls: 1},
		{name: "advertised tools method missing", code: -32601, wantCalls: 1},
		{name: "second page fails", code: -32603, secondPage: true, wantCalls: 2},
		{name: "canceled during discovery", cancel: true, wantCalls: 1},
		{name: "initialization fails", initError: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			var calls atomic.Int32
			var resources atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				var request struct {
					ID     json.RawMessage `json:"id"`
					Method string          `json:"method"`
					Params struct {
						Cursor string `json:"cursor"`
					} `json:"params"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				if len(request.ID) == 0 {
					w.WriteHeader(http.StatusAccepted)
					return
				}
				response := map[string]any{"jsonrpc": "2.0", "id": request.ID}
				switch request.Method {
				case "initialize":
					if tt.initError {
						response["error"] = map[string]any{"code": -32603, "message": "initialization unavailable"}
						break
					}
					capabilities := map[string]any{}
					if !tt.noTools {
						capabilities["tools"] = map[string]any{}
					}
					response["result"] = map[string]any{
						"protocolVersion": sdkmcp.LATEST_PROTOCOL_VERSION,
						"capabilities":    capabilities,
						"serverInfo": map[string]string{
							"name": "probe", "version": "1", "description": "Probe server",
						},
					}
				case "tools/list":
					calls.Add(1)
					if tt.cancel {
						cancel()
						return
					}
					if tt.secondPage && request.Params.Cursor == "" {
						response["result"] = json.RawMessage(
							`{"tools":[{"name":"partial","inputSchema":{"type":"object"}}],"nextCursor":"second"}`,
						)
					} else if tt.code != 0 {
						response["error"] = map[string]any{"code": tt.code, "message": "directory unavailable"}
					} else {
						response["result"] = json.RawMessage(tt.tools)
					}
				case "resources/list":
					resources.Add(1)
					response["error"] = map[string]any{"code": -32601, "message": "resources not supported"}
				default:
					t.Errorf("unexpected request: %s", request.Method)
				}
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(response); err != nil {
					t.Errorf("encode MCP response: %v", err)
				}
			}))
			defer upstream.Close()
			svc, repo := newTestService()
			t.Cleanup(svc.mcpManager.Shutdown)
			require.NoError(t, repo.Create(ctx, &types.MCPService{
				ID: "probe", TenantID: 1, URL: &upstream.URL, TransportType: types.MCPTransportHTTPStreamable,
			}))
			result, err := svc.TestMCPService(ctx, 1, "probe")
			require.NoError(t, err)
			require.NotNil(t, result)
			assert.Equal(t, tt.wantCalls, calls.Load(), "must reach the intended protocol phase")
			assert.Equal(t, tt.wantOK, result.Success, result.Message)
			assert.Len(t, result.Tools, tt.wantTools, "never return a partial directory")
			if tt.wantOK {
				assert.Contains(t, result.Message, "Connected successfully to probe v1")
				assert.Equal(t, "Probe server", result.Description)
				assert.Equal(t, int32(1), resources.Load(), "unsupported resources remain non-fatal")
				if tt.wantTools > 0 {
					require.Len(t, result.Tools, 1)
					assert.Equal(t, "lookup", result.Tools[0].Name)
					assert.JSONEq(t, `{"type":"object"}`, string(result.Tools[0].InputSchema))
				}
			} else {
				assert.NotContains(t, result.Message, "Connected successfully")
				assert.Zero(t, resources.Load(), "failed discovery must return immediately")
				if tt.initError {
					assert.Contains(t, result.Message, "Initialization failed")
				} else {
					assert.Contains(t, result.Message, "Tool discovery failed")
					if tt.code != 0 {
						assert.Contains(t, result.Message, "directory unavailable")
					}
				}
			}
		})
	}
}

func TestTestMCPServiceSSEToolDiscovery(t *testing.T) {
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("failure=%v", fail), func(t *testing.T) {
			var calls atomic.Int32
			hooks := &sdkserver.Hooks{}
			hooks.AddOnRequestInitialization(func(_ context.Context, _ any, request any) error {
				var message struct {
					Method string `json:"method"`
				}
				if err := json.Unmarshal(request.(json.RawMessage), &message); err != nil {
					return err
				}
				if message.Method == "tools/list" {
					calls.Add(1)
					if fail {
						return errors.New("directory unavailable")
					}
				}
				return nil
			})
			server := sdkserver.NewMCPServer(
				"probe", "1", sdkserver.WithToolCapabilities(false), sdkserver.WithHooks(hooks),
			)
			upstream := httptest.NewServer(sdkserver.NewSSEServer(server))
			defer upstream.Close()
			url := upstream.URL + "/sse"
			svc, repo := newTestService()
			t.Cleanup(svc.mcpManager.Shutdown)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			require.NoError(t, repo.Create(ctx, &types.MCPService{
				ID: "probe", TenantID: 1, URL: &url, TransportType: types.MCPTransportSSE,
			}))
			result, err := svc.TestMCPService(ctx, 1, "probe")
			require.NoError(t, err)
			require.NotNil(t, result)
			assert.Equal(t, int32(1), calls.Load())
			assert.Equal(t, !fail, result.Success, result.Message)
			if fail {
				assert.Contains(t, result.Message, "Tool discovery failed")
				assert.Contains(t, result.Message, "directory unavailable")
			}
		})
	}
}

func TestMCPTestFailurePreservesOAuthRequired(t *testing.T) {
	err := fmt.Errorf("failed to list tools: %w", &mcp.OAuthRequiredError{MetadataURL: "https://example.com/metadata"})
	result := mcpTestFailure(err, "Tool discovery failed")
	assert.False(t, result.Success)
	assert.True(t, result.OAuthRequired)
	assert.Contains(t, result.Message, "OAuth")
}

// fakeMCPRepo is a minimal in-memory implementation of
// interfaces.MCPServiceRepository for testing the service-layer logic
// without depending on the database.
type fakeMCPRepo struct {
	store map[string]*types.MCPService
}

func newFakeMCPRepo() *fakeMCPRepo {
	return &fakeMCPRepo{store: make(map[string]*types.MCPService)}
}

func (r *fakeMCPRepo) Create(_ context.Context, s *types.MCPService) error {
	r.store[s.ID] = cloneService(s)
	return nil
}

func (r *fakeMCPRepo) GetByID(_ context.Context, _ uint64, id string) (*types.MCPService, error) {
	s, ok := r.store[id]
	if !ok {
		return nil, nil
	}
	// Return a copy so service-layer mutations don't leak back into the
	// store, mirroring how GORM returns a fresh struct per query.
	return cloneService(s), nil
}

func cloneService(s *types.MCPService) *types.MCPService {
	cp := *s
	if s.AuthConfig != nil {
		ac := *s.AuthConfig
		if s.AuthConfig.CustomHeaders != nil {
			ac.CustomHeaders = make(map[string]string, len(s.AuthConfig.CustomHeaders))
			for k, v := range s.AuthConfig.CustomHeaders {
				ac.CustomHeaders[k] = v
			}
		}
		cp.AuthConfig = &ac
	}
	if s.AdvancedConfig != nil {
		adv := *s.AdvancedConfig
		cp.AdvancedConfig = &adv
	}
	return &cp
}

func (r *fakeMCPRepo) List(_ context.Context, _ uint64) ([]*types.MCPService, error) {
	out := make([]*types.MCPService, 0, len(r.store))
	for _, s := range r.store {
		out = append(out, cloneService(s))
	}
	return out, nil
}

func (r *fakeMCPRepo) ListEnabled(ctx context.Context, tenantID uint64) ([]*types.MCPService, error) {
	return r.List(ctx, tenantID)
}

func (r *fakeMCPRepo) ListByIDs(_ context.Context, _ uint64, ids []string) ([]*types.MCPService, error) {
	out := make([]*types.MCPService, 0, len(ids))
	for _, id := range ids {
		if s, ok := r.store[id]; ok {
			out = append(out, cloneService(s))
		}
	}
	return out, nil
}

func (r *fakeMCPRepo) Update(_ context.Context, s *types.MCPService) error {
	r.store[s.ID] = cloneService(s)
	return nil
}

func (r *fakeMCPRepo) Delete(_ context.Context, _ uint64, id string) error {
	delete(r.store, id)
	return nil
}

func seedService(t *testing.T, repo *fakeMCPRepo, apiKey, token string) string {
	t.Helper()
	s := &types.MCPService{
		ID:            "svc-test",
		TenantID:      1,
		Name:          "test",
		Enabled:       true,
		TransportType: types.MCPTransportSSE,
		AuthConfig: &types.MCPAuthConfig{
			APIKey: apiKey,
			Token:  token,
		},
	}
	require.NoError(t, repo.Create(context.Background(), s))
	return s.ID
}

// newTestService wires up a mcpServiceService with a fresh fake repo and a
// real (empty) MCPManager. CloseClient on an empty manager is a no-op.
func newTestService() (*mcpServiceService, *fakeMCPRepo) {
	repo := newFakeMCPRepo()
	svc := &mcpServiceService{
		mcpServiceRepo: repo,
		mcpManager:     mcp.NewMCPManager(nil),
		oauthRepo:      nil,
	}
	return svc, repo
}

func TestUpdateMCPService_RespectsScalarFieldPresence(t *testing.T) {
	tests := []struct {
		name            string
		update          *types.MCPService
		updateFields    map[string]bool
		wantName        string
		wantDescription string
		wantEnabled     bool
	}{
		{
			name:            "description only",
			update:          &types.MCPService{Description: "after"},
			updateFields:    map[string]bool{"description": true},
			wantName:        "test",
			wantDescription: "after",
			wantEnabled:     true,
		},
		{
			name:            "name only",
			update:          &types.MCPService{Name: "renamed"},
			updateFields:    map[string]bool{"name": true},
			wantName:        "renamed",
			wantDescription: "before",
			wantEnabled:     true,
		},
		{
			name:            "explicit empty description",
			update:          &types.MCPService{Description: ""},
			updateFields:    map[string]bool{"description": true},
			wantName:        "test",
			wantDescription: "",
			wantEnabled:     true,
		},
		{
			name:            "explicit disable",
			update:          &types.MCPService{Enabled: false},
			updateFields:    map[string]bool{"enabled": true},
			wantName:        "test",
			wantDescription: "before",
			wantEnabled:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			svc, repo := newTestService()
			id := seedService(t, repo, "stored-api", "stored-token")
			repo.store[id].Description = "before"

			tt.update.ID = id
			tt.update.TenantID = 1
			require.NoError(t, svc.UpdateMCPService(ctx, tt.update, tt.updateFields))

			got := repo.store[id]
			assert.Equal(t, tt.wantName, got.Name)
			assert.Equal(t, tt.wantDescription, got.Description)
			assert.Equal(t, tt.wantEnabled, got.Enabled)
		})
	}
}

func TestUpdateMCPService_AppliesNonScalarUpdateWithoutName(t *testing.T) {
	ctx := context.Background()
	svc, repo := newTestService()
	id := seedService(t, repo, "stored-api", "stored-token")
	// Use resolvable example.com paths: subdomains like before.example.com fail
	// SSRF DNS checks because they do not resolve to a public IP.
	beforeURL := "https://example.com/before"
	repo.store[id].Description = "before"
	repo.store[id].URL = &beforeURL

	afterURL := "https://example.com/after"
	update := &types.MCPService{
		ID:       id,
		TenantID: 1,
		URL:      &afterURL,
	}
	require.NoError(t, svc.UpdateMCPService(ctx, update, nil))

	got := repo.store[id]
	require.NotNil(t, got.URL)
	assert.Equal(t, afterURL, *got.URL)
	assert.Equal(t, "test", got.Name)
	assert.Equal(t, "before", got.Description)
	assert.True(t, got.Enabled)
}

// ---- UpdateMCPService: must not touch APIKey/Token even when caller sends them ----

// The handler now strips api_key/token from the main PUT body, but defense
// in depth: if a future caller (CLI / test / misconfigured proxy) still
// passes auth_config with secret fields, UpdateMCPService must NOT clobber
// the stored credentials. Credentials live behind the dedicated subresource.
func TestUpdateMCPService_DoesNotTouchSecretsEvenIfPassed(t *testing.T) {
	ctx := context.Background()
	svc, repo := newTestService()
	id := seedService(t, repo, "stored-api", "stored-token")

	upd := &types.MCPService{
		ID:            id,
		TenantID:      1,
		Name:          "renamed",
		Enabled:       true,
		TransportType: types.MCPTransportSSE,
		// Hostile body: tries to overwrite both secrets via main PUT.
		AuthConfig: &types.MCPAuthConfig{
			APIKey: "should-not-overwrite",
			Token:  "should-not-overwrite-either",
		},
	}
	require.NoError(t, svc.UpdateMCPService(ctx, upd, map[string]bool{
		"name":    true,
		"enabled": true,
	}))

	got := repo.store[id]
	assert.Equal(t, "stored-api", got.AuthConfig.APIKey,
		"main PUT must not overwrite stored APIKey under any circumstance")
	assert.Equal(t, "stored-token", got.AuthConfig.Token,
		"main PUT must not overwrite stored Token under any circumstance")
	assert.Equal(t, "renamed", got.Name, "non-secret field updates still apply")
}

func TestUpdateMCPService_CustomHeadersPreserveOnNil(t *testing.T) {
	ctx := context.Background()
	svc, repo := newTestService()
	id := seedService(t, repo, "stored-api", "stored-token")
	repo.store[id].AuthConfig.CustomHeaders = map[string]string{"X-Tenant": "acme"}

	upd := &types.MCPService{
		ID:            id,
		TenantID:      1,
		Name:          "test",
		Enabled:       true,
		TransportType: types.MCPTransportSSE,
		// AuthConfig present but CustomHeaders nil → preserve.
		AuthConfig: &types.MCPAuthConfig{},
	}
	require.NoError(t, svc.UpdateMCPService(ctx, upd, nil))

	got := repo.store[id]
	assert.Equal(t, "acme", got.AuthConfig.CustomHeaders["X-Tenant"],
		"nil CustomHeaders in request must preserve existing headers")
}

func TestUpdateMCPService_CustomHeadersReplaceOnNonNil(t *testing.T) {
	ctx := context.Background()
	svc, repo := newTestService()
	id := seedService(t, repo, "stored-api", "stored-token")
	repo.store[id].AuthConfig.CustomHeaders = map[string]string{"X-Tenant": "acme"}

	upd := &types.MCPService{
		ID:            id,
		TenantID:      1,
		Name:          "test",
		Enabled:       true,
		TransportType: types.MCPTransportSSE,
		AuthConfig: &types.MCPAuthConfig{
			CustomHeaders: map[string]string{"X-Replaced": "yes"},
		},
	}
	require.NoError(t, svc.UpdateMCPService(ctx, upd, nil))

	got := repo.store[id]
	assert.Equal(t, map[string]string{"X-Replaced": "yes"}, got.AuthConfig.CustomHeaders,
		"non-nil CustomHeaders must replace the stored map")
}

// ---- UpdateMCPCredentials: write path ----

func TestUpdateMCPCredentials_WritesAPIKey(t *testing.T) {
	ctx := context.Background()
	svc, repo := newTestService()
	id := seedService(t, repo, "", "")

	newKey := "fresh-api-key"
	got, err := svc.UpdateMCPCredentials(ctx, 1, id, &newKey, nil)
	require.NoError(t, err)
	require.NotNil(t, got.AuthConfig)
	assert.Equal(t, "fresh-api-key", got.AuthConfig.APIKey)
	assert.Empty(t, got.AuthConfig.Token, "untouched field stays untouched")

	stored := repo.store[id]
	assert.Equal(t, "fresh-api-key", stored.AuthConfig.APIKey, "persisted")
}

func TestUpdateMCPCredentials_NilPointerIsNoop(t *testing.T) {
	ctx := context.Background()
	svc, repo := newTestService()
	id := seedService(t, repo, "stored-api", "stored-token")

	got, err := svc.UpdateMCPCredentials(ctx, 1, id, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "stored-api", got.AuthConfig.APIKey)
	assert.Equal(t, "stored-token", got.AuthConfig.Token)
}

func TestUpdateMCPCredentials_EmptyStringIsNoop(t *testing.T) {
	ctx := context.Background()
	svc, repo := newTestService()
	id := seedService(t, repo, "stored-api", "stored-token")

	empty := ""
	got, err := svc.UpdateMCPCredentials(ctx, 1, id, &empty, &empty)
	require.NoError(t, err)
	assert.Equal(t, "stored-api", got.AuthConfig.APIKey,
		"empty string is treated as no-op; clearing goes through ClearMCPCredential")
	assert.Equal(t, "stored-token", got.AuthConfig.Token)
}

func TestUpdateMCPCredentials_ReplacesExisting(t *testing.T) {
	ctx := context.Background()
	svc, repo := newTestService()
	id := seedService(t, repo, "old-api", "old-token")

	newKey, newTok := "new-api", "new-tok"
	got, err := svc.UpdateMCPCredentials(ctx, 1, id, &newKey, &newTok)
	require.NoError(t, err)
	assert.Equal(t, "new-api", got.AuthConfig.APIKey)
	assert.Equal(t, "new-tok", got.AuthConfig.Token)
}

func TestUpdateMCPCredentials_RejectsBuiltin(t *testing.T) {
	ctx := context.Background()
	svc, repo := newTestService()
	id := seedService(t, repo, "stored-api", "")
	repo.store[id].IsBuiltin = true

	newKey := "anything"
	_, err := svc.UpdateMCPCredentials(ctx, 1, id, &newKey, nil)
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "builtin")
}

func TestUpdateMCPCredentials_ServiceNotFound(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService()
	newKey := "x"
	_, err := svc.UpdateMCPCredentials(ctx, 1, "nope", &newKey, nil)
	require.Error(t, err)
}

// ---- ClearMCPCredential ----

func TestClearMCPCredential_ClearsAPIKey(t *testing.T) {
	ctx := context.Background()
	svc, repo := newTestService()
	id := seedService(t, repo, "stored-api", "stored-token")

	require.NoError(t, svc.ClearMCPCredential(ctx, 1, id, "api_key"))
	stored := repo.store[id]
	assert.Empty(t, stored.AuthConfig.APIKey)
	assert.Equal(t, "stored-token", stored.AuthConfig.Token, "other field untouched")
}

func TestClearMCPCredential_ClearsToken(t *testing.T) {
	ctx := context.Background()
	svc, repo := newTestService()
	id := seedService(t, repo, "stored-api", "stored-token")

	require.NoError(t, svc.ClearMCPCredential(ctx, 1, id, "token"))
	stored := repo.store[id]
	assert.Equal(t, "stored-api", stored.AuthConfig.APIKey)
	assert.Empty(t, stored.AuthConfig.Token)
}

func TestClearMCPCredential_IdempotentOnEmpty(t *testing.T) {
	ctx := context.Background()
	svc, repo := newTestService()
	id := seedService(t, repo, "stored-api", "") // token already empty

	require.NoError(t, svc.ClearMCPCredential(ctx, 1, id, "token"),
		"clearing already-empty field must not error")
	stored := repo.store[id]
	assert.Equal(t, "stored-api", stored.AuthConfig.APIKey)
	assert.Empty(t, stored.AuthConfig.Token)
}

func TestClearMCPCredential_UnknownFieldErrors(t *testing.T) {
	ctx := context.Background()
	svc, repo := newTestService()
	id := seedService(t, repo, "stored-api", "")

	err := svc.ClearMCPCredential(ctx, 1, id, "bogus")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown")
}

func TestClearMCPCredential_RejectsBuiltin(t *testing.T) {
	ctx := context.Background()
	svc, repo := newTestService()
	id := seedService(t, repo, "stored-api", "")
	repo.store[id].IsBuiltin = true

	err := svc.ClearMCPCredential(ctx, 1, id, "api_key")
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "builtin")
}

// ---- Service-layer Get/List: returns RAW entity (DTO handles redaction) ----

// After the credential-subresource refactor, the service-layer Get/List
// return the entity unmodified. Handlers MUST convert via
// dto.NewMCPServiceResponse, but the credentials handler depends on the
// unredacted form to derive metadata (configured: bool).
func TestGetMCPServiceByID_ReturnsRawCredentials(t *testing.T) {
	ctx := context.Background()
	svc, repo := newTestService()
	id := seedService(t, repo, "real-api", "real-token")

	got, err := svc.GetMCPServiceByID(ctx, 1, id)
	require.NoError(t, err)
	require.NotNil(t, got.AuthConfig)
	assert.Equal(t, "real-api", got.AuthConfig.APIKey,
		"service layer returns raw credentials; redaction is the DTO's job")
	assert.Equal(t, "real-token", got.AuthConfig.Token)
}

func TestListMCPServices_ReturnsRawCredentials(t *testing.T) {
	ctx := context.Background()
	svc, repo := newTestService()
	seedService(t, repo, "real-api", "real-token")

	got, err := svc.ListMCPServices(ctx, 1)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "real-api", got[0].AuthConfig.APIKey)
	assert.Equal(t, "real-token", got[0].AuthConfig.Token)
}
