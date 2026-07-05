package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type tenantAPIKeyService struct {
	repo interfaces.TenantAPIKeyRepository
}

func NewTenantAPIKeyService(repo interfaces.TenantAPIKeyRepository) interfaces.TenantAPIKeyService {
	return &tenantAPIKeyService{repo: repo}
}

func (s *tenantAPIKeyService) CreateAPIKey(
	ctx context.Context, req interfaces.TenantAPIKeyCreateRequest,
) (*interfaces.TenantAPIKeyCreateResult, error) {
	if req.TenantID == 0 {
		return nil, errors.New("tenant_id is required")
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, errors.New("name is required")
	}
	token, err := generateTenantAPIKeyToken()
	if err != nil {
		return nil, err
	}
	key := &types.TenantAPIKey{
		TenantID:         req.TenantID,
		Name:             name,
		KeyHash:          hashTenantAPIKey(token),
		APIKey:           token,
		Scopes:           normalizeAPIKeyScopes(req.Scopes),
		KnowledgeBaseIDs: normalizeAPIKeyIDs(req.KnowledgeBaseIDs),
		ExpiresAt:        req.ExpiresAt,
	}
	if len(key.Scopes) == 0 {
		key.Scopes = types.StringArray{types.TenantAPIKeyScopeRead}
	}
	if err := s.repo.CreateAPIKey(ctx, key); err != nil {
		return nil, err
	}
	return &interfaces.TenantAPIKeyCreateResult{APIKey: key, Token: token}, nil
}

func (s *tenantAPIKeyService) AuthenticateAPIKey(ctx context.Context, token string) (*types.TenantAPIKey, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, apprepo.ErrTenantAPIKeyNotFound
	}
	key, err := s.repo.GetAPIKeyByHash(ctx, hashTenantAPIKey(token))
	if err != nil {
		return nil, err
	}
	if key.RevokedAt != nil {
		return nil, apprepo.ErrTenantAPIKeyNotFound
	}
	if key.ExpiresAt != nil && time.Now().After(*key.ExpiresAt) {
		return nil, apprepo.ErrTenantAPIKeyNotFound
	}
	_ = s.repo.UpdateAPIKeyLastUsed(ctx, key.ID, time.Now())
	return key, nil
}

func (s *tenantAPIKeyService) AuthenticateTenantAPIKey(
	ctx context.Context, tenantID uint64, token string,
) (*types.TenantAPIKey, error) {
	token = strings.TrimSpace(token)
	if tenantID == 0 || token == "" {
		return nil, apprepo.ErrTenantAPIKeyNotFound
	}
	if key, err := s.AuthenticateAPIKey(ctx, token); err == nil && key != nil {
		if key.TenantID != tenantID {
			return nil, apprepo.ErrTenantAPIKeyNotFound
		}
		return key, nil
	} else if err != nil && !errors.Is(err, apprepo.ErrTenantAPIKeyNotFound) {
		return nil, err
	}
	keys, err := s.repo.ListAPIKeys(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	for _, key := range keys {
		if key == nil || subtle.ConstantTimeCompare([]byte(key.APIKey), []byte(token)) != 1 {
			continue
		}
		if key.ExpiresAt != nil && time.Now().After(*key.ExpiresAt) {
			return nil, apprepo.ErrTenantAPIKeyNotFound
		}
		if err := s.ensureAPIKeyHash(ctx, key); err != nil {
			return nil, err
		}
		_ = s.repo.UpdateAPIKeyLastUsed(ctx, key.ID, time.Now())
		return key, nil
	}
	return nil, apprepo.ErrTenantAPIKeyNotFound
}

func (s *tenantAPIKeyService) ListAPIKeys(ctx context.Context, tenantID uint64) ([]*types.TenantAPIKey, error) {
	keys, err := s.repo.ListAPIKeys(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	for _, key := range keys {
		if err := s.ensureAPIKeyHash(ctx, key); err != nil {
			return nil, err
		}
	}
	return keys, nil
}

func (s *tenantAPIKeyService) RevokeAPIKey(ctx context.Context, tenantID uint64, id uint64) error {
	return s.repo.RevokeAPIKey(ctx, tenantID, id)
}

func (s *tenantAPIKeyService) RevokeAllAPIKeys(ctx context.Context, tenantID uint64) error {
	if tenantID == 0 {
		return nil
	}
	return s.repo.RevokeAllAPIKeys(ctx, tenantID)
}

func generateTenantAPIKeyToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "sk-" + base64.RawURLEncoding.EncodeToString(b[:]), nil
}

func hashTenantAPIKey(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (s *tenantAPIKeyService) ensureAPIKeyHash(ctx context.Context, key *types.TenantAPIKey) error {
	if key == nil || strings.TrimSpace(key.APIKey) == "" {
		return nil
	}
	hash := hashTenantAPIKey(key.APIKey)
	if key.KeyHash == hash {
		return nil
	}
	if err := s.repo.UpdateAPIKeyHash(ctx, key.ID, hash); err != nil {
		return err
	}
	key.KeyHash = hash
	return nil
}

func normalizeAPIKeyScopes(in []string) types.StringArray {
	out := types.StringArray{}
	seen := map[string]struct{}{}
	for _, scope := range in {
		scope = strings.ToLower(strings.TrimSpace(scope))
		switch scope {
		case types.TenantAPIKeyScopeRead, types.TenantAPIKeyScopeWrite, types.TenantAPIKeyScopeAdmin:
		default:
			continue
		}
		if _, ok := seen[scope]; ok {
			continue
		}
		seen[scope] = struct{}{}
		out = append(out, scope)
	}
	return out
}

func normalizeAPIKeyIDs(in []string) types.StringArray {
	out := types.StringArray{}
	seen := map[string]struct{}{}
	for _, id := range in {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
