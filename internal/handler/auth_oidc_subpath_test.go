package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/gin-gonic/gin"
)

func TestOIDCRedirectCallback_SubpathAndRoot(t *testing.T) {
	cases := []struct {
		name    string
		baseURL string
		want    string
	}{
		{
			name:    "root deployment",
			baseURL: "",
			want:    "/#oidc_error=access_denied",
		},
		{
			name:    "subpath deployment",
			baseURL: "https://example.com/weknora/",
			want:    "https://example.com/weknora/#oidc_error=access_denied",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("FRONTEND_BASE_URL", "")

			h := &AuthHandler{
				configInfo: &config.Config{
					FrontendBaseURL: tc.baseURL,
				},
			}

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			c.Request = httptest.NewRequest(
				http.MethodGet,
				"/api/v1/auth/oidc/callback?error=access_denied",
				nil,
			)

			h.OIDCRedirectCallback(c)

			if w.Code != http.StatusFound {
				t.Fatalf("status = %d, want 302", w.Code)
			}

			if got := w.Header().Get("Location"); got != tc.want {
				t.Fatalf("Location = %q, want %q", got, tc.want)
			}
		})
	}
}
