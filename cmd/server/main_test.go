package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWithSubpathPrefix(t *testing.T) {
	cases := []struct {
		name     string
		prefix   string
		inPath   string
		wantPath string
	}{
		{"empty prefix leaves path unchanged", "", "/weknora/api/v1/x", "/weknora/api/v1/x"},
		{"trailing slash is normalized", "/weknora/", "/weknora/api/v1/x", "/api/v1/x"},
		{"surrounding spaces are trimmed", "  /weknora  ", "/weknora/api/v1/x", "/api/v1/x"},
		{"leading slash is optional", "weknora", "/weknora/api/v1/x", "/api/v1/x"},
		{"exact prefix match rewrites to root", "/weknora", "/weknora", "/"},
		{"nested prefix is stripped", "/kb/weknora", "/kb/weknora/api/v1/x/y", "/api/v1/x/y"},
		{"non-matching path is left intact", "/weknora", "/api/v1/health", "/api/v1/health"},
		{"prefix-looking path is left intact", "/weknora", "/weknora-other/x", "/weknora-other/x"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got string
			inner := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				got = r.URL.Path
			})
			wrapped := withSubpathPrefix(inner, tc.prefix)

			req := httptest.NewRequest(http.MethodGet, "http://localhost"+tc.inPath, nil)
			wrapped.ServeHTTP(httptest.NewRecorder(), req)

			if got != tc.wantPath {
				t.Fatalf("path mismatch: got %q, want %q", got, tc.wantPath)
			}
		})
	}
}
