package service

import (
	"context"
	"math"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	quotaMiB                  int64 = 1024 * 1024
	quotaGiB                        = 1024 * quotaMiB
	defaultTenantStorageQuota       = 10 * quotaGiB
)

// ResolveDefaultTenantStorageQuota returns the effective default in bytes.
// Each setting uses DB > ENV > default resolution. A positive MB setting
// takes precedence over the legacy GB setting; zero or negative MB falls
// back to GB. Invalid GB falls back to 10 GiB. Overflow must never turn a
// configured quota into a non-positive (unlimited) value.
func ResolveDefaultTenantStorageQuota(ctx context.Context, settings interfaces.SystemSettingService) int64 {
	if settings == nil {
		return defaultTenantStorageQuota
	}
	mb := settings.GetInt(ctx, "tenant.default_storage_quota_mb", "WEKNORA_TENANT_DEFAULT_STORAGE_QUOTA_MB", 0)
	if mb > 0 && mb <= math.MaxInt64/quotaMiB {
		return mb * quotaMiB
	}
	if mb > math.MaxInt64/quotaMiB {
		logger.Warn(ctx, "Default tenant storage quota in MB exceeds int64 bytes; falling back to GB")
	}
	gb := settings.GetInt(ctx, "tenant.default_storage_quota_gb", "WEKNORA_TENANT_DEFAULT_STORAGE_QUOTA_GB", 10)
	if gb <= 0 || gb > math.MaxInt64/quotaGiB {
		return defaultTenantStorageQuota
	}
	return gb * quotaGiB
}
