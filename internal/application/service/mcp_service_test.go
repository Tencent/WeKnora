package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/mcp"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeMCPRepo is a minimal in-memory implementation of
// interfaces.MCPServiceRepository for testing the service-layer merge logic
// without depending on the database.
type fakeMCPRepo struct {
	store map[string]*types.MCPService
}

func newFakeMCPRepo() *fakeMCPRepo {
	return &fakeMCPRepo{store: make(map[string]*types.MCPService)}
}

func (r *fakeMCPRepo) Create(_ context.Context, s *types.MCPService) error {
	r.store[s.ID] = s
	return nil
}

func (r *fakeMCPRepo) GetByID(_ context.Context, _ uint64, id string) (*types.MCPService, error) {
	s, ok := r.store[id]
	if !ok {
		return nil, nil
	}
	// return a deep-ish copy so service-layer mutations to the returned value
	// do not leak back into the fake store, mirroring how GORM produces a
	// fresh struct per query
	cp := *s
	if s.AuthConfig != nil {
		ac := *s.AuthConfig
		cp.AuthConfig = &ac
	}
	if s.AdvancedConfig != nil {
		adv := *s.AdvancedConfig
		cp.AdvancedConfig = &adv
	}
	return &cp, nil
}

func cloneService(s *types.MCPService) *types.MCPService {
	cp := *s
	if s.AuthConfig != nil {
		ac := *s.AuthConfig
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
	r.store[s.ID] = s
	return nil
}

func (r *fakeMCPRepo) Delete(_ context.Context, _ uint64, id string) error {
	delete(r.store, id)
	return nil
}

// seedService plants a service with the given auth credentials into the fake
// repo and returns the service ID. Convenience helper used by every test.
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
// real (empty) MCPManager. CloseClient on an empty manager is a no-op, so
// tests can safely exercise the configChanged code paths.
func newTestService() (*mcpServiceService, *fakeMCPRepo) {
	repo := newFakeMCPRepo()
	svc := &mcpServiceService{
		mcpServiceRepo: repo,
		mcpManager:     mcp.NewMCPManager(),
	}
	return svc, repo
}

// buildFullUpdate returns an UpdateMCPService input that exercises the
// "full update" branch (service.Name is non-empty), which is the branch that
// actually merges AuthConfig.
func buildFullUpdate(id string, auth *types.MCPAuthConfig) *types.MCPService {
	return &types.MCPService{
		ID:            id,
		TenantID:      1,
		Name:          "test", // non-empty → full update branch
		Enabled:       true,
		TransportType: types.MCPTransportSSE,
		AuthConfig:    auth,
	}
}

func TestUpdateMCPService_PreservesSecretsOnEmptyOrRedacted(t *testing.T) {
	ctx := context.Background()

	t.Run("nil AuthConfig preserves existing", func(t *testing.T) {
		svc, repo := newTestService()
		id := seedService(t, repo, "stored-api", "stored-token")

		upd := buildFullUpdate(id, nil)
		require.NoError(t, svc.UpdateMCPService(ctx, upd))

		got := repo.store[id]
		assert.Equal(t, "stored-api", got.AuthConfig.APIKey)
		assert.Equal(t, "stored-token", got.AuthConfig.Token)
	})

	t.Run("empty string preserves existing", func(t *testing.T) {
		svc, repo := newTestService()
		id := seedService(t, repo, "stored-api", "stored-token")

		upd := buildFullUpdate(id, &types.MCPAuthConfig{APIKey: "", Token: ""})
		require.NoError(t, svc.UpdateMCPService(ctx, upd))

		got := repo.store[id]
		assert.Equal(t, "stored-api", got.AuthConfig.APIKey)
		assert.Equal(t, "stored-token", got.AuthConfig.Token)
	})

	t.Run("redacted placeholder preserves existing (NELO-1299 round-trip)", func(t *testing.T) {
		svc, repo := newTestService()
		id := seedService(t, repo, "stored-api", "stored-token")

		upd := buildFullUpdate(id, &types.MCPAuthConfig{
			APIKey: types.RedactedSecretPlaceholder,
			Token:  types.RedactedSecretPlaceholder,
		})
		require.NoError(t, svc.UpdateMCPService(ctx, upd))

		got := repo.store[id]
		assert.Equal(t, "stored-api", got.AuthConfig.APIKey)
		assert.Equal(t, "stored-token", got.AuthConfig.Token)
	})
}

func TestUpdateMCPService_ReplacesOnExplicitValue(t *testing.T) {
	ctx := context.Background()
	svc, repo := newTestService()
	id := seedService(t, repo, "stored-api", "stored-token")

	upd := buildFullUpdate(id, &types.MCPAuthConfig{
		APIKey: "new-api",
		Token:  "new-token",
	})
	require.NoError(t, svc.UpdateMCPService(ctx, upd))

	got := repo.store[id]
	assert.Equal(t, "new-api", got.AuthConfig.APIKey)
	assert.Equal(t, "new-token", got.AuthConfig.Token)
}

func TestUpdateMCPService_MixedFields(t *testing.T) {
	ctx := context.Background()
	svc, repo := newTestService()
	id := seedService(t, repo, "stored-api", "stored-token")

	// Replace token only, leave api_key as redacted placeholder → preserve
	upd := buildFullUpdate(id, &types.MCPAuthConfig{
		APIKey: types.RedactedSecretPlaceholder,
		Token:  "new-token",
	})
	require.NoError(t, svc.UpdateMCPService(ctx, upd))

	got := repo.store[id]
	assert.Equal(t, "stored-api", got.AuthConfig.APIKey, "api_key should be preserved")
	assert.Equal(t, "new-token", got.AuthConfig.Token, "token should be replaced")
}

func TestUpdateMCPService_ClearFlags(t *testing.T) {
	ctx := context.Background()

	t.Run("ClearToken removes stored token, preserves api_key", func(t *testing.T) {
		svc, repo := newTestService()
		id := seedService(t, repo, "stored-api", "stored-token")

		upd := buildFullUpdate(id, &types.MCPAuthConfig{
			APIKey:     types.RedactedSecretPlaceholder,
			ClearToken: true,
		})
		require.NoError(t, svc.UpdateMCPService(ctx, upd))

		got := repo.store[id]
		assert.Equal(t, "stored-api", got.AuthConfig.APIKey)
		assert.Empty(t, got.AuthConfig.Token)
	})

	t.Run("ClearAPIKey removes stored api_key, preserves token", func(t *testing.T) {
		svc, repo := newTestService()
		id := seedService(t, repo, "stored-api", "stored-token")

		upd := buildFullUpdate(id, &types.MCPAuthConfig{
			ClearAPIKey: true,
			Token:       types.RedactedSecretPlaceholder,
		})
		require.NoError(t, svc.UpdateMCPService(ctx, upd))

		got := repo.store[id]
		assert.Empty(t, got.AuthConfig.APIKey)
		assert.Equal(t, "stored-token", got.AuthConfig.Token)
	})

	t.Run("ClearToken takes precedence over submitted value", func(t *testing.T) {
		svc, repo := newTestService()
		id := seedService(t, repo, "stored-api", "stored-token")

		upd := buildFullUpdate(id, &types.MCPAuthConfig{
			Token:      "will-be-ignored",
			ClearToken: true,
		})
		require.NoError(t, svc.UpdateMCPService(ctx, upd))

		got := repo.store[id]
		assert.Empty(t, got.AuthConfig.Token)
	})

	t.Run("clear flags are not persisted", func(t *testing.T) {
		svc, repo := newTestService()
		id := seedService(t, repo, "stored-api", "stored-token")

		upd := buildFullUpdate(id, &types.MCPAuthConfig{ClearToken: true})
		require.NoError(t, svc.UpdateMCPService(ctx, upd))

		got := repo.store[id]
		require.NotNil(t, got.AuthConfig)
		assert.False(t, got.AuthConfig.ClearToken, "ClearToken must not be persisted")
		assert.False(t, got.AuthConfig.ClearAPIKey, "ClearAPIKey must not be persisted")
	})

	t.Run("clear on already-empty field is a no-op", func(t *testing.T) {
		svc, repo := newTestService()
		id := seedService(t, repo, "stored-api", "") // token not set

		upd := buildFullUpdate(id, &types.MCPAuthConfig{ClearToken: true})
		require.NoError(t, svc.UpdateMCPService(ctx, upd))

		got := repo.store[id]
		assert.Equal(t, "stored-api", got.AuthConfig.APIKey)
		assert.Empty(t, got.AuthConfig.Token)
	})
}

func TestListMCPServices_RedactsSecrets(t *testing.T) {
	ctx := context.Background()
	svc, repo := newTestService()
	seedService(t, repo, "real-api", "real-token")

	got, err := svc.ListMCPServices(ctx, 1)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, types.RedactedSecretPlaceholder, got[0].AuthConfig.APIKey)
	assert.Equal(t, types.RedactedSecretPlaceholder, got[0].AuthConfig.Token)

	// And the underlying store is untouched
	stored := repo.store["svc-test"]
	assert.Equal(t, "real-api", stored.AuthConfig.APIKey)
	assert.Equal(t, "real-token", stored.AuthConfig.Token)
}

func TestGetMCPServiceByID_RedactsSecrets(t *testing.T) {
	ctx := context.Background()
	svc, repo := newTestService()
	id := seedService(t, repo, "real-api", "real-token")

	got, err := svc.GetMCPServiceByID(ctx, 1, id)
	require.NoError(t, err)
	require.NotNil(t, got.AuthConfig)
	assert.Equal(t, types.RedactedSecretPlaceholder, got.AuthConfig.APIKey)
	assert.Equal(t, types.RedactedSecretPlaceholder, got.AuthConfig.Token)

	// underlying store unchanged
	stored := repo.store[id]
	assert.Equal(t, "real-api", stored.AuthConfig.APIKey)
	assert.Equal(t, "real-token", stored.AuthConfig.Token)
}

func TestGetMCPServiceByID_EmptySecretsStayEmpty(t *testing.T) {
	ctx := context.Background()
	svc, repo := newTestService()
	id := seedService(t, repo, "", "") // both empty

	got, err := svc.GetMCPServiceByID(ctx, 1, id)
	require.NoError(t, err)
	require.NotNil(t, got.AuthConfig)
	assert.Empty(t, got.AuthConfig.APIKey, "empty secret must stay empty, not be redacted")
	assert.Empty(t, got.AuthConfig.Token)
}

// Partial update: service.Name == "" is interpreted as "only touch enabled".
// A clear flag sent in such a body was previously dropped; the refactor now
// merges AuthConfig unconditionally so single-field clears round-trip.
func TestUpdateMCPService_PartialUpdateHonorsClearFlag(t *testing.T) {
	ctx := context.Background()
	svc, repo := newTestService()
	id := seedService(t, repo, "stored-api", "stored-token")

	// Partial update body: no name, enabled flipped, ClearToken set.
	upd := &types.MCPService{
		ID:       id,
		TenantID: 1,
		Enabled:  true, // keep enabled, but simulate a "flag only" request
		AuthConfig: &types.MCPAuthConfig{
			ClearToken: true,
		},
	}
	require.NoError(t, svc.UpdateMCPService(ctx, upd))

	got := repo.store[id]
	assert.Equal(t, "stored-api", got.AuthConfig.APIKey, "APIKey preserved in partial update")
	assert.Empty(t, got.AuthConfig.Token, "ClearToken must clear Token even in partial update")
}

// CustomHeaders: nil means "no change" (preserve existing). Sending a
// non-nil (including empty) map replaces. Previously the merge always
// overwrote, silently wiping headers on any auth_config-bearing request.
func TestUpdateMCPService_CustomHeadersPreserveOnNil(t *testing.T) {
	ctx := context.Background()
	svc, repo := newTestService()
	id := seedService(t, repo, "stored-api", "stored-token")
	// Seed with custom headers.
	repo.store[id].AuthConfig.CustomHeaders = map[string]string{"X-Tenant": "acme"}

	// Update with AuthConfig but nil CustomHeaders → preserve.
	upd := buildFullUpdate(id, &types.MCPAuthConfig{
		Token: "new-token",
	})
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

	upd := buildFullUpdate(id, &types.MCPAuthConfig{
		APIKey:        types.RedactedSecretPlaceholder,
		Token:         types.RedactedSecretPlaceholder,
		CustomHeaders: map[string]string{"X-Replaced": "yes"},
	})
	require.NoError(t, svc.UpdateMCPService(ctx, upd))

	got := repo.store[id]
	assert.Equal(t, map[string]string{"X-Replaced": "yes"}, got.AuthConfig.CustomHeaders,
		"non-nil CustomHeaders must replace the stored map")
}
