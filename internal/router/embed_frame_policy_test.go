package router

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type framePolicyService struct{ interfaces.EmbedChannelService }

func (framePolicyService) LookupEnabledChannel(_ context.Context, id string) (*types.EmbedChannel, error) {
	switch id {
	case "active":
		return &types.EmbedChannel{AllowedOrigins: []byte(`["https://a.example"]`)}, nil
	case "empty":
		return &types.EmbedChannel{AllowedOrigins: []byte(`[]`)}, nil
	case "invalid":
		return &types.EmbedChannel{AllowedOrigins: []byte(`["https://a.example; frame-ancestors *"]`)}, nil
	default:
		return nil, fmt.Errorf("channel unavailable")
	}
}

func TestEmbedFramePolicy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	webDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(webDir, "index.html"), []byte("main SPA"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(webDir, "embed.html"), []byte("embed entry"), 0o600))
	t.Setenv("WEKNORA_WEB_DIR", webDir)
	r := gin.New()
	r.Use(embedFrameAncestorsMiddleware(framePolicyService{}))
	serveFrontendStatic(r)
	r.GET("/api/v1/embed-frame-policy", embedFramePolicyHandler(framePolicyService{}))
	for _, tc := range []struct {
		path   string
		status int
		csp    string
	}{
		{"/embed/active", 200, "frame-ancestors 'self' https://a.example"},
		{"/embed/active?token=ignored", 200, "frame-ancestors 'self' https://a.example"},
		{"/embed/empty", 403, "frame-ancestors 'none'"},
		{"/embed/invalid", 403, "frame-ancestors 'none'"},
		{"/embed/disabled", 403, "frame-ancestors 'none'"},
		{"/embed/active%2F..%2Fdisabled", 403, "frame-ancestors 'none'"},
		{"/embed/active/../disabled", 403, "frame-ancestors 'none'"},
		{"/embed/active%252F..%252Fdisabled", 403, "frame-ancestors 'none'"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			// Lite returns the dedicated entry, with a fresh CSP on GET and HEAD.
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				w := httptest.NewRecorder()
				r.ServeHTTP(w, httptest.NewRequest(method, tc.path, nil))
				require.Equal(t, tc.status, w.Code)
				require.Equal(t, tc.csp, w.Header().Get("Content-Security-Policy"))
				require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
				if tc.status == 200 && method == http.MethodGet {
					require.Equal(t, "embed entry", w.Body.String())
				}
			}
			// Nginx receives the identical policy, without exposing channel config.
			req := httptest.NewRequest(http.MethodGet, "/api/v1/embed-frame-policy", nil)
			req.Header.Set("X-Embed-Page-URI", tc.path)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			want := tc.status
			if want == 200 {
				want = 204
			}
			require.Equal(t, want, w.Code)
			require.Equal(t, tc.csp, w.Header().Get("Content-Security-Policy"))
			require.Empty(t, w.Body.String())
		})
	}
	for _, path := range []string{"/", "/embed.html"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, "frame-ancestors 'self'", w.Header().Get("Content-Security-Policy"))
	}
	for _, uri := range []string{"", "/", "/embed/", "https://evil.test/embed/active"} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/embed-frame-policy", nil)
		req.Header.Set("X-Embed-Page-URI", uri)
		r.ServeHTTP(w, req)
		require.Equal(t, 403, w.Code, uri)
	}
}
