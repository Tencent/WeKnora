package service

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	files "github.com/Tencent/WeKnora/internal/application/service/file"
	"github.com/Tencent/WeKnora/internal/storageurl"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type kbOriginalCleanupRepository interface {
	FindOriginalForCleanup(context.Context, uint64, string, string) (*types.StoredResource, bool, error)
	MarkOriginalDeleted(context.Context, uint64, string) error
}

func (s *knowledgeBaseService) cleanupOriginals(ctx context.Context, tenantID uint64, kbID string, documents []*types.Knowledge) error {
	var tenant *types.Tenant
	for _, document := range documents {
		if strings.TrimSpace(document.FilePath) == "" {
			continue
		}
		if tenant == nil {
			var err error
			tenant, err = s.tenantRepo.GetTenantByID(ctx, tenantID)
			if err != nil {
				return fmt.Errorf("load original storage owner: %w", err)
			}
			if tenant == nil || tenant.ID != tenantID {
				return fmt.Errorf("original storage owner unavailable")
			}
		}
		if err := s.cleanupOriginal(ctx, tenant, kbID, document); err != nil {
			return fmt.Errorf("clean knowledge base %s original %s: %w", kbID, document.ID, err)
		}
	}
	return nil
}

func (s *knowledgeBaseService) cleanupOriginal(
	ctx context.Context, tenant *types.Tenant, kbID string, knowledge *types.Knowledge,
) error {
	ref := strings.TrimSpace(knowledge.FilePath)
	if ref == "" {
		return nil
	}
	repo, ok := s.repo.(kbOriginalCleanupRepository)
	if !ok {
		return fmt.Errorf("original cleanup metadata unavailable")
	}
	resource, shared, err := repo.FindOriginalForCleanup(ctx, tenant.ID, kbID, ref)
	if err != nil {
		return err
	}
	if shared {
		// Preserve the original for the other owner. Release only this document.
		if resource != nil && resource.State == types.ResourceStateActive && s.resourceCatalog != nil {
			_, err = s.resourceCatalog.Release(ctx, ref, types.ResourceOwnerKnowledge, knowledge.ID)
		}
		return err
	}
	if resource == nil && strings.HasPrefix(ref, types.ResourceScheme) {
		return fmt.Errorf("original resource reference is invalid")
	}
	driver, path, err := s.resolveOriginalStorage(ctx, tenant, ref, resource)
	if err != nil {
		return err
	}
	if err := files.DeleteAndVerifyAbsent(ctx, driver, path); err != nil {
		return err
	}
	if resource != nil {
		return repo.MarkOriginalDeleted(ctx, tenant.ID, resource.ID)
	}
	return nil
}

func (s *knowledgeBaseService) resolveOriginalStorage(
	ctx context.Context, tenant *types.Tenant, ref string, resource *types.StoredResource,
) (interfaces.FileService, string, error) {
	path := ref
	var backendID, provider string
	if resource != nil {
		if resource.State == types.ResourceStateActive {
			if resolver, ok := s.resourceCatalog.(interfaces.ResourceStorageBindingResolver); ok {
				bound, err := resolver.ResolveStorageBinding(ctx, ref)
				if err != nil {
					return nil, "", err
				}
				if bound == nil || bound.ID != resource.ID || bound.TenantID != tenant.ID {
					return nil, "", fmt.Errorf("original resource binding changed")
				}
				resource = bound
			}
		}
		path, backendID, provider = resource.PhysicalPath, resource.StorageBackendID, resource.Provider
	}
	if path == "" {
		return nil, "", fmt.Errorf("original storage path unavailable")
	}
	if id, _, scoped := types.ParseStorageBackendPath(path); scoped {
		if backendID != "" && backendID != id {
			return nil, "", fmt.Errorf("original storage backend mismatch")
		}
		backendID = id
	}
	if resource != nil && backendID == "" {
		return nil, "", fmt.Errorf("original resource has no known storage backend")
	}
	if provider == "" {
		provider = types.InferStorageFromFilePath(path)
	}
	if backendID == "" && provider == "" {
		if !filepath.IsAbs(path) {
			return nil, "", fmt.Errorf("original storage location is ambiguous")
		}
		provider = "local" // The local driver validates legacy absolute paths against its root.
	}
	if s.storageResolver == nil {
		return nil, "", fmt.Errorf("original storage resolver unavailable")
	}
	driver, resolvedProvider, err := s.storageResolver.ResolveFileService(ctx, tenant, backendID, provider, storageurl.LocalStorageBaseDir())
	if err != nil {
		return nil, "", err
	}
	if driver == nil || (provider != "" && resolvedProvider != provider) {
		return nil, "", fmt.Errorf("original storage backend unavailable or mismatched")
	}
	return driver, path, nil
}
