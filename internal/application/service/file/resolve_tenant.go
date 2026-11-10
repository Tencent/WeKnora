package file

import (
	"context"
	"net/http"
	"os"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// ResolveTenantFileServiceWithFallback applies the storage resolution and
// fallback rule shared by file proxies and artifact downloads: when tenant-level
// resolution fails and the requested provider matches the process-global
// STORAGE_TYPE, serve through the global file service instead of failing.
// Returns ok=false (already logged) when no service can be resolved; the
// caller decides the HTTP status.
func ResolveTenantFileServiceWithFallback(
	ctx context.Context,
	logTag string,
	tenant *types.Tenant,
	backendID, provider, absDir string,
	storageResolver interfaces.StorageBackendResolver,
	globalFileService interfaces.FileService,
) (fileSvc interfaces.FileService, resolvedProvider string, ok bool) {
	var err error
	if storageResolver != nil {
		fileSvc, resolvedProvider, err = storageResolver.ResolveFileService(ctx, tenant, backendID, provider, absDir)
	} else if tenant.StorageEngineConfig != nil {
		fileSvc, resolvedProvider, err = NewFileServiceFromStorageConfig(provider, tenant.StorageEngineConfig, absDir)
	} else {
		err = http.ErrMissingFile
	}
	if err == nil {
		return fileSvc, resolvedProvider, true
	}

	globalStorageType := strings.ToLower(strings.TrimSpace(os.Getenv("STORAGE_TYPE")))
	if globalStorageType == "" {
		globalStorageType = "local"
	}
	if provider == globalStorageType && globalFileService != nil {
		logger.Warnf(
			ctx,
			"[Router] %s tenant storage config missing or invalid, fallback to global file "+
				"service: tenant_id=%d provider=%s err=%v",

			logTag,
			tenant.ID,
			provider,
			err,
		)
		return globalFileService, globalStorageType, true
	}
	logger.Warnf(
		ctx,
		"[Router] %s resolve file service failed without fallback: tenant_id=%d "+
			"provider=%s global_storage_type=%s err=%v",

		logTag,
		tenant.ID,
		provider,
		globalStorageType,
		err,
	)
	return nil, "", false
}
