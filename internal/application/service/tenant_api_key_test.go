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
	byHash map[string]*types.TenantAPIKey
	nextID uint64
}

func TestTenantAPIKeyServiceCreateAPIKeyUsesSKPrefix(t *testing.T) {
	ctx := context.Background()
	repo := newFakeTenantAPIKeyRepo()
	svc := NewTenantAPIKeyService(repo)

	result, err := svc.CreateAPIKey(ctx, interfaces.TenantAPIKeyCreateRequest{
		TenantID: 42,
		Name:     "integration",
		Scopes:   []string{types.TenantAPIKeyScopeRead},
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
		if key.TenantID == tenantID && key.RevokedAt == nil {
			cp := *key
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (r *fakeTenantAPIKeyRepo) RevokeAPIKey(_ context.Context, tenantID uint64, id uint64) error {
	now := time.Now()
	for _, key := range r.byHash {
		if key.ID == id && key.TenantID == tenantID && key.RevokedAt == nil {
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

func (r *fakeTenantAPIKeyRepo) UpdateAPIKeyLastUsed(_ context.Context, id uint64, at time.Time) error {
	for _, key := range r.byHash {
		if key.ID == id && key.RevokedAt == nil {
			key.LastUsedAt = &at
		}
	}
	return nil
}

func TestTenantAPIKeyServiceEnsureTenantAPIKeyBackfillsMetadata(t *testing.T) {
	ctx := context.Background()
	repo := newFakeTenantAPIKeyRepo()
	svc := NewTenantAPIKeyService(repo)
	token := "sk-42-migrated-secret-value"

	if err := svc.EnsureTenantAPIKey(ctx, 42, token); err != nil {
		t.Fatalf("EnsureTenantAPIKey returned error: %v", err)
	}

	keys, err := svc.ListAPIKeys(ctx, 42)
	if err != nil {
		t.Fatalf("ListAPIKeys returned error: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("tenant keys count = %d, want 1", len(keys))
	}
	key := keys[0]
	if key.APIKey != token {
		t.Fatalf("tenant key api_key = %q, want %q", key.APIKey, token)
	}
	if !(types.TenantAPIKeyScope{Scopes: key.Scopes}).HasScope(types.TenantAPIKeyScopeAdmin) {
		t.Fatalf("tenant key should include admin scope")
	}
}

func TestTenantAPIKeyServiceAuthenticateTenantAPIKeyRepairsMigratedHash(t *testing.T) {
	ctx := context.Background()
	repo := newFakeTenantAPIKeyRepo()
	svc := NewTenantAPIKeyService(repo)
	token := "sk-42-migrated-secret-value"
	key := &types.TenantAPIKey{
		TenantID: 42,
		Name:     "Tenant API key",
		KeyHash:  "migrated-tenant-42",
		APIKey:   token,
		Scopes:   types.StringArray{types.TenantAPIKeyScopeAdmin},
	}
	if err := repo.CreateAPIKey(ctx, key); err != nil {
		t.Fatalf("CreateAPIKey returned error: %v", err)
	}
	got, err := svc.AuthenticateTenantAPIKey(ctx, 42, token)
	if err != nil {
		t.Fatalf("AuthenticateTenantAPIKey returned error: %v", err)
	}
	if got.KeyHash != hashTenantAPIKey(token) {
		t.Fatalf("repaired hash = %q, want %q", got.KeyHash, hashTenantAPIKey(token))
	}
	if _, err := svc.AuthenticateAPIKey(ctx, token); err != nil {
		t.Fatalf("AuthenticateAPIKey after repair returned error: %v", err)
	}
}
