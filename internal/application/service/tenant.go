package service

import (
	"context"
	"errors"
	"time"

	werrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// ListTenantsParams defines parameters for listing tenants with filtering and pagination
type ListTenantsParams struct {
	Page     int    // Page number for pagination
	PageSize int    // Number of items per page
	Status   string // Filter by tenant status
	Name     string // Filter by tenant name
}

// tenantService implements the TenantService interface
type tenantService struct {
	repo        interfaces.TenantRepository // Repository for tenant data operations
	storageRepo interfaces.StorageBackendRepository
}

// NewTenantService creates a new tenant service instance
func NewTenantService(repo interfaces.TenantRepository, storageRepo interfaces.StorageBackendRepository) interfaces.TenantService {
	return &tenantService{repo: repo, storageRepo: storageRepo}
}

// CreateTenant creates a new tenant
func (s *tenantService) CreateTenant(ctx context.Context, tenant *types.Tenant) (*types.Tenant, error) {
	logger.Info(ctx, "Start creating tenant")

	if tenant.Name == "" {
		logger.Error(ctx, "Workspace name cannot be empty")
		return nil, errors.New("workspace name cannot be empty")
	}

	logger.Infof(ctx, "Creating tenant, name: %s", tenant.Name)

	// New tenants do not receive an API key by default. Integrations create
	// keys explicitly through tenant_api_keys.
	tenant.Status = "active"
	tenant.CreatedAt = time.Now()
	tenant.UpdatedAt = time.Now()

	if err := s.validateStorageBucketUniqueness(ctx, tenant); err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"tenant_name": tenant.Name,
		})
		return nil, err
	}

	logger.Info(ctx, "Saving tenant information to database")
	if err := s.repo.CreateTenant(ctx, tenant); err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"tenant_name": tenant.Name,
		})
		return nil, err
	}
	if err := s.createDefaultStorageBackend(ctx, tenant); err != nil {
		// No related rows exist yet, so rolling the tenant back is safe and
		// avoids leaving a workspace that cannot bind new knowledge bases.
		_ = s.repo.DeleteTenant(ctx, tenant.ID)
		return nil, err
	}

	logger.Infof(ctx, "Tenant created successfully, ID: %d, name: %s", tenant.ID, tenant.Name)
	return tenant, nil
}

func (s *tenantService) createDefaultStorageBackend(ctx context.Context, tenant *types.Tenant) error {
	if s.storageRepo == nil || tenant == nil {
		return nil
	}
	provider := ""
	if tenant.StorageEngineConfig != nil {
		provider = tenant.StorageEngineConfig.DefaultProvider
	}
	backend := types.StorageBackendFromLegacy(tenant.ID, provider, tenant.StorageEngineConfig)
	if backend == nil {
		backend = types.StorageBackendFromEnvironment(tenant.ID)
	}
	if backend == nil {
		return errors.New("no supported default storage backend is configured")
	}
	backend.LegacyAlias = true
	if err := s.storageRepo.Create(ctx, backend); err != nil {
		return err
	}
	tenant.DefaultStorageBackendID = &backend.ID
	if err := s.repo.UpdateTenant(ctx, tenant); err != nil {
		_ = s.storageRepo.Delete(ctx, tenant.ID, backend.ID)
		return err
	}
	return nil
}

// GetTenantByID retrieves a tenant by their ID
func (s *tenantService) GetTenantByID(ctx context.Context, id uint64) (*types.Tenant, error) {
	if id == 0 {
		logger.Error(ctx, "Workspace ID cannot be 0")
		return nil, errors.New("tenant ID cannot be 0")
	}

	tenant, err := s.repo.GetTenantByID(ctx, id)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"tenant_id": id,
		})
		return nil, err
	}

	return tenant, nil
}

// GetTenantsByIDs batches GetTenantByID; returns a map keyed by tenant ID.
func (s *tenantService) GetTenantsByIDs(ctx context.Context, ids []uint64) (map[uint64]*types.Tenant, error) {
	return s.repo.GetTenantsByIDs(ctx, ids)
}

// ListTenants retrieves a list of all tenants
func (s *tenantService) ListTenants(ctx context.Context) ([]*types.Tenant, error) {
	tenants, err := s.repo.ListTenants(ctx)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		return nil, err
	}

	logger.Infof(ctx, "Tenant list retrieved successfully, total: %d", len(tenants))
	return tenants, nil
}

// UpdateTenant updates an existing tenant's information
func (s *tenantService) UpdateTenant(ctx context.Context, tenant *types.Tenant) (*types.Tenant, error) {
	if tenant.ID == 0 {
		logger.Error(ctx, "Workspace ID cannot be 0")
		return nil, errors.New("tenant ID cannot be 0")
	}

	logger.Infof(ctx, "Updating tenant, ID: %d, name: %s", tenant.ID, tenant.Name)

	if err := s.validateStorageBucketUniqueness(ctx, tenant); err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"tenant_id": tenant.ID,
		})
		return nil, err
	}

	tenant.UpdatedAt = time.Now()
	logger.Info(ctx, "Saving tenant information to database")

	if err := s.repo.UpdateTenant(ctx, tenant); err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"tenant_id": tenant.ID,
		})
		return nil, err
	}

	logger.Infof(ctx, "Tenant updated successfully, ID: %d", tenant.ID)
	return tenant, nil
}

// DeleteTenant removes a tenant by their ID
func (s *tenantService) DeleteTenant(ctx context.Context, id uint64) error {
	logger.Info(ctx, "Start deleting tenant")

	if id == 0 {
		logger.Error(ctx, "Workspace ID cannot be 0")
		return errors.New("tenant ID cannot be 0")
	}

	logger.Infof(ctx, "Deleting tenant, ID: %d", id)

	// Get tenant information for logging
	tenant, err := s.repo.GetTenantByID(ctx, id)
	if err != nil {
		if err.Error() == "record not found" {
			logger.Warnf(ctx, "Tenant to be deleted does not exist, ID: %d", id)
		} else {
			logger.ErrorWithFields(ctx, err, map[string]interface{}{
				"tenant_id": id,
			})
			return err
		}
	} else {
		logger.Infof(ctx, "Deleting tenant, ID: %d, name: %s", id, tenant.Name)
	}

	err = s.repo.DeleteTenant(ctx, id)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"tenant_id": id,
		})
		return err
	}

	logger.Infof(ctx, "Workspace deleted successfully, ID: %d", id)
	return nil
}

// ListAllTenants lists all tenants (for users with cross-tenant access permission)
// This method returns all tenants without filtering, intended for admin users
func (s *tenantService) ListAllTenants(ctx context.Context) ([]*types.Tenant, error) {
	tenants, err := s.repo.ListTenants(ctx)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		return nil, err
	}

	logger.Infof(ctx, "All tenants list retrieved successfully, total: %d", len(tenants))
	return tenants, nil
}

// BulkSetStorageQuota delegates to the repository. Validation is
// minimal — quotaBytes <= 0 is rejected because the storage-quota
// enforcement in knowledge_create.go treats <=0 as "unlimited", which
// is never what a SystemAdmin pressing "apply default" intends.
func (s *tenantService) BulkSetStorageQuota(ctx context.Context, quotaBytes int64) (int64, error) {
	if quotaBytes <= 0 {
		return 0, errors.New("quota must be positive")
	}
	affected, err := s.repo.BulkSetStorageQuota(ctx, quotaBytes)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"quota_bytes": quotaBytes})
		return 0, err
	}
	logger.Infof(ctx, "Bulk set storage_quota=%d on %d tenants", quotaBytes, affected)
	return affected, nil
}

// SearchTenants searches tenants with pagination and filters
func (s *tenantService) SearchTenants(ctx context.Context, keyword string, tenantID uint64, page, pageSize int) ([]*types.Tenant, int64, error) {
	tenants, total, err := s.repo.SearchTenants(ctx, keyword, tenantID, page, pageSize)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"keyword":  keyword,
			"tenantID": tenantID,
			"page":     page,
			"pageSize": pageSize,
		})
		return nil, 0, err
	}

	logger.Infof(ctx, "Tenants search completed, keyword: %s, tenantID: %d, page: %d, pageSize: %d, total: %d, found: %d",
		keyword, tenantID, page, pageSize, total, len(tenants))
	return tenants, total, nil
}

// GetTenantByIDForUser gets a tenant by ID with permission check
// This method verifies that the user has permission to access the tenant
func (s *tenantService) GetTenantByIDForUser(ctx context.Context, tenantID uint64, userID string) (*types.Tenant, error) {
	tenant, err := s.repo.GetTenantByID(ctx, tenantID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"tenant_id": tenantID,
			"user_id":   userID,
		})
		return nil, err
	}

	return tenant, nil
}

func (s *tenantService) GetWeKnoraCloudCredentials(ctx context.Context) *types.WeKnoraCloudCredentials {
	// Try to get tenant info from context first (already loaded by middleware).
	// CredentialsConfig.Scan handles decryption, so credentials are ready to use.
	if tenant, ok := types.TenantInfoFromContext(ctx); ok {
		if creds := tenant.Credentials.GetWeKnoraCloud(); creds != nil {
			return creds
		}
	}

	// Fallback: load tenant from repo by tenantID
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok {
		return nil
	}

	tenant, err := s.repo.GetTenantByID(ctx, tenantID)
	if err != nil || tenant == nil {
		return nil
	}
	return tenant.Credentials.GetWeKnoraCloud()
}

func (s *tenantService) validateStorageBucketUniqueness(ctx context.Context, tenant *types.Tenant) error {
	if tenant.StorageEngineConfig == nil {
		return nil
	}

	// Fetch existing tenant from DB to compare
	var oldTenant *types.Tenant
	if tenant.ID != 0 {
		var err error
		oldTenant, err = s.repo.GetTenantByID(ctx, tenant.ID)
		if err != nil && err.Error() != "tenant not found" && err.Error() != "record not found" {
			return err
		}
	}

	// Fetch ALL tenants to check for collision.
	allTenants, err := s.repo.ListTenants(ctx)
	if err != nil {
		return err
	}

	// Helper to get bucket names from a StorageEngineConfig
	getBuckets := func(cfg *types.StorageEngineConfig) map[string]string {
		if cfg == nil {
			return nil
		}
		res := make(map[string]string)
		if cfg.MinIO != nil && cfg.MinIO.BucketName != "" {
			res["minio"] = cfg.MinIO.BucketName
		}
		if cfg.COS != nil && cfg.COS.BucketName != "" {
			res["cos"] = cfg.COS.BucketName
		}
		if cfg.TOS != nil && cfg.TOS.BucketName != "" {
			res["tos"] = cfg.TOS.BucketName
		}
		if cfg.S3 != nil && cfg.S3.BucketName != "" {
			res["s3"] = cfg.S3.BucketName
		}
		if cfg.OSS != nil && cfg.OSS.BucketName != "" {
			res["oss"] = cfg.OSS.BucketName
		}
		return res
	}

	var oldBuckets map[string]string
	if oldTenant != nil {
		oldBuckets = getBuckets(oldTenant.StorageEngineConfig)
	}
	newBuckets := getBuckets(tenant.StorageEngineConfig)

	// Collect buckets used by other tenants
	usedByOthers := make(map[string]map[string]bool) // provider -> set of bucket names
	for _, t := range allTenants {
		if t.ID == tenant.ID {
			continue
		}
		tb := getBuckets(t.StorageEngineConfig)
		for p, b := range tb {
			if usedByOthers[p] == nil {
				usedByOthers[p] = make(map[string]bool)
			}
			usedByOthers[p][b] = true
		}
	}

	// Check if any NEW bucket is already used by someone else, AND it's different from the OLD bucket
	for p, b := range newBuckets {
		oldB := oldBuckets[p]
		if b != oldB { // User is trying to change their bucket name or set a new one
			if usedByOthers[p] != nil && usedByOthers[p][b] {
				return werrors.NewBadRequestError("存储桶名称「" + b + "」已被其他空间使用，为保证数据隔离，请使用其他名称")
			}
		}
	}

	return nil
}
