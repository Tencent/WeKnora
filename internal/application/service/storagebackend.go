package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	filesvc "github.com/Tencent/WeKnora/internal/application/service/file"
	"github.com/Tencent/WeKnora/internal/database"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type StorageBackendService struct {
	repo            interfaces.StorageBackendRepository
	db              *gorm.DB
	resourceCatalog interfaces.ResourceCatalog
}

// NewStorageBackendService creates a storage backend service. The optional
// catalog keeps focused tests compatible while production uses the explicit
// constructor below.
func NewStorageBackendService(
	repo interfaces.StorageBackendRepository,
	db *gorm.DB,
	catalogs ...interfaces.ResourceCatalog,
) *StorageBackendService {
	service := &StorageBackendService{repo: repo, db: db}
	if len(catalogs) > 0 {
		service.resourceCatalog = catalogs[0]
	}
	return service
}

// NewStorageBackendServiceWithResources is the production DI constructor.
// The variadic constructor above remains convenient for focused tests that do
// not exercise resource registration.
func NewStorageBackendServiceWithResources(
	repo interfaces.StorageBackendRepository,
	db *gorm.DB,
	catalog interfaces.ResourceCatalog,
) *StorageBackendService {
	return NewStorageBackendService(repo, db, catalog)
}

func (s *StorageBackendService) Create(ctx context.Context, backend *types.StorageBackend) error {
	if err := backend.Validate(); err != nil {
		return err
	}
	if err := validateStorageBackendEndpoint(backend); err != nil {
		return err
	}
	if err := s.Test(ctx, backend); err != nil {
		return apperrors.NewBadRequestError("storage connection test failed").WithDetails(secutils.SanitizeStorageConnectivityError(err))
	}
	backend.CreatedAt, backend.UpdatedAt = time.Now(), time.Now()
	if err := s.repo.Create(ctx, backend); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return apperrors.NewConflictError("a storage backend with this name already exists")
		}
		return err
	}
	return nil
}

func (s *StorageBackendService) Update(ctx context.Context, incoming *types.StorageBackend) error {
	existing, err := s.repo.GetByID(ctx, incoming.TenantID, incoming.ID)
	if err != nil {
		return err
	}
	if existing == nil {
		return apperrors.NewNotFoundError("storage backend not found")
	}
	if existing.Source == types.StorageBackendSourceEnv {
		return apperrors.NewBadRequestError("environment storage backend is read-only")
	}
	incoming.Provider = existing.Provider
	incoming.Config = incoming.Config.MergeSecrets(existing.Config)
	if incoming.Config.LocationKey(existing.Provider) != existing.Config.LocationKey(existing.Provider) {
		return apperrors.NewBadRequestError("endpoint, region, bucket and path prefix are immutable; use storage migration instead")
	}
	if incoming.Status == "" {
		incoming.Status = existing.Status
	}
	if incoming.Status == types.StorageBackendStatusDisabled && existing.Status != types.StorageBackendStatusDisabled {
		var references int64
		if err := s.db.WithContext(ctx).Model(&types.Tenant{}).Where("id = ? AND default_storage_backend_id = ?", incoming.TenantID, incoming.ID).Count(&references).Error; err != nil {
			return err
		}
		if references == 0 {
			if err := s.db.WithContext(ctx).Model(&types.KnowledgeBase{}).Where("tenant_id = ? AND storage_backend_id = ?", incoming.TenantID, incoming.ID).Count(&references).Error; err != nil {
				return err
			}
		}
		if references == 0 {
			if err := s.db.WithContext(ctx).Model(&types.StoredResource{}).
				Where(
					"tenant_id = ? AND storage_backend_id = ? AND state = ?",
					incoming.TenantID,
					incoming.ID,
					types.ResourceStateActive,
				).
				Count(&references).Error; err != nil {
				return err
			}
		}
		if references > 0 {
			return apperrors.NewBadRequestError("a default or bound storage backend cannot be disabled")
		}
	}
	if err := incoming.Validate(); err != nil {
		return err
	}
	if err := validateStorageBackendEndpoint(incoming); err != nil {
		return err
	}
	if err := s.Test(ctx, incoming); err != nil {
		return apperrors.NewBadRequestError("storage connection test failed").WithDetails(secutils.SanitizeStorageConnectivityError(err))
	}
	incoming.UpdatedAt = time.Now()
	return s.repo.Update(ctx, incoming)
}

func (s *StorageBackendService) Delete(ctx context.Context, tenantID uint64, id string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var backend types.StorageBackend
		query := tx.Where("tenant_id = ? AND id = ?", tenantID, id)
		if database.SupportsRowLock(tx.Dialector.Name()) {
			query = query.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		if err := query.First(&backend).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return apperrors.NewNotFoundError("storage backend not found")
			}
			return err
		}
		if backend.Source == types.StorageBackendSourceEnv {
			return apperrors.NewBadRequestError("environment storage backend is read-only")
		}
		var defaultCount int64
		if err := tx.Model(&types.Tenant{}).Where("id = ? AND default_storage_backend_id = ?", tenantID, id).Count(&defaultCount).Error; err != nil {
			return err
		}
		if defaultCount > 0 {
			return apperrors.NewBadRequestError("default storage backend cannot be deleted")
		}
		var kbCount int64
		if err := tx.Model(&types.KnowledgeBase{}).Where("tenant_id = ? AND storage_backend_id = ?", tenantID, id).Count(&kbCount).Error; err != nil {
			return err
		}
		if kbCount > 0 {
			return apperrors.NewBadRequestError(fmt.Sprintf("storage backend still has %d knowledge base(s) bound to it", kbCount))
		}
		var resourceCount int64
		if err := tx.Model(&types.StoredResource{}).
			Where("tenant_id = ? AND storage_backend_id = ? AND state = ?", tenantID, id, types.ResourceStateActive).
			Count(&resourceCount).Error; err != nil {
			return err
		}
		if resourceCount > 0 {
			return apperrors.NewBadRequestError(fmt.Sprintf("storage backend still has %d active resource(s)", resourceCount))
		}
		if backend.LegacyAlias {
			return apperrors.NewBadRequestError("legacy storage backend cannot be deleted while old file paths may reference it")
		}
		return tx.Delete(&backend).Error
	})
}

func (s *StorageBackendService) SetDefault(ctx context.Context, tenantID uint64, id string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var backend types.StorageBackend
		query := tx.Where("tenant_id = ? AND id = ?", tenantID, id)
		if database.SupportsRowLock(tx.Dialector.Name()) {
			query = query.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		if err := query.First(&backend).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return apperrors.NewNotFoundError("storage backend not found")
			}
			return err
		}
		if backend.Status != types.StorageBackendStatusActive {
			return apperrors.NewBadRequestError("only an active storage backend can be the default")
		}
		return tx.Model(&types.Tenant{}).Where("id = ?", tenantID).Update("default_storage_backend_id", id).Error
	})
}

func (s *StorageBackendService) Test(ctx context.Context, backend *types.StorageBackend) error {
	if err := backend.Validate(); err != nil {
		return err
	}
	if err := validateStorageBackendEndpoint(backend); err != nil {
		return err
	}
	if backend.Provider == "local" {
		baseDir := strings.TrimSpace(os.Getenv("LOCAL_STORAGE_BASE_DIR"))
		if baseDir == "" {
			baseDir = "/data/files"
		}
		candidate := filepath.Join(baseDir, strings.Trim(strings.TrimSpace(backend.Config.PathPrefix), "/\\"))
		safeDir, err := secutils.SafePathUnderBase(baseDir, candidate)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(safeDir, 0o755); err != nil {
			return fmt.Errorf("create local storage directory: %w", err)
		}
	}
	c := backend.Config
	switch backend.Provider {
	case "local":
		fileService, _, err := filesvc.NewFileServiceFromStorageConfig("local", backend.ToStorageEngineConfig(), "")
		if err != nil {
			return err
		}
		return fileService.CheckConnectivity(ctx)
	case "minio":
		if c.Mode == "docker" {
			c.Endpoint = os.Getenv("MINIO_ENDPOINT")
			c.AccessKeyID = os.Getenv("MINIO_ACCESS_KEY_ID")
			c.SecretAccessKey = os.Getenv("MINIO_SECRET_ACCESS_KEY")
			if c.BucketName == "" {
				c.BucketName = os.Getenv("MINIO_BUCKET_NAME")
			}
		}
		return filesvc.CheckMinioConnectivity(ctx, c.Endpoint, c.AccessKeyID, c.SecretAccessKey, c.BucketName, c.UseSSL)
	case "cos":
		return filesvc.CheckCosConnectivity(ctx, c.BucketName, c.Region, c.AccessKeyID, c.SecretAccessKey)
	case "tos":
		return filesvc.CheckTosConnectivity(ctx, c.Endpoint, c.Region, c.AccessKeyID, c.SecretAccessKey, c.BucketName)
	case "s3":
		return filesvc.CheckS3ConnectivityWithOptions(ctx, c.Endpoint, c.AccessKeyID, c.SecretAccessKey, c.BucketName, c.Region, c.ForcePathStyle)
	case "oss":
		return filesvc.CheckOssConnectivity(ctx, c.Endpoint, c.Region, c.AccessKeyID, c.SecretAccessKey, c.BucketName)
	case "ks3":
		return filesvc.CheckKS3Connectivity(ctx, c.Endpoint, c.Region, c.AccessKeyID, c.SecretAccessKey, c.BucketName)
	case "obs":
		return filesvc.CheckObsConnectivity(ctx, c.Endpoint, c.Region, c.AccessKeyID, c.SecretAccessKey, c.BucketName)
	default:
		return fmt.Errorf("unsupported storage provider: %s", backend.Provider)
	}
}

func (s *StorageBackendService) ResolveBackend(ctx context.Context, tenant *types.Tenant, backendID, provider string) (*types.StorageBackend, error) {
	if tenant == nil {
		return nil, fmt.Errorf("workspace context missing")
	}
	backendID = strings.TrimSpace(backendID)
	provider = strings.ToLower(strings.TrimSpace(provider))
	if backendID == "" && provider != "" {
		backend, err := s.repo.FindLegacyAlias(ctx, tenant.ID, provider)
		if err != nil || backend != nil {
			return backend, err
		}
	}
	if backendID == "" && tenant.DefaultStorageBackendID != nil {
		backendID = strings.TrimSpace(*tenant.DefaultStorageBackendID)
	}
	if backendID != "" {
		backend, err := s.repo.GetByID(ctx, tenant.ID, backendID)
		if err != nil {
			return nil, err
		}
		if backend == nil {
			return nil, fmt.Errorf("storage backend not found")
		}
		if backend.Status != types.StorageBackendStatusActive {
			return nil, fmt.Errorf("storage backend is not active")
		}
		return backend, nil
	}
	return nil, nil
}

func (s *StorageBackendService) ResolveFileService(ctx context.Context, tenant *types.Tenant, backendID, provider, localBaseDir string) (interfaces.FileService, string, error) {
	backend, err := s.ResolveBackend(ctx, tenant, backendID, provider)
	if err != nil {
		return nil, "", err
	}
	if backend != nil {
		inner, provider, err := filesvc.NewFileServiceFromStorageConfig(backend.Provider, backend.ToStorageEngineConfig(), localBaseDir)
		if err != nil {
			return nil, provider, err
		}
		scoped := filesvc.NewBackendScopedFileService(backend.ID, inner)
		return filesvc.NewResourceCatalogFileService(scoped, s.resourceCatalog), provider, nil
	}
	if tenant == nil {
		return nil, "", fmt.Errorf("workspace context missing")
	}
	inner, resolvedProvider, err := filesvc.NewFileServiceFromStorageConfig(
		provider,
		tenant.StorageEngineConfig,
		localBaseDir,
	)
	if err != nil {
		return nil, resolvedProvider, err
	}
	return filesvc.NewResourceCatalogFileService(inner, s.resourceCatalog), resolvedProvider, nil
}

func validateStorageBackendEndpoint(backend *types.StorageBackend) error {
	if backend.Provider == "local" || (backend.Provider == "minio" && backend.Config.Mode == "docker") {
		return nil
	}
	endpoint := strings.TrimSpace(backend.Config.Endpoint)
	if backend.Provider == "cos" || endpoint == "" {
		return nil
	}
	if !strings.Contains(endpoint, "://") {
		scheme := "https://"
		if backend.Provider == "minio" && !backend.Config.UseSSL {
			scheme = "http://"
		}
		endpoint = scheme + endpoint
	}
	if err := secutils.ValidateURLForSSRF(endpoint); err != nil {
		return apperrors.NewBadRequestError("storage endpoint failed SSRF validation").WithDetails(err.Error())
	}
	return nil
}

var (
	_ interfaces.StorageBackendService  = (*StorageBackendService)(nil)
	_ interfaces.StorageBackendResolver = (*StorageBackendService)(nil)
)
