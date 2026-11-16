package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
)

func TestOIDCPKCEProofCookie(t *testing.T) {
	for _, route := range []string{"/auth/oidc/start", "/auth/oidc/url"} {
		t.Run(route, func(t *testing.T) {
			buildAuthURL := func(context.Context, string) (*types.OIDCAuthURLResponse, error) {
				return &types.OIDCAuthURLResponse{
					Success: true, AuthorizationURL: "https://idp.example/authorize",
					Nonce: "nonce", PKCEVerifier: "proof-key",
				}, nil
			}
			us := &stubOIDCStartUserService{getOIDCAuthorizationURL: buildAuthURL}
			h := NewAuthHandler(&config.Config{}, us, nil, nil, nil)
			gin.SetMode(gin.TestMode)
			router := gin.New()
			router.GET("/auth/oidc/start", h.OIDCStart)
			router.GET("/auth/oidc/url", h.GetOIDCAuthorizationURL)
			recorder := httptest.NewRecorder()
			requestURL := "https://app.example" + route + "?redirect_uri=https%3A%2F%2Fapp.example%2Fcallback"
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, requestURL, nil))
			for _, cookie := range recorder.Result().Cookies() {
				if cookie.Name == oidcPKCECookieName {
					if cookie.Value != "proof-key" || !cookie.HttpOnly || !cookie.Secure ||
						cookie.SameSite != http.SameSiteLaxMode || cookie.MaxAge != oidcNonceCookieMaxAge {
						t.Fatal("PKCE cookie must be short-lived, HttpOnly, SameSite=Lax and secure on HTTPS")
					}
					return
				}
			}
			t.Fatal("missing PKCE proof cookie")
		})
	}
}

type stubOIDCPKCECallback struct {
	interfaces.UserService
	verifier string
}

func (s *stubOIDCPKCECallback) LoginWithOIDC(
	_ context.Context, _, _ string, _ types.TenantProvisioningMode, verifier string,
) (*types.OIDCCallbackResponse, error) {
	s.verifier = verifier
	return &types.OIDCCallbackResponse{Success: false, Message: "stop after exchange boundary"}, nil
}

func TestOIDCPKCECallbackRequiresProofAndConsumesCookies(t *testing.T) {
	state, err := secutils.SignOIDCState(&secutils.OIDCStatePayload{
		Nonce: "nonce", RedirectURI: "https://app.example/callback", PKCEEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, provideProof := range []bool{false, true} {
		us := &stubOIDCPKCECallback{}
		h := NewAuthHandler(&config.Config{}, us, nil, nil, nil)
		router := gin.New()
		router.GET("/auth/oidc/callback", h.OIDCRedirectCallback)
		req := httptest.NewRequest(http.MethodGet, "/auth/oidc/callback?code=code&state="+url.QueryEscape(state), nil)
		req.AddCookie(&http.Cookie{Name: oidcNonceCookieName, Value: "nonce"})
		if provideProof {
			req.AddCookie(&http.Cookie{Name: oidcPKCECookieName, Value: "proof-key"})
		}
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, req)
		if provideProof {
			if us.verifier != "proof-key" {
				t.Fatal("callback did not forward the bound verifier")
			}
			cleared := 0
			for _, cookie := range recorder.Result().Cookies() {
				if (cookie.Name == oidcNonceCookieName || cookie.Name == oidcPKCECookieName) && cookie.MaxAge < 0 {
					cleared++
				}
			}
			if cleared != 2 {
				t.Fatal("callback must consume both OIDC cookies")
			}
		} else if us.verifier != "" || recorder.Header().Get("Location") != "/#oidc_error=invalid_state" {
			t.Fatal("missing verifier cookie must not reach the login service")
		}
	}
}
