package utils

import (
	"os"
	"strconv"
)

// GetMaxFileSize returns the maximum file upload size in bytes.
// Default is 50MB, can be configured via MAX_FILE_SIZE_MB environment variable.
//
// This is the legacy ENV-only entry point. Handlers wired with
// interfaces.SystemSettingService should prefer reading the 3-tier
// resolver directly:
//
//	mb := h.systemSettingSvc.GetInt(ctx, "file.max_size_mb", "MAX_FILE_SIZE_MB", 50)
//	maxBytes := mb * 1024 * 1024
//
// That way SystemAdmin's UI override (DB row in system_settings) takes
// precedence over the ENV value. This function is kept for unit tests
// and any non-handler call sites that don't have the service injected;
// it never sees DB values, only ENV.
//
// We deliberately don't expose a "service-aware" wrapper here because
// internal/types depends on internal/utils — wrapping it would create
// an import cycle. Call sites that have a SystemSettingService should
// just use it directly (it's only one extra line).
func GetMaxFileSize() int64 {
	if sizeStr := os.Getenv("MAX_FILE_SIZE_MB"); sizeStr != "" {
		if size, err := strconv.ParseInt(sizeStr, 10, 64); err == nil && size > 0 {
			return size * 1024 * 1024
		}
	}
	return 50 * 1024 * 1024 // default 50MB
}

// GetMaxFileSizeMB returns the maximum file upload size in MB. Same
// caveat as GetMaxFileSize — handlers should prefer SystemSettingService.GetInt.
func GetMaxFileSizeMB() int64 {
	if sizeStr := os.Getenv("MAX_FILE_SIZE_MB"); sizeStr != "" {
		if size, err := strconv.ParseInt(sizeStr, 10, 64); err == nil && size > 0 {
			return size
		}
	}
	return 50 // default 50MB
}
