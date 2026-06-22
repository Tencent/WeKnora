package service

import (
	"context"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/mcp"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
	require.NoError(t, svc.UpdateMCPService(ctx, upd))

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
	require.NoError(t, svc.UpdateMCPService(ctx, upd))

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
	require.NoError(t, svc.UpdateMCPService(ctx, upd))

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
