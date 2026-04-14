package docparser

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
)

// stringOr returns val (trimmed) if non-empty, otherwise fallback.
func stringOr(val, fallback string) string {
	val = strings.TrimSpace(val)
	if val == "" {
		return fallback
	}
	return val
}

// parseBoolOr parses a truthy string ("true","1","yes"), returning fallback on empty.
func parseBoolOr(val string, fallback bool) bool {
	val = strings.ToLower(strings.TrimSpace(val))
	if val == "" {
		return fallback
	}
	return val == "true" || val == "1" || val == "yes"
}

// firstNonEmpty returns the first non-empty string, or "" if all are empty.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// sleepCtx sleeps for d but returns early if ctx is cancelled.
func sleepCtx(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// logResponseStructure recursively logs the structure of an API response,
// truncating large string values. label identifies the subsystem (e.g. "MinerU").
func logResponseStructure(label string, obj interface{}, prefix string) {
	switch v := obj.(type) {
	case map[string]interface{}:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		logger.Infof(context.Background(), "[%s] %s = {object with %d keys: %s}", label, prefix, len(v), strings.Join(keys, ", "))
		for _, key := range keys {
			val := v[key]
			path := prefix + "." + key
			switch inner := val.(type) {
			case map[string]interface{}:
				logResponseStructure(label, inner, path)
			case []interface{}:
				logger.Infof(context.Background(), "[%s] %s = [array with %d items]", label, path, len(inner))
				if len(inner) > 0 {
					logger.Infof(context.Background(), "[%s] %s[0] type=%T", label, path, inner[0])
					if len(inner) <= 3 {
						for i, item := range inner {
							logResponseStructure(label, item, fmt.Sprintf("%s[%d]", path, i))
						}
					} else {
						logResponseStructure(label, inner[0], path+"[0]")
						logger.Infof(context.Background(), "[%s] ... and %d more items in %s", label, len(inner)-1, path)
					}
				}
			case string:
				if len(inner) > 200 {
					logger.Infof(context.Background(), "[%s] %s = string(%d chars): %.200s...", label, path, len(inner), inner)
				} else {
					logger.Infof(context.Background(), "[%s] %s = %q", label, path, inner)
				}
			case float64:
				logger.Infof(context.Background(), "[%s] %s = %v (number)", label, path, inner)
			case bool:
				logger.Infof(context.Background(), "[%s] %s = %v (bool)", label, path, inner)
			case nil:
				logger.Infof(context.Background(), "[%s] %s = null", label, path)
			default:
				logger.Infof(context.Background(), "[%s] %s = %v (%T)", label, path, val, val)
			}
		}
	case []interface{}:
		logger.Infof(context.Background(), "[%s] %s = [array with %d items]", label, prefix, len(v))
		if len(v) > 0 {
			if len(v) <= 3 {
				for i, item := range v {
					logResponseStructure(label, item, fmt.Sprintf("%s[%d]", prefix, i))
				}
			} else {
				logResponseStructure(label, v[0], prefix+"[0]")
				logger.Infof(context.Background(), "[%s] ... and %d more items in %s", label, len(v)-1, prefix)
			}
		}
	default:
		logger.Infof(context.Background(), "[%s] %s = %v (%T)", label, prefix, v, v)
	}
}
