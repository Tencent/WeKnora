package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeBasePath(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want string
	}{
		{"", ""},
		{"   ", ""},
		{"/", ""},
		{"/weknora", "/weknora"},
		{"/weknora/", "/weknora"},
		{"/weknora//", "/weknora"},
		{"  /weknora/  ", "/weknora"},
		{"weknora", "/weknora"},
		{"weknora/", "/weknora"},
		{"/a/b", "/a/b"},
		// Invalid values fail closed to root serving.
		{"/weknora api", ""},
		{"/weknora;evil", ""},
		{"/weknora?x=1", ""},
		{"/weknora#frag", ""},
		{"/weknora\\x", ""},
	} {
		require.Equal(t, tc.want, NormalizeBasePath(tc.raw), "raw %q", tc.raw)
	}
}

func TestStripAppBasePath(t *testing.T) {
	seen := ""
	inner := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = r.URL.Path + "|" + r.URL.RequestURI()
	})

	t.Setenv("APP_BASE_PATH", "/weknora")
	wrapped := StripAppBasePath(inner)

	// Prefixed API path: prefix removed, query preserved.
	seen = ""
	wrapped.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/weknora/api/v1/auth/me?x=1", nil))
	require.Equal(t, "/api/v1/auth/me|/api/v1/auth/me?x=1", seen)

	// Exact prefix: root.
	seen = ""
	wrapped.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/weknora", nil))
	require.Equal(t, "/|/", seen)

	// Same textual prefix without a segment boundary: untouched.
	seen = ""
	wrapped.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/weknora2/api", nil))
	require.Equal(t, "/weknora2/api|/weknora2/api", seen)

	// Root path (healthcheck / direct access): untouched.
	seen = ""
	wrapped.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/health", nil))
	require.Equal(t, "/health|/health", seen)

	// Unset prefix: wrapper is a pass-through.
	t.Setenv("APP_BASE_PATH", "")
	wrapped = StripAppBasePath(inner)
	seen = ""
	wrapped.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/weknora/api/v1/auth/me", nil))
	require.Equal(t, "/weknora/api/v1/auth/me|/weknora/api/v1/auth/me", seen)
}
