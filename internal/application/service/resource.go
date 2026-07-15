package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const defaultResourceGrantTTL = 2 * time.Hour

type resourceCatalog struct {
	repo interfaces.ResourceRepository
}

// NewResourceCatalog creates the stable resource-reference domain service.
func NewResourceCatalog(repo interfaces.ResourceRepository) interfaces.ResourceCatalog {
	return &resourceCatalog{repo: repo}
}

func randomResourceToken() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func resourceLocationHash(path string) string {
	sum := sha256.Sum256([]byte(path))
	return hex.EncodeToString(sum[:])
}

func (s *resourceCatalog) Register(
	ctx context.Context,
	tenantID uint64,
	physicalPath string,
	meta interfaces.ResourceRegistration,
) (string, error) {
	physicalPath = strings.TrimSpace(physicalPath)
	if tenantID == 0 || physicalPath == "" {
		return "", fmt.Errorf("resource registration requires tenant and physical path")
	}
	if _, ok := types.ParseResourcePath(physicalPath); ok {
		return physicalPath, nil
	}
	locationHash := resourceLocationHash(physicalPath)
	existing, err := s.repo.GetByTenantLocation(ctx, tenantID, locationHash)
	if err != nil {
		return "", err
	}
	if existing != nil {
		return types.BuildResourcePath(existing.Handle), nil
	}

	backendID, inner, scoped := types.ParseStorageBackendPath(physicalPath)
	providerPath := physicalPath
	if scoped {
		providerPath = inner
	}
	provider := types.ParseProviderScheme(providerPath)
	if provider == "" {
		return "", fmt.Errorf("resource physical path has unsupported provider scheme")
	}
	lifecycle := types.ResourceLifecyclePersistent
	if meta.Temporary {
		lifecycle = types.ResourceLifecycleTemporary
	}
	for attempt := 0; attempt < 4; attempt++ {
		handle, tokenErr := randomResourceToken()
		if tokenErr != nil {
			return "", tokenErr
		}
		resource := &types.StoredResource{
			Handle:           handle,
			TenantID:         tenantID,
			StorageBackendID: backendID,
			Provider:         provider,
			PhysicalPath:     physicalPath,
			LocationHash:     locationHash,
			Kind:             meta.Kind,
			MimeType:         meta.MimeType,
			OriginalName:     meta.OriginalName,
			Size:             meta.Size,
			ContentHash:      meta.ContentHash,
			Lifecycle:        lifecycle,
		}
		if err := s.repo.Create(ctx, resource); err == nil {
			return types.BuildResourcePath(handle), nil
		} else if !strings.Contains(strings.ToLower(err.Error()), "unique") {
			return "", err
		}
		existing, lookupErr := s.repo.GetByTenantLocation(ctx, tenantID, locationHash)
		if lookupErr == nil && existing != nil {
			return types.BuildResourcePath(existing.Handle), nil
		}
	}
	return "", fmt.Errorf("failed to allocate unique resource handle")
}

func (s *resourceCatalog) Resolve(ctx context.Context, reference string) (*types.StoredResource, error) {
	handle, ok := types.ParseResourcePath(reference)
	if !ok {
		return nil, fmt.Errorf("invalid resource reference")
	}
	resource, err := s.repo.GetByHandle(ctx, handle)
	if err != nil {
		return nil, err
	}
	if resource == nil {
		return nil, fmt.Errorf("resource not found")
	}
	return resource, nil
}

func (s *resourceCatalog) ResolvePath(ctx context.Context, value string) (string, *types.StoredResource, error) {
	if _, ok := types.ParseResourcePath(value); !ok {
		return value, nil, nil
	}
	resource, err := s.Resolve(ctx, value)
	if err != nil {
		return "", nil, err
	}
	return resource.PhysicalPath, resource, nil
}

func (s *resourceCatalog) Bind(ctx context.Context, reference, ownerType, ownerID, relation string) error {
	resource, err := s.Resolve(ctx, reference)
	if err != nil {
		return err
	}
	if strings.TrimSpace(ownerType) == "" || strings.TrimSpace(ownerID) == "" {
		return fmt.Errorf("resource binding requires owner type and id")
	}
	if relation == "" {
		relation = "attachment"
	}
	return s.repo.CreateBinding(ctx, &types.ResourceBinding{
		ResourceID: resource.ID,
		TenantID:   resource.TenantID,
		OwnerType:  ownerType,
		OwnerID:    ownerID,
		Relation:   relation,
	})
}

func (s *resourceCatalog) MarkDeleted(ctx context.Context, reference string) error {
	resource, err := s.Resolve(ctx, reference)
	if err != nil {
		return err
	}
	return s.repo.MarkDeleted(ctx, resource.ID)
}

func (s *resourceCatalog) CreateAccessGrant(ctx context.Context, reference string, ttl time.Duration) (string, error) {
	resource, err := s.Resolve(ctx, reference)
	if err != nil {
		return "", err
	}
	if ttl <= 0 {
		ttl = defaultResourceGrantTTL
	}
	// Opportunistic cleanup keeps high-volume IM rendering from accumulating
	// expired capability rows; failure is non-fatal to the current grant.
	_ = s.repo.DeleteExpiredGrants(ctx, time.Now())
	for attempt := 0; attempt < 4; attempt++ {
		token, tokenErr := randomResourceToken()
		if tokenErr != nil {
			return "", tokenErr
		}
		grant := &types.ResourceAccessGrant{
			TokenHash:   resourceLocationHash(token),
			ResourceID:  resource.ID,
			AccessScope: "read",
			ExpiresAt:   time.Now().Add(ttl),
		}
		if err := s.repo.CreateGrant(ctx, grant); err == nil {
			return token, nil
		} else if !strings.Contains(strings.ToLower(err.Error()), "unique") {
			return "", err
		}
	}
	return "", fmt.Errorf("failed to allocate unique resource access token")
}

func (s *resourceCatalog) ResolveAccessGrant(ctx context.Context, token string) (*types.StoredResource, error) {
	grant, err := s.repo.GetValidGrant(ctx, resourceLocationHash(strings.TrimSpace(token)), time.Now())
	if err != nil {
		return nil, err
	}
	if grant == nil {
		return nil, fmt.Errorf("resource access grant is invalid or expired")
	}
	resource, err := s.repo.GetByID(ctx, grant.ResourceID)
	if err != nil {
		return nil, err
	}
	if resource == nil {
		return nil, fmt.Errorf("resource not found")
	}
	return resource, nil
}
