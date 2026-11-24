package docparser

import (
	"context"
	"os"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
)

const (
	mineruMaxImageBytesEnv    = "WEKNORA_MINERU_MAX_IMAGE_BYTES"
	mineruMaxZipBytesEnv      = "WEKNORA_MINERU_MAX_ZIP_BYTES"
	mineruMaxZipEntryBytesEnv = "WEKNORA_MINERU_MAX_ZIP_ENTRY_BYTES"
)

// minerUMaxImageBytes returns the per-result-package image budget. A deployment
// can raise it for large scanned documents while the bounded default remains in
// place for installations that do not set the environment variable.
func minerUMaxImageBytes() int64 {
	return minerUMaxBytes(mineruMaxImageBytesEnv, maxZipImagesTotalBytes)
}

// minerUMaxZipBytes returns the compressed result-package download limit used
// by both MinerU Cloud and self-hosted V1 clients.
func minerUMaxZipBytes() int64 {
	return minerUMaxBytes(mineruMaxZipBytesEnv, mineruV1MaxZipBytes)
}

// minerUMaxZipEntryBytes returns the decompressed limit for one result-package
// member (markdown, layout JSON or image).
func minerUMaxZipEntryBytes() int64 {
	return minerUMaxBytes(mineruMaxZipEntryBytesEnv, maxZipEntryBytes)
}

func minerUMaxBytes(envName string, defaultLimit int64) int64 {
	value := strings.TrimSpace(os.Getenv(envName))
	if value == "" {
		return defaultLimit
	}

	limit, err := strconv.ParseInt(value, 10, 64)
	if err != nil || limit <= 0 {
		logger.Warnf(context.Background(), "Invalid %s %q; using %d bytes",
			envName, value, defaultLimit)
		return defaultLimit
	}
	return limit
}
