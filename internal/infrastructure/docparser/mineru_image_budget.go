package docparser

import (
	"context"
	"os"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
)

const mineruMaxImageBytesEnv = "WEKNORA_MINERU_MAX_IMAGE_BYTES"

// minerUMaxImageBytes returns the per-result-package image budget. A deployment
// can raise it for large scanned documents while the bounded default remains in
// place for installations that do not set the environment variable.
func minerUMaxImageBytes() int64 {
	value := strings.TrimSpace(os.Getenv(mineruMaxImageBytesEnv))
	if value == "" {
		return maxZipImagesTotalBytes
	}

	limit, err := strconv.ParseInt(value, 10, 64)
	if err != nil || limit <= 0 {
		logger.Warnf(context.Background(), "Invalid %s %q; using %d bytes",
			mineruMaxImageBytesEnv, value, maxZipImagesTotalBytes)
		return maxZipImagesTotalBytes
	}
	return limit
}
