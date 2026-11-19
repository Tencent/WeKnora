package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

func TestOIDCPKCEAuthorizationAndTokenExchange(t *testing.T) {
	withOIDCSSRFWhitelist(t, "127.0.0.1")
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "S256"}[enabled], func(t *testing.T) {
			var challenge, expectedVerifier string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := r.ParseForm(); err != nil {
					t.Error(err)
				}
				verifier := r.Form.Get("code_verifier")
				if enabled {
					digest := sha256.Sum256([]byte(verifier))
					if verifier != expectedVerifier || base64.RawURLEncoding.EncodeToString(digest[:]) != challenge {
						t.Error("token verifier does not match the authorization challenge")
					}
				} else if _, exists := r.Form["code_verifier"]; exists {
					t.Error("legacy exchange unexpectedly sends code_verifier")
				}
				_, _ = w.Write([]byte(`{"access_token":"test-access-token","token_type":"Bearer"}`))
			}))
			defer server.Close()
			cfg := &config.OIDCAuthConfig{
				Enable: true, PKCEEnabled: enabled,
				ClientID: "client-id", ClientSecret: "client-secret",
				AuthorizationEndpoint: server.URL + "/authorize", TokenEndpoint: server.URL + "/token",
				Scopes: []string{"openid", "profile", "email"},
			}
			svc := &userService{config: &config.Config{OIDCAuth: cfg}}
			response, err := svc.GetOIDCAuthorizationURL(context.Background(), "https://app.example/callback")
			if err != nil {
				t.Fatal(err)
			}
			authorizationURL, err := url.Parse(response.AuthorizationURL)
			if err != nil {
				t.Fatal(err)
			}
			query := authorizationURL.Query()
			state, err := secutils.VerifyOIDCState(query.Get("state"))
			if err != nil || state.PKCEEnabled != enabled {
				t.Fatalf("signed PKCE state mismatch: %v", err)
			}
			challenge = query.Get("code_challenge")
			expectedVerifier = response.PKCEVerifier
			if enabled {
				if len(expectedVerifier) != 43 || query.Get("code_challenge_method") != "S256" {
					t.Fatal("expected a 32-byte random verifier and S256")
				}
				if strings.Contains(response.AuthorizationURL, expectedVerifier) {
					t.Fatal("verifier leaked into authorization URL")
				}
				rawState, err := base64.RawURLEncoding.DecodeString(strings.Split(response.State, ".")[0])
				if err != nil || strings.Contains(string(rawState), expectedVerifier) {
					t.Fatal("verifier must not be stored in signed-but-readable state")
				}
				encoded, err := json.Marshal(response)
				if err != nil || strings.Contains(string(encoded), expectedVerifier) {
					t.Fatal("verifier must not be returned in JSON")
				}
			} else if expectedVerifier != "" || challenge != "" || query.Get("code_challenge_method") != "" {
				t.Fatal("PKCE must be opt-in")
			}
			_, err = svc.exchangeOIDCCode(context.Background(), cfg, "code", state.RedirectURI, expectedVerifier)
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestOIDCPKCERejectsMissingVerifierBeforeTokenRequest(t *testing.T) {
	withOIDCSSRFWhitelist(t, "127.0.0.1")
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests++ }))
	defer server.Close()
	svc := &userService{}
	cfg := &config.OIDCAuthConfig{PKCEEnabled: true, TokenEndpoint: server.URL}
	_, err := svc.exchangeOIDCCode(context.Background(), cfg, "code", "https://app.example/callback", "")
	if err == nil || requests != 0 {
		t.Fatal("missing verifier must fail before contacting the token endpoint")
	}
}
