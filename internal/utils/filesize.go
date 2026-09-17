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

const (
	defaultMaxBackupArchiveSizeMB = 4096
	maxBackupArchiveSizeMBCeiling = 32768
	maxBackupExtractBytesCeiling  = 32 << 30
)

// GetMaxBackupArchiveSize is the compressed backup-upload cap in bytes.
// Default 4096 MB (4 GiB). Nginx has a matching location for
// POST /api/v1/backups/restore so the frontend proxy does not 413 at
// MAX_FILE_SIZE_MB. Never below MAX_FILE_SIZE_MB; ceiling 32768 MB.
func GetMaxBackupArchiveSize() int64 {
	return GetMaxBackupArchiveSizeMB() * 1024 * 1024
}

// GetMaxBackupArchiveSizeMB returns the backup-upload cap in MB.
func GetMaxBackupArchiveSizeMB() int64 {
	mb := envSizeMB("MAX_BACKUP_ARCHIVE_SIZE_MB", defaultMaxBackupArchiveSizeMB)
	if fileMB := GetMaxFileSizeMB(); mb < fileMB {
		mb = fileMB
	}
	if mb > maxBackupArchiveSizeMBCeiling {
		return maxBackupArchiveSizeMBCeiling
	}
	return mb
}

// GetMaxBackupExtractBytes caps uncompressed bytes written while unpacking
// a backup (zip-bomb guard). Defaults to 8× the compressed upload cap,
// never above 32 GiB.
func GetMaxBackupExtractBytes() int64 {
	capBytes := GetMaxBackupArchiveSize() * 8
	if capBytes > maxBackupExtractBytesCeiling {
		return maxBackupExtractBytesCeiling
	}
	if capBytes < 1<<30 {
		return 1 << 30
	}
	return capBytes
}
