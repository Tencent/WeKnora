package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type fakeTenantAPIKeyRepo struct {
	byHash              map[string]*types.TenantAPIKey
	nextID              uint64
	lastUsedUpdateCount int
}

func TestTenantAPIKeyServiceCreateAPIKeyUsesSKPrefix(t *testing.T) {
	ctx := context.Background()
	repo := newFakeTenantAPIKeyRepo()
	svc := NewTenantAPIKeyService(repo)

	result, err := svc.CreateAPIKey(ctx, interfaces.TenantAPIKeyCreateRequest{
		TenantID: 42,
		Name:     "integration",
	})
	if err != nil {
		t.Fatalf("CreateAPIKey returned error: %v", err)
	}
	if !strings.HasPrefix(result.Token, "sk-") {
		t.Fatalf("created token = %q, want sk- prefix", result.Token)
	}
	if result.APIKey.APIKey != result.Token {
		t.Fatalf("created api_key = %q, want token %q", result.APIKey.APIKey, result.Token)
	}
}

func newFakeTenantAPIKeyRepo() *fakeTenantAPIKeyRepo {
	return &fakeTenantAPIKeyRepo{byHash: map[string]*types.TenantAPIKey{}, nextID: 1}
}

func (r *fakeTenantAPIKeyRepo) CreateAPIKey(_ context.Context, key *types.TenantAPIKey) error {
	if _, ok := r.byHash[key.KeyHash]; ok {
		return errors.New("duplicate key hash")
	}
	cp := *key
	cp.ID = r.nextID
	r.nextID++
	r.byHash[cp.KeyHash] = &cp
	key.ID = cp.ID
	return nil
}

func (r *fakeTenantAPIKeyRepo) GetAPIKeyByHash(_ context.Context, hash string) (*types.TenantAPIKey, error) {
	key, ok := r.byHash[hash]
	if !ok {
		return nil, apprepo.ErrTenantAPIKeyNotFound
	}
	cp := *key
	return &cp, nil
}

func (r *fakeTenantAPIKeyRepo) ListAPIKeys(_ context.Context, tenantID uint64) ([]*types.TenantAPIKey, error) {
	out := []*types.TenantAPIKey{}
	for _, key := range r.byHash {
		if key.TenantIDValue() == tenantID && key.RevokedAt == nil {
			cp := *key
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (r *fakeTenantAPIKeyRepo) ListPlatformAPIKeys(_ context.Context) ([]*types.TenantAPIKey, error) {
	out := []*types.TenantAPIKey{}
	for _, key := range r.byHash {
		if key.IsPlatform() && key.RevokedAt == nil {
			cp := *key
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (r *fakeTenantAPIKeyRepo) RevokeAPIKey(_ context.Context, tenantID uint64, id uint64) error {
	now := time.Now()
	for _, key := range r.byHash {
		if key.ID == id && key.TenantIDValue() == tenantID && key.RevokedAt == nil {
			key.RevokedAt = &now
			return nil
		}
	}
	return apprepo.ErrTenantAPIKeyNotFound
}

func (r *fakeTenantAPIKeyRepo) RevokePlatformAPIKey(_ context.Context, id uint64) error {
	now := time.Now()
	for _, key := range r.byHash {
		if key.ID == id && key.IsPlatform() && key.RevokedAt == nil {
			key.RevokedAt = &now
			return nil
		}
	}
	return apprepo.ErrTenantAPIKeyNotFound
}

func (r *fakeTenantAPIKeyRepo) UpdateAPIKeyHash(_ context.Context, id uint64, hash string) error {
	for oldHash, key := range r.byHash {
		if key.ID == id && key.RevokedAt == nil {
			delete(r.byHash, oldHash)
			key.KeyHash = hash
			r.byHash[hash] = key
			return nil
		}
	}
	return apprepo.ErrTenantAPIKeyNotFound
}

func (r *fakeTenantAPIKeyRepo) HasKeysWithPlaceholderHash(_ context.Context) (bool, error) {
	for _, key := range r.byHash {
		if key.RevokedAt == nil && strings.HasPrefix(key.KeyHash, "migrated-tenant-") {
			return true, nil
		}
	}
	return false, nil
}

func (r *fakeTenantAPIKeyRepo) ListKeysWithPlaceholderHash(_ context.Context) ([]*types.TenantAPIKey, error) {
	out := []*types.TenantAPIKey{}
	for _, key := range r.byHash {
		if key.RevokedAt == nil && strings.HasPrefix(key.KeyHash, "migrated-tenant-") {
			cp := *key
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (r *fakeTenantAPIKeyRepo) UpdateAPIKeyLastUsed(_ context.Context, id uint64, at time.Time) error {
	r.lastUsedUpdateCount++
	for _, key := range r.byHash {
		if key.ID == id && key.RevokedAt == nil {
			key.LastUsedAt = &at
		}
	}
	return nil
}

func TestTenantAPIKeyServiceBackfillMissingKeyHashes(t *testing.T) {
	ctx := context.Background()
	repo := newFakeTenantAPIKeyRepo()
	svc := NewTenantAPIKeyService(repo)

	token := "sk-legacy-token-value"
	legacy := &types.TenantAPIKey{
		TenantID:   uint64Pointer(7),
		Name:       "legacy",
		KeyHash:    "migrated-tenant-7",
		APIKey:     token,
		FullAccess: true,
	}
	if err := repo.CreateAPIKey(ctx, legacy); err != nil {
		t.Fatalf("CreateAPIKey returned error: %v", err)
	}

	n, err := svc.BackfillMissingKeyHashes(ctx)
	if err != nil {
		t.Fatalf("BackfillMissingKeyHashes returned error: %v", err)
	}
	if n != 1 {
		t.Fatalf("backfilled = %d, want 1", n)
	}
	if _, err := svc.AuthenticateAPIKey(ctx, token); err != nil {
		t.Fatalf("AuthenticateAPIKey after backfill returned error: %v", err)
	}
	if n, err := svc.BackfillMissingKeyHashes(ctx); err != nil || n != 0 {
		t.Fatalf("second BackfillMissingKeyHashes = (%d, %v), want (0, nil)", n, err)
	}
}

func uint64Pointer(value uint64) *uint64 { return &value }

func TestTenantAPIKeyServiceCreatesPlatformKeyWithoutTenant(t *testing.T) {
	repo := newFakeTenantAPIKeyRepo()
	svc := NewTenantAPIKeyService(repo)
	created, err := svc.CreateAPIKey(context.Background(), interfaces.TenantAPIKeyCreateRequest{
		ScopeType:    types.APIKeyScopePlatform,
		Name:         "automation",
		Capabilities: []string{string(types.APIKeyCapabilityRetrieve)},
	})
	if err != nil {
		t.Fatalf("CreateAPIKey returned error: %v", err)
	}
	if !created.APIKey.IsPlatform() || created.APIKey.TenantID != nil {
		t.Fatalf("created key scope = %q tenant=%v, want platform with nil tenant", created.APIKey.ScopeType, created.APIKey.TenantID)
	}
	if created.APIKey.FullAccess {
		t.Fatal("platform API key must not be full-access")
	}
}

func TestTenantAPIKeyServiceRejectsFullAccessPlatformKey(t *testing.T) {
	svc := NewTenantAPIKeyService(newFakeTenantAPIKeyRepo())
	_, err := svc.CreateAPIKey(context.Background(), interfaces.TenantAPIKeyCreateRequest{
		ScopeType: types.APIKeyScopePlatform,
		Name:      "unsafe", FullAccess: true,
	})
	if err == nil {
		t.Fatal("full-access platform key should be rejected")
	}
}

func TestTenantAPIKeyServiceRevokeAPIKey(t *testing.T) {
	ctx := context.Background()
	repo := newFakeTenantAPIKeyRepo()
	svc := NewTenantAPIKeyService(repo)

	created, err := svc.CreateAPIKey(ctx, interfaces.TenantAPIKeyCreateRequest{
		TenantID: 42,
		Name:     "integration",
	})
	if err != nil {
		t.Fatalf("CreateAPIKey returned error: %v", err)
	}
	if err := svc.RevokeAPIKey(ctx, 42, created.APIKey.ID); err != nil {
		t.Fatalf("RevokeAPIKey returned error: %v", err)
	}
	if _, err := svc.AuthenticateAPIKey(ctx, created.Token); err == nil {
		t.Fatal("revoked key should not authenticate")
	}
}

func TestTenantAPIKeyServiceAuthenticateThrottlesLastUsedUpdates(t *testing.T) {
	ctx := context.Background()
	repo := newFakeTenantAPIKeyRepo()
	svc := NewTenantAPIKeyService(repo)

	created, err := svc.CreateAPIKey(ctx, interfaces.TenantAPIKeyCreateRequest{
		TenantID: 42,
		Name:     "integration",
	})
	if err != nil {
		t.Fatalf("CreateAPIKey returned error: %v", err)
	}

	for i := 0; i < 5; i++ {
		if _, err := svc.AuthenticateAPIKey(ctx, created.Token); err != nil {
			t.Fatalf("AuthenticateAPIKey #%d returned error: %v", i+1, err)
		}
	}

	deadline := time.Now().Add(500 * time.Millisecond)
	for repo.lastUsedUpdateCount == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if repo.lastUsedUpdateCount != 1 {
		t.Fatalf("last_used update count = %d, want 1 (throttled async write)", repo.lastUsedUpdateCount)
	}
}
