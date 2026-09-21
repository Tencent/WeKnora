package utils

import (
	"os"
	"strconv"
)

const (
	defaultMaxFileSizeMB        = 50
	defaultMaxSkillBundleSizeMB = 256
	// maxSkillBundleSizeMBCeiling matches the install-time uncompressed
	// archive cap: a download larger than that cannot become a valid skill.
	maxSkillBundleSizeMBCeiling = 512
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
	return GetMaxFileSizeMB() * 1024 * 1024
}

// GetMaxFileSizeMB returns the maximum file upload size in MB. Same
// caveat as GetMaxFileSize — handlers should prefer SystemSettingService.GetInt.
func GetMaxFileSizeMB() int64 {
	return envSizeMB("MAX_FILE_SIZE_MB", defaultMaxFileSizeMB)
}

// GetMaxSkillBundleSize is the compressed zip / source-download cap for
// skills. Knowledge uploads stay on GetMaxFileSize: packages such as
// ppt-master exceed 50MB, but raising the document limit would also
// inflate docreader gRPC messages. A GitHub zipball is the whole
// repository archive, not the SKILL.md subtree, so a huge monorepo can
// still be refused here even when the skill itself is small.
func GetMaxSkillBundleSize() int64 {
	return GetMaxSkillBundleSizeMB() * 1024 * 1024
}

// GetMaxSkillBundleSizeMB returns the skill zip cap in MB.
// MAX_SKILL_BUNDLE_SIZE_MB defaults to 256, is never below MAX_FILE_SIZE_MB,
// and is capped at 512.
func GetMaxSkillBundleSizeMB() int64 {
	skillMB := envSizeMB("MAX_SKILL_BUNDLE_SIZE_MB", defaultMaxSkillBundleSizeMB)
	if fileMB := GetMaxFileSizeMB(); skillMB < fileMB {
		skillMB = fileMB
	}
	if skillMB > maxSkillBundleSizeMBCeiling {
		return maxSkillBundleSizeMBCeiling
	}
	return skillMB
}

func envSizeMB(key string, fallback int64) int64 {
	if sizeStr := os.Getenv(key); sizeStr != "" {
		if size, err := strconv.ParseInt(sizeStr, 10, 64); err == nil && size > 0 {
			return size
		}
	}
	return fallback
}
