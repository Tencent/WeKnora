package storageurl

import (
	"context"
	"os"
	"strings"

	filesvc "github.com/Tencent/WeKnora/internal/application/service/file"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// LocalStorageBaseDir is the on-disk root used when a reference resolves to the
// local provider.
func LocalStorageBaseDir() string {
	baseDir := strings.TrimSpace(os.Getenv("LOCAL_STORAGE_BASE_DIR"))
	if baseDir == "" {
		baseDir = "/data/files"
	}
	return baseDir
}

type fileServiceKey struct {
	tenantID  uint64
	backendID string
	provider  string
}

// FileServiceResolver resolves and caches one FileService per owner and storage instance.
// The cache is scoped to a single request or outbound message so a long answer
// with many images does not re-create an SDK client per reference.
//
// Not safe for concurrent use; Rewriter drives it from one goroutine at a time.
type FileServiceResolver struct {
	tenant          *types.Tenant
	defaultSvc      interfaces.FileService
	storageResolver interfaces.StorageBackendResolver
	resourceCatalog interfaces.ResourceCatalog
	tenantService   interfaces.TenantService
	ctx             context.Context
	cache           map[fileServiceKey]interfaces.FileService
	owners          map[uint64]*types.Tenant
	authorize       func(context.Context, string) error
}

// NewFileServiceResolver builds a resolver for tenant. defaultSvc is the
// process-wide fallback for legacy references without an explicit backend.
// WithResources enables owner/instance routing for resource:// handles.
func NewFileServiceResolver(
	tenant *types.Tenant,
	defaultSvc interfaces.FileService,
	storageResolvers ...interfaces.StorageBackendResolver,
) *FileServiceResolver {
	resolver := &FileServiceResolver{
		tenant:     tenant,
		defaultSvc: defaultSvc,
		ctx:        context.Background(),
		cache:      make(map[fileServiceKey]interfaces.FileService),
		owners:     make(map[uint64]*types.Tenant),
	}
	if len(storageResolvers) > 0 {
		resolver.storageResolver = storageResolvers[0]
	}
	return resolver
}

// WithResources enables routing resource handles through their recorded owner
// and storage instance. Callers must already be authorized to expose the
// references; this only selects storage and does not grant resource access.
func (r *FileServiceResolver) WithResources(
	catalog interfaces.ResourceCatalog,
	tenants interfaces.TenantService,
) *FileServiceResolver {
	r.resourceCatalog = catalog
	r.tenantService = tenants
	return r
}

// WithAuthorization gates every reference before any storage credentials are
// selected. Model output and editable Markdown are not authorization evidence.
func (r *FileServiceResolver) WithAuthorization(authorize func(context.Context, string) error) *FileServiceResolver {
	r.authorize = authorize
	return r
}

// WithContext sets the context used for backend lookups and their logs.
func (r *FileServiceResolver) WithContext(ctx context.Context) *FileServiceResolver {
	if ctx != nil {
		r.ctx = ctx
	}
	return r
}

// ResolveFileService implements Resolver.
func (r *FileServiceResolver) ResolveFileService(filePath string) interfaces.FileService {
	if r.authorize != nil {
		if err := r.authorize(r.ctx, filePath); err != nil {
			logger.Warnf(r.ctx, "resource URL access denied: src=%s err=%v", filePath, err)
			return nil
		}
	}
	if _, ok := types.ParseResourcePath(filePath); ok {
		if r.resourceCatalog != nil {
			return r.resolveResource(filePath)
		}
		return r.defaultSvc
	}
	backendID, _, _ := types.ParseStorageBackendPath(filePath)
	provider := types.ParseProviderScheme(filePath)
	if provider == "" {
		if r.tenant != nil && r.tenant.StorageEngineConfig != nil {
			provider = strings.ToLower(strings.TrimSpace(r.tenant.StorageEngineConfig.DefaultProvider))
		}
		if provider == "" {
			return nil
		}
	}
	return r.resolveStorage(r.tenant, backendID, provider, backendID == "")
}

func (r *FileServiceResolver) resolveResource(reference string) interfaces.FileService {
	resolve := r.resourceCatalog.Resolve
	if bindings, ok := r.resourceCatalog.(interfaces.ResourceStorageBindingResolver); ok {
		resolve = bindings.ResolveStorageBinding
	}
	resource, err := resolve(r.ctx, reference)
	if err != nil || resource == nil || resource.TenantID == 0 {
		logger.Warnf(r.ctx, "resolve resource storage failed: src=%s err=%v", reference, err)
		return nil
	}
	tenant := r.tenant
	if tenant == nil || tenant.ID != resource.TenantID {
		tenant = r.owners[resource.TenantID]
		if tenant == nil && r.tenantService != nil {
			tenant, err = r.tenantService.GetTenantByID(r.ctx, resource.TenantID)
		}
		if err != nil || tenant == nil || tenant.ID != resource.TenantID {
			logger.Warnf(r.ctx, "resolve resource owner failed: src=%s tenant_id=%d err=%v",
				reference, resource.TenantID, err)
			return nil
		}
		r.owners[resource.TenantID] = tenant
	}
	backendID := resource.StorageBackendID
	if backendID == "" {
		backendID, _, _ = types.ParseStorageBackendPath(resource.PhysicalPath)
	}
	if backendID == "" {
		return nil
	}
	provider := resource.Provider
	if provider == "" {
		provider = types.ParseProviderScheme(resource.PhysicalPath)
	}
	// A registered resource belongs to this owner's backend. Failure must not
	// sign its physical path with unrelated process-global credentials.
	return r.resolveStorage(tenant, backendID, provider, false)
}

func (r *FileServiceResolver) resolveStorage(
	tenant *types.Tenant,
	backendID, provider string,
	allowFallback bool,
) interfaces.FileService {
	cacheKey := fileServiceKey{backendID: backendID, provider: provider}
	if tenant != nil {
		cacheKey.tenantID = tenant.ID
	}
	if svc, ok := r.cache[cacheKey]; ok {
		return svc
	}
	if r.storageResolver != nil && tenant != nil {
		svc, _, err := r.storageResolver.ResolveFileService(r.ctx, tenant, backendID, provider, LocalStorageBaseDir())
		if err == nil && svc != nil {
			r.cache[cacheKey] = svc
			return svc
		}
		logger.Warnf(r.ctx, "resolve storage backend failed: backend_id=%s provider=%s err=%v",
			backendID, provider, err)
	}
	if !allowFallback {
		return nil
	}
	svc := BuildFileServiceForProvider(tenant, provider, r.defaultSvc)
	if svc != nil {
		r.cache[cacheKey] = svc
	}
	return svc
}

// BuildFileServiceForProvider selects the FileService for a storage provider.
// The reference's own scheme wins over the tenant DefaultProvider. It falls back
// to the process-wide default FileService (STORAGE_TYPE / env) when tenant
// config is missing — mirrors ImageMultimodalService.resolveFileServiceForPayload
// (issue #1282).
func BuildFileServiceForProvider(
	tenant *types.Tenant,
	provider string,
	defaultSvc interfaces.FileService,
) interfaces.FileService {
	baseDir := LocalStorageBaseDir()
	var sec *types.StorageEngineConfig
	if tenant != nil {
		sec = tenant.StorageEngineConfig
	}

	svc, _, err := filesvc.NewFileServiceFromStorageConfig(provider, sec, baseDir)
	if err == nil {
		return svc
	}
	if provider == "local" {
		externalURL := strings.TrimSpace(os.Getenv("APP_EXTERNAL_URL"))
		return filesvc.NewLocalFileService(baseDir, externalURL)
	}
	if defaultSvc != nil {
		return defaultSvc
	}
	return nil
}

var _ Resolver = (*FileServiceResolver)(nil)
