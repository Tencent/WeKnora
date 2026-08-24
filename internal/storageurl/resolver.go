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

// FileServiceResolver resolves and caches one FileService per storage provider.
// The cache is scoped to a single request or outbound message so a long answer
// with many images does not re-create an SDK client per reference.
//
// Not safe for concurrent use; Rewriter drives it from one goroutine at a time.
type FileServiceResolver struct {
	tenant          *types.Tenant
	defaultSvc      interfaces.FileService
	storageResolver interfaces.StorageBackendResolver
	ctx             context.Context
	cache           map[string]interfaces.FileService
}

// NewFileServiceResolver builds a resolver for tenant. defaultSvc is the
// process-wide FileService used for `resource://` handles and as the fallback
// when tenant storage config is missing.
func NewFileServiceResolver(
	tenant *types.Tenant,
	defaultSvc interfaces.FileService,
	storageResolvers ...interfaces.StorageBackendResolver,
) *FileServiceResolver {
	resolver := &FileServiceResolver{
		tenant:     tenant,
		defaultSvc: defaultSvc,
		ctx:        context.Background(),
		cache:      make(map[string]interfaces.FileService),
	}
	if len(storageResolvers) > 0 {
		resolver.storageResolver = storageResolvers[0]
	}
	return resolver
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
	if _, ok := types.ParseResourcePath(filePath); ok {
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
	cacheKey := backendID + ":" + provider
	if svc, ok := r.cache[cacheKey]; ok {
		return svc
	}
	if r.storageResolver != nil && r.tenant != nil {
		svc, _, err := r.storageResolver.ResolveFileService(r.ctx, r.tenant, backendID, provider, LocalStorageBaseDir())
		if err == nil {
			r.cache[cacheKey] = svc
			return svc
		}
		logger.Warnf(r.ctx, "resolve storage backend failed: backend_id=%s provider=%s err=%v",
			backendID, provider, err)
	}
	svc := BuildFileServiceForProvider(r.tenant, provider, r.defaultSvc)
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
