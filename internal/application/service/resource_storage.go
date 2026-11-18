package service

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
)

// NewResourceCatalogWithStorage snapshots the same environment as initRawFileService.
// A later workspace-default change must not change the destination of global writes.
func NewResourceCatalogWithStorage(
	repo interfaces.ResourceRepository,
	storage interfaces.StorageBackendRepository,
) interfaces.ResourceCatalog {
	global := types.StorageBackendFromEnvironment(0)
	if global != nil {
		global.LegacyAlias = false
		switch global.Provider {
		case "local", "minio":
			// The process-wide drivers do not use the workspace path-prefix settings.
			global.Config.PathPrefix = ""
		case "s3", "oss", "obs":
			if global.Config.PathPrefix == "" {
				global.Config.PathPrefix = "weknora/"
			}
		}
		if global.Provider == "s3" {
			global.Config.ForcePathStyle = false // initRawFileService uses NewS3FileService.
		}
	}
	return &resourceCatalog{repo: repo, storage: storage, globalStorage: global}
}

func (s *resourceCatalog) globalBackendSnapshot(tenantID uint64) *types.StorageBackend {
	backend := *s.globalStorage
	backend.TenantID = tenantID
	// Credentials can rotate, but changing location creates a separate instance.
	// This is deliberately not a legacy alias: startup refreshes those in place.
	identity := fmt.Sprintf("global-storage:%d:%s:%t:%t:%s:%s", tenantID,
		backend.Config.LocationKey(backend.Provider), backend.Config.UseSSL, backend.Config.ForcePathStyle,
		backend.Config.TempBucketName, backend.Config.TempRegion)
	backend.ID = uuid.NewSHA1(uuid.NameSpaceOID, []byte(identity)).String()
	backend.Name = fmt.Sprintf("System %s files (%s)", strings.ToUpper(backend.Provider), backend.ID[:8])
	return &backend
}

func (s *resourceCatalog) globalBackend(ctx context.Context, tenantID uint64) (*types.StorageBackend, error) {
	if s.storage == nil || s.globalStorage == nil {
		return nil, fmt.Errorf("global storage instance is unavailable")
	}
	backend := s.globalBackendSnapshot(tenantID)
	existing, err := s.storage.GetByID(ctx, tenantID, backend.ID)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		if err := s.storage.Create(ctx, backend); err != nil {
			// Concurrent first writes converge on the same instance.
			existing, lookupErr := s.storage.GetByID(ctx, tenantID, backend.ID)
			if lookupErr != nil || existing == nil || existing.Status != types.StorageBackendStatusActive {
				return nil, err
			}
			return existing, nil
		}
		return backend, nil
	}
	if existing.Status != types.StorageBackendStatusActive {
		return nil, fmt.Errorf("global storage instance is disabled")
	}
	if existing.Config != backend.Config {
		existing.Config = backend.Config
		if err := s.storage.Update(ctx, existing); err != nil {
			return nil, err
		}
	}
	return existing, nil
}

func (s *resourceCatalog) RegisterGlobal(
	ctx context.Context, tenantID uint64, physicalPath string, meta interfaces.ResourceRegistration,
) (string, error) {
	backend, err := s.globalBackend(ctx, tenantID)
	if err != nil {
		return "", err
	}
	if !resourceMatchesBackend(physicalPath, backend) {
		return "", fmt.Errorf("resource does not match the global storage instance")
	}
	meta.StorageBackendID = backend.ID
	return s.Register(ctx, tenantID, physicalPath, meta)
}

// ResolveStorageBinding backfills old unscoped resources conservatively. A
// provider alone never justifies using the workspace default or a legacy alias.
func (s *resourceCatalog) ResolveStorageBinding(
	ctx context.Context, reference string,
) (*types.StoredResource, error) {
	resource, err := s.Resolve(ctx, reference)
	if err != nil {
		return resource, err
	}
	if resource.StorageBackendID != "" {
		// Refresh rotated credentials only for the exact current global location.
		// Missing/disabled explicit instances are never recreated during reads.
		if s.globalStorage != nil && s.globalBackendSnapshot(resource.TenantID).ID == resource.StorageBackendID {
			backend, err := s.storage.GetByID(ctx, resource.TenantID, resource.StorageBackendID)
			if err != nil || backend == nil || backend.Status != types.StorageBackendStatusActive {
				return nil, fmt.Errorf("bound global storage instance is unavailable")
			}
			if _, err := s.globalBackend(ctx, resource.TenantID); err != nil {
				return nil, err
			}
		}
		return resource, nil
	}
	if backendID, _, ok := types.ParseStorageBackendPath(resource.PhysicalPath); ok {
		return s.repo.BindStorageBackend(ctx, resource.ID, backendID)
	}
	if s.storage == nil {
		return nil, fmt.Errorf("resource has no storage instance")
	}
	var global *types.StorageBackend
	if s.globalStorage != nil && resourceMatchesBackend(resource.PhysicalPath, s.globalStorage) {
		global, err = s.globalBackend(ctx, resource.TenantID)
		if err != nil {
			return nil, err
		}
	}
	backends, err := s.storage.List(ctx, resource.TenantID)
	if err != nil {
		return nil, err
	}
	var selected *types.StorageBackend
	for _, backend := range backends {
		if !resourceMatchesBackend(resource.PhysicalPath, backend) {
			continue
		}
		// Include disabled instances in ambiguity checks; they are not a reason
		// to silently select a different endpoint for an old path.
		if selected != nil && resourceBackendLocation(selected) != resourceBackendLocation(backend) {
			return nil, fmt.Errorf("resource storage instance is ambiguous")
		}
		if selected == nil || backend.ID < selected.ID {
			selected = backend
		}
	}
	if selected == nil || selected.Status != types.StorageBackendStatusActive {
		return nil, fmt.Errorf("resource storage instance is unavailable")
	}
	if global != nil {
		// Prefer the immutable-location snapshot over startup's mutable env alias.
		selected = global
	}
	return s.repo.BindStorageBackend(ctx, resource.ID, selected.ID)
}

func resourceMatchesBackend(physical string, backend *types.StorageBackend) bool {
	u, err := url.Parse(physical)
	if err != nil || u.Scheme != backend.Provider || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	if backend.Provider == "local" {
		// local:// paths omit the root/prefix, so different local roots are ambiguous.
		return true
	}
	if u.Host != backend.Config.BucketName &&
		(u.Host == "" || u.Host != backend.Config.TempBucketName) {
		return false
	}
	if backend.Provider == "cos" {
		region, _, _ := strings.Cut(strings.TrimPrefix(u.Path, "/"), "/")
		return region == backend.Config.Region ||
			(u.Host == backend.Config.TempBucketName && region == backend.Config.TempRegion)
	}
	return true
}

func resourceBackendLocation(backend *types.StorageBackend) string {
	c := backend.Config
	if backend.Provider != "local" {
		// Object paths already include the key prefix; it does not affect reads.
		c.PathPrefix = ""
	}
	return fmt.Sprintf("%s|%t|%t|%s|%s", c.LocationKey(backend.Provider),
		c.UseSSL, c.ForcePathStyle, c.TempBucketName, c.TempRegion)
}
