package utils

import (
	"os"
	"strconv"
)

// GetMaxFileSize returns the maximum file upload size in bytes.
// Default is 50MB, can be configured via MAX_FILE_SIZE_MB environment variable.
func GetMaxFileSize() int64 {
	if sizeStr := os.Getenv("MAX_FILE_SIZE_MB"); sizeStr != "" {
		if size, err := strconv.ParseInt(sizeStr, 10, 64); err == nil && size > 0 {
			return size * 1024 * 1024
		}
	}
	return 50 * 1024 * 1024 // default 50MB
}

// GetMaxFileSizeMB returns the maximum file upload size in MB.
func GetMaxFileSizeMB() int64 {
	if sizeStr := os.Getenv("MAX_FILE_SIZE_MB"); sizeStr != "" {
		if size, err := strconv.ParseInt(sizeStr, 10, 64); err == nil && size > 0 {
			return size
		}
	}
	return 50 // default 50MB
}
