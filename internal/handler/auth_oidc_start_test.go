package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// stubOIDCStartUserService only implements GetOIDCAuthorizationURL; every
// other UserService call panics via the nil interface embedding. Keeps the
// test focused on the OIDCStart handler's branching logic.
type stubOIDCStartUserService struct {
	interfaces.UserService
	getOIDCAuthorizationURL func(ctx context.Context, redirectURI string) (*types.OIDCAuthURLResponse, error)
}

func (s *stubOIDCStartUserService) GetOIDCAuthorizationURL(ctx context.Context, redirectURI string) (*types.OIDCAuthURLResponse, error) {
	return s.getOIDCAuthorizationURL(ctx, redirectURI)
}

func newOIDCStartTestRouter(h *AuthHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(errorCapture())
	r.GET("/auth/oidc/start", h.OIDCStart)
	return r
}

// TestOIDCStart_RedirectsToAuthProvider: on success the handler must 302
// to the IdP authorization URL and bind the nonce via cookie (CSRF/replay
// defence), exactly like /auth/oidc/url.
func TestOIDCStart_RedirectsToAuthProvider(t *testing.T) {
	const authURL = "http://idp.example.com/authorize?client_id=weknora"
	us := &stubOIDCStartUserService{
		getOIDCAuthorizationURL: func(context.Context, string) (*types.OIDCAuthURLResponse, error) {
			return &types.OIDCAuthURLResponse{
				Success:          true,
				AuthorizationURL: authURL,
				State:            "state123",
				Nonce:            "nonce456",
			}, nil
		},
	}
	h := NewAuthHandler(&config.Config{}, us, nil, nil, nil)
	r := newOIDCStartTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/auth/oidc/start", nil)
	req.Host = "weknora.example.com"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != authURL {
		t.Errorf("Location = %q, want %q", loc, authURL)
	}
	var hasNonce bool
	for _, ck := range w.Result().Cookies() {
		if ck.Name == oidcNonceCookieName && ck.Value == "nonce456" {
			hasNonce = true
		}
	}
	if !hasNonce {
		t.Errorf("expected %s cookie to be set with the nonce", oidcNonceCookieName)
	}
}

// TestOIDCStart_BuildsCallbackURLFromRequestOrigin: redirect_uri handed to
// the IdP is derived from the request's own origin (no caller-supplied
// value needed); X-Forwarded-Proto upgrades the scheme to https.
func TestOIDCStart_BuildsCallbackURLFromRequestOrigin(t *testing.T) {
	var captured string
	us := &stubOIDCStartUserService{
		getOIDCAuthorizationURL: func(_ context.Context, redirectURI string) (*types.OIDCAuthURLResponse, error) {
			captured = redirectURI
			return &types.OIDCAuthURLResponse{Success: true, AuthorizationURL: "http://idp", Nonce: "n"}, nil
		},
	}
	h := NewAuthHandler(&config.Config{}, us, nil, nil, nil)
	r := newOIDCStartTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/auth/oidc/start", nil)
	req.Host = "weknora.example.com"
	req.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	const want = "https://weknora.example.com/api/v1/auth/oidc/callback"
	if captured != want {
		t.Errorf("callback URL passed to IdP = %q, want %q", captured, want)
	}
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", w.Code)
	}
}

// TestOIDCStart_ServiceErrorReturnsNonRedirect: if the IdP URL cannot be
// built, never 302 — surface the error the same way /auth/oidc/url does.
func TestOIDCStart_ServiceErrorReturnsNonRedirect(t *testing.T) {
	us := &stubOIDCStartUserService{
		getOIDCAuthorizationURL: func(context.Context, string) (*types.OIDCAuthURLResponse, error) {
			return nil, fmt.Errorf("idp unavailable")
		},
	}
	h := NewAuthHandler(&config.Config{}, us, nil, nil, nil)
	r := newOIDCStartTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/auth/oidc/start", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code == http.StatusFound {
		t.Fatalf("status = 302, want non-redirect on service error")
	}
}
