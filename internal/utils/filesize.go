package utils

import (
	"os"
	"strconv"
)

// GetMaxFileSize returns the maximum file upload size in bytes.
// Default is 50MB, can be configured via MAX_FILE_SIZE_MB environment variable.
//
// MAX_FILE_SIZE_MB is intentionally a deploy-time-only knob (NOT a
// runtime system_setting). The effective upload limit is gated by
// three other layers that all read this env at startup and cache the
// value:
//   - frontend nginx client_max_body_size (envsubst into nginx.conf)
//   - docreader gRPC max_send/recv_message_length
//   - frontend client-side check via window.__RUNTIME_CONFIG__
//
// Surfacing a SystemAdmin UI knob whose effect is silently capped by
// any of the above would mislead operators ("I raised it to 200MB but
// nginx still returns 413"). Until all four layers can be reconfigured
// in lockstep without container restarts, every call site must read
// the env directly via this helper.
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
