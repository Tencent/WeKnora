package service

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// signRS256JWT mints an RS256-signed JWT with the given kid and claims,
// mirroring what a standard OIDC IdP produces for id_token.
func signRS256JWT(t *testing.T, key *rsa.PrivateKey, kid string, claims jwt.MapClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = kid
	signed, err := token.SignedString(key)
	require.NoError(t, err)
	return signed
}

func rsaTestKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	return key
}

func rsaJWKSBody(t *testing.T, key *rsa.PublicKey, kid string) []byte {
	t.Helper()
	n := base64.RawURLEncoding.EncodeToString(key.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())
	doc := map[string]interface{}{
		"keys": []map[string]interface{}{
			{"kty": "RSA", "kid": kid, "use": "sig", "alg": "RS256", "n": n, "e": e},
		},
	}
	body, err := json.Marshal(doc)
	require.NoError(t, err)
	return body
}

func oidcTestConfig(jwksURI, issuer, clientID string) *config.OIDCAuthConfig {
	return &config.OIDCAuthConfig{
		IssuerURL: issuer,
		ClientID:  clientID,
		JWKSURI:   jwksURI,
	}
}

func TestVerifyOIDCIDToken_ValidRS256(t *testing.T) {
	withOIDCSSRFWhitelist(t, "127.0.0.1")
	key := rsaTestKey(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(rsaJWKSBody(t, &key.PublicKey, "key-1"))
	}))
	defer srv.Close()

	cfg := oidcTestConfig(srv.URL, "https://idp.example", "client-1")
	token := signRS256JWT(t, key, "key-1", jwt.MapClaims{
		"sub": "user-1", "iss": "https://idp.example", "aud": "client-1",
		"exp": time.Now().Add(time.Hour).Unix(),
	})

	err := verifyOIDCIDToken(context.Background(), cfg, token)
	assert.NoError(t, err)
}

func TestVerifyOIDCIDToken_WrongKeyRejected(t *testing.T) {
	withOIDCSSRFWhitelist(t, "127.0.0.1")
	serverKey := rsaTestKey(t)
	attackerKey := rsaTestKey(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(rsaJWKSBody(t, &serverKey.PublicKey, "key-1"))
	}))
	defer srv.Close()

	cfg := oidcTestConfig(srv.URL, "https://idp.example", "client-1")
	// Attacker-signed token: not in the IdP's JWKS.
	token := signRS256JWT(t, attackerKey, "key-1", jwt.MapClaims{
		"sub": "victim", "iss": "https://idp.example", "aud": "client-1",
		"exp": time.Now().Add(time.Hour).Unix(),
	})

	err := verifyOIDCIDToken(context.Background(), cfg, token)
	require.Error(t, err)
	assert.False(t, isOIDCJWKSUnavailable(err), "signature failure must not degrade, got: %v", err)
	assert.Contains(t, err.Error(), "signature verification failed")
}

func TestVerifyOIDCIDToken_ExpiredRejected(t *testing.T) {
	withOIDCSSRFWhitelist(t, "127.0.0.1")
	key := rsaTestKey(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(rsaJWKSBody(t, &key.PublicKey, "key-1"))
	}))
	defer srv.Close()

	cfg := oidcTestConfig(srv.URL, "https://idp.example", "client-1")
	token := signRS256JWT(t, key, "key-1", jwt.MapClaims{
		"sub": "user-1", "iss": "https://idp.example", "aud": "client-1",
		"exp": time.Now().Add(-time.Hour).Unix(),
	})

	err := verifyOIDCIDToken(context.Background(), cfg, token)
	require.Error(t, err)
	assert.False(t, isOIDCJWKSUnavailable(err))
}

func TestVerifyOIDCIDToken_IssuerMismatchRejected(t *testing.T) {
	withOIDCSSRFWhitelist(t, "127.0.0.1")
	key := rsaTestKey(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(rsaJWKSBody(t, &key.PublicKey, "key-1"))
	}))
	defer srv.Close()

	cfg := oidcTestConfig(srv.URL, "https://real-idp.example", "client-1")
	token := signRS256JWT(t, key, "key-1", jwt.MapClaims{
		"sub": "user-1", "iss": "https://evil.example", "aud": "client-1",
		"exp": time.Now().Add(time.Hour).Unix(),
	})

	err := verifyOIDCIDToken(context.Background(), cfg, token)
	require.Error(t, err)
	assert.False(t, isOIDCJWKSUnavailable(err))
}

func TestVerifyOIDCIDToken_HS256Rejected(t *testing.T) {
	withOIDCSSRFWhitelist(t, "127.0.0.1")
	key := rsaTestKey(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(rsaJWKSBody(t, &key.PublicKey, "key-1"))
	}))
	defer srv.Close()

	cfg := oidcTestConfig(srv.URL, "https://idp.example", "client-1")
	// HS256 is outside the asymmetric allowlist and must be refused.
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "user-1", "iss": "https://idp.example", "aud": "client-1",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	signed, err := token.SignedString([]byte("shared-secret"))
	require.NoError(t, err)

	err = verifyOIDCIDToken(context.Background(), cfg, signed)
	require.Error(t, err)
	assert.False(t, isOIDCJWKSUnavailable(err))
}

func TestVerifyOIDCIDToken_JWKSUnavailableDegrades(t *testing.T) {
	withOIDCSSRFWhitelist(t, "127.0.0.1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := oidcTestConfig(srv.URL, "https://idp.example", "client-1")
	key := rsaTestKey(t)
	token := signRS256JWT(t, key, "key-1", jwt.MapClaims{
		"sub": "user-1", "exp": time.Now().Add(time.Hour).Unix(),
	})

	err := verifyOIDCIDToken(context.Background(), cfg, token)
	require.Error(t, err)
	assert.True(t, isOIDCJWKSUnavailable(err), "JWKS fetch failure must be marked unavailable, got: %v", err)
}

func TestResolveOIDCUserInfo_SignatureFailureRejectsLogin(t *testing.T) {
	withOIDCSSRFWhitelist(t, "127.0.0.1")
	serverKey := rsaTestKey(t)
	attackerKey := rsaTestKey(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(rsaJWKSBody(t, &serverKey.PublicKey, "key-1"))
	}))
	defer srv.Close()

	cfg := oidcTestConfig(srv.URL, "https://idp.example", "client-1")
	cfg.UserInfoMapping = &config.OIDCUserInfoMapping{Username: "name", Email: "email"}
	token := signRS256JWT(t, attackerKey, "key-1", jwt.MapClaims{
		"sub": "victim", "name": "Victim", "iss": "https://idp.example", "aud": "client-1",
		"exp": time.Now().Add(time.Hour).Unix(),
	})

	svc := &userService{}
	info, err := svc.resolveOIDCUserInfo(context.Background(), cfg, &oidcTokenResponse{IDToken: token})
	require.Error(t, err)
	assert.Nil(t, info)
	assert.Contains(t, err.Error(), "signature verification failed")
}

func TestResolveOIDCUserInfo_ValidSignatureAcceptsClaims(t *testing.T) {
	withOIDCSSRFWhitelist(t, "127.0.0.1")
	key := rsaTestKey(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(rsaJWKSBody(t, &key.PublicKey, "key-1"))
	}))
	defer srv.Close()

	cfg := oidcTestConfig(srv.URL, "https://idp.example", "client-1")
	cfg.UserInfoMapping = &config.OIDCUserInfoMapping{Username: "name", Email: "email"}
	token := signRS256JWT(t, key, "key-1", jwt.MapClaims{
		"sub": "user-1", "name": "Alice", "email": "alice@example.com",
		"iss": "https://idp.example", "aud": "client-1",
		"exp": time.Now().Add(time.Hour).Unix(),
	})

	svc := &userService{}
	info, err := svc.resolveOIDCUserInfo(context.Background(), cfg, &oidcTokenResponse{IDToken: token})
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, "user-1", info.Subject)
	assert.Equal(t, "Alice", info.Username)
	assert.Equal(t, "alice@example.com", info.Email)
}

func TestResolveOIDCUserInfo_NoJWKSKeepsLegacyBehavior(t *testing.T) {
	cfg := &config.OIDCAuthConfig{
		UserInfoMapping: &config.OIDCUserInfoMapping{Username: "name", Email: "email"},
	}
	// No JWKSURI: legacy unverified path still works, but expired tokens are
	// still refused.
	svc := &userService{}
	info, err := svc.resolveOIDCUserInfo(context.Background(), cfg, &oidcTokenResponse{
		IDToken: "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJ1c2VyLTEiLCJuYW1lIjoiQm9iIiwiZXhwIjo0MTAyNDQ0ODAwfQ.x", // exp = 2100
	})
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, "Bob", info.Username)
}

func TestResolveOIDCUserInfo_ExpiredRejectedWithoutJWKS(t *testing.T) {
	cfg := &config.OIDCAuthConfig{
		UserInfoMapping: &config.OIDCUserInfoMapping{Username: "name", Email: "email"},
	}
	svc := &userService{}
	info, err := svc.resolveOIDCUserInfo(context.Background(), cfg, &oidcTokenResponse{
		IDToken: "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJ1c2VyLTEiLCJuYW1lIjoiQm9iIiwiZXhwIjoxMDAwMDAwMDAwfQ.x", // exp = 2001-09-09
	})
	require.Error(t, err)
	assert.Nil(t, info)
	assert.Contains(t, err.Error(), "expired")
}

func TestResolveOIDCUserInfo_UserInfoOverridesVerifiedClaims(t *testing.T) {
	withOIDCSSRFWhitelist(t, "127.0.0.1")
	key := rsaTestKey(t)
	jwksSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(rsaJWKSBody(t, &key.PublicKey, "key-1"))
	}))
	defer jwksSrv.Close()

	userinfoSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer access-123", r.Header.Get("Authorization"))
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"sub": "user-1", "name": "FromUserInfo"})
	}))
	defer userinfoSrv.Close()

	cfg := oidcTestConfig(jwksSrv.URL, "https://idp.example", "client-1")
	cfg.UserInfoEndpoint = userinfoSrv.URL
	cfg.UserInfoMapping = &config.OIDCUserInfoMapping{Username: "name", Email: "email"}
	token := signRS256JWT(t, key, "key-1", jwt.MapClaims{
		"sub": "user-1", "name": "FromIdToken",
		"iss": "https://idp.example", "aud": "client-1",
		"exp": time.Now().Add(time.Hour).Unix(),
	})

	svc := &userService{}
	info, err := svc.resolveOIDCUserInfo(context.Background(), cfg, &oidcTokenResponse{
		IDToken:     token,
		AccessToken: "access-123",
	})
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, "FromUserInfo", info.Username)
}

func TestPopulateOIDCEndpoints_FillsJWKSAndIssuerFromDiscovery(t *testing.T) {
	withOIDCSSRFWhitelist(t, "127.0.0.1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"authorization_endpoint": serverURL(r, "/authorize"),
			"token_endpoint":         serverURL(r, "/token"),
			"userinfo_endpoint":      serverURL(r, "/userinfo"),
			"jwks_uri":               serverURL(r, "/jwks"),
			"issuer":                 "https://idp.example",
		})
	}))
	defer srv.Close()

	cfg := &config.OIDCAuthConfig{DiscoveryURL: srv.URL}
	svc := &userService{}
	err := svc.populateOIDCEndpoints(context.Background(), cfg)
	require.NoError(t, err)
	assert.Equal(t, srv.URL+"/jwks", cfg.JWKSURI)
	assert.Equal(t, "https://idp.example", cfg.IssuerURL)
}

func TestPopulateOIDCEndpoints_RejectsInternalJWKSURI(t *testing.T) {
	withOIDCSSRFWhitelist(t, "127.0.0.1")
	// Same whitelist as the token-endpoint test: the discovered jwks_uri
	// points at a cloud-metadata address, which must be rejected.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"authorization_endpoint": serverURL(r, "/authorize"),
			"token_endpoint":         serverURL(r, "/token"),
			"jwks_uri":               "http://169.254.169.254/latest/meta-data/",
		})
	}))
	defer srv.Close()

	cfg := &config.OIDCAuthConfig{DiscoveryURL: srv.URL}
	svc := &userService{}
	err := svc.populateOIDCEndpoints(context.Background(), cfg)
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "jwks"), "error should mention jwks: %v", err)
}
