package router

import (
	"context"
	"net/http"
	"os"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
)

// AppBasePath is the URL prefix (for example "/weknora") under which the
// application is publicly exposed, configured via APP_BASE_PATH.
//
// An empty value (or "/") means root serving, which is the default and
// preserves the existing behavior. The value is a path, not a URL: it must
// be root-relative and must not contain whitespace or characters that are
// unsafe in an nginx location or a URL path. Invalid values are dropped
// (root serving) with a warning, so a typo can never break startup.
func AppBasePath() string {
	raw := os.Getenv("APP_BASE_PATH")
	normalized := NormalizeBasePath(raw)
	if strings.TrimSpace(raw) != "" && normalized == "" {
		logger.Warnf(context.Background(),
			"[Router] ignoring invalid APP_BASE_PATH %q: expected a root-relative path like /weknora", raw)
	}
	return normalized
}

// NormalizeBasePath canonicalizes a raw base-path value: surrounding
// whitespace is trimmed, a missing leading slash is added, trailing slashes
// are removed, and "" means "serve at root". Values containing whitespace or
// URL/nginx metacharacters are rejected ("" is returned).
func NormalizeBasePath(raw string) string {
	p := strings.TrimSpace(raw)
	if p == "" || p == "/" {
		return ""
	}
	if strings.ContainsAny(p, " \t\r\n;{}#?\\") {
		return ""
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	p = strings.TrimRight(p, "/")
	if p == "" {
		return ""
	}
	return p
}

// stripBasePathPrefix removes base from the front of path. It reports
// whether the path was under the prefix: an exact match strips to "/".
// A path that merely shares the textual prefix (for example "/weknora2" for
// base "/weknora") is not stripped.
func stripBasePathPrefix(path, base string) (string, bool) {
	if base == "" {
		return path, false
	}
	if path == base {
		return "/", true
	}
	if strings.HasPrefix(path, base+"/") {
		return path[len(base):], true
	}
	return "", false
}

// StripAppBasePath wraps an http.Handler so the application can be served
// under APP_BASE_PATH (for example behind a gateway that forwards
// "/weknora/*" without stripping the prefix).
//
// The prefix is removed before the wrapped handler sees the request, so all
// routing, middleware path checks and static serving keep working on
// root-relative paths. Requests outside the prefix pass through unchanged
// (direct access and the container healthcheck keep working), and the query
// string is preserved.
func StripAppBasePath(next http.Handler) http.Handler {
	base := AppBasePath()
	if base == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stripped, ok := stripBasePathPrefix(r.URL.Path, base)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		r2 := r.Clone(r.Context())
		u := *r.URL
		u.Path = stripped
		u.RawPath = ""
		r2.URL = &u
		r2.RequestURI = u.RequestURI()
		next.ServeHTTP(w, r2)
	})
}
