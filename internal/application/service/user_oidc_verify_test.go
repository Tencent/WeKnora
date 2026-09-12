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
)

// signedIDToken builds an RS256-signed id_token with the given claims and kid.
func signedIDToken(t *testing.T, key *rsa.PrivateKey, kid string, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = kid
	s, err := tok.SignedString(key)
	if err != nil {
		t.Fatalf("sign id_token: %v", err)
	}
	return s
}

func rsaJWK(key *rsa.PrivateKey, kid string) oidcJWK {
	pub := key.Public().(*rsa.PublicKey)
	return oidcJWK{
		Kty: "RSA",
		Kid: kid,
		Alg: "RS256",
		Use: "sig",
		N:   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		E:   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}
}

func jwksServerFromKeys(t *testing.T, keys ...oidcJWK) *httptest.Server {
	t.Helper()
	doc := oidcJWKS{Keys: keys}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(doc)
	}))
}

// jwksServer serves a JWKS document exposing the public half of key under kid.
func jwksServer(t *testing.T, key *rsa.PrivateKey, kid string) *httptest.Server {
	t.Helper()
	return jwksServerFromKeys(t, rsaJWK(key, kid))
}

func oidcVerifyCfg(jwksURL string) *config.OIDCAuthConfig {
	return &config.OIDCAuthConfig{
		JwksURI:   jwksURL,
		IssuerURL: "https://idp.example",
		ClientID:  "weknora-client",
		UserInfoMapping: &config.OIDCUserInfoMapping{
			Username: "name",
			Email:    "email",
		},
	}
}

func unsignedJWT(claims jwt.MapClaims) string {
	header, _ := json.Marshal(map[string]string{"alg": "none", "typ": "JWT"})
	payload, _ := json.Marshal(claims)
	return base64.RawURLEncoding.EncodeToString(header) + "." +
		base64.RawURLEncoding.EncodeToString(payload) + "."
}

func baseClaims(iss, aud string) jwt.MapClaims {
	return jwt.MapClaims{
		"iss":   iss,
		"aud":   aud,
		"sub":   "user-123",
		"email": "user@example.com",
		"name":  "Real User",
		"exp":   time.Now().Add(time.Hour).Unix(),
		"iat":   time.Now().Unix(),
	}
}

// A validly signed id_token with the right issuer/audience is accepted and its
// claims are returned.
func TestVerifyOIDCIDToken_ValidTokenAccepted(t *testing.T) {
	withOIDCSSRFWhitelist(t, "127.0.0.1")

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	jwks := jwksServer(t, key, "kid-1")
	defer jwks.Close()

	cfg := oidcVerifyCfg(jwks.URL)
	idToken := signedIDToken(t, key, "kid-1", baseClaims("https://idp.example", "weknora-client"))

	svc := &userService{}
	claims, err := svc.verifyOIDCIDToken(context.Background(), cfg, idToken)
	if err != nil {
		t.Fatalf("valid token rejected: %v", err)
	}
	if got, _ := claims["email"].(string); got != "user@example.com" {
		t.Fatalf("email claim = %q, want user@example.com", got)
	}
}

// A token whose payload has been tampered with (re-signed body but signature
// from the original) must be rejected. This is the core of the vulnerability:
// previously decodeJWTClaims would have trusted the forged claims.
func TestVerifyOIDCIDToken_TamperedPayloadRejected(t *testing.T) {
	withOIDCSSRFWhitelist(t, "127.0.0.1")

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	jwks := jwksServer(t, key, "kid-1")
	defer jwks.Close()

	cfg := oidcVerifyCfg(jwks.URL)

	valid := signedIDToken(t, key, "kid-1", baseClaims("https://idp.example", "weknora-client"))

	// Swap the payload for a forged one (admin@example.com) while keeping the
	// original header + signature.
	parts := strings.Split(valid, ".")
	forgedPayload := baseClaims("https://idp.example", "weknora-client")
	forgedPayload["email"] = "admin@example.com"
	forgedPayload["sub"] = "attacker"
	pb, _ := json.Marshal(forgedPayload)
	parts[1] = base64.RawURLEncoding.EncodeToString(pb)
	forged := strings.Join(parts, ".")

	svc := &userService{}
	if _, err := svc.verifyOIDCIDToken(context.Background(), cfg, forged); err == nil {
		t.Fatal("forged id_token was accepted; signature not verified")
	}
}

// A token signed by a different (attacker) key must be rejected even though it
// is a syntactically valid, self-consistent JWT.
func TestVerifyOIDCIDToken_WrongSignerRejected(t *testing.T) {
	withOIDCSSRFWhitelist(t, "127.0.0.1")

	idpKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	attackerKey, _ := rsa.GenerateKey(rand.Reader, 2048)

	jwks := jwksServer(t, idpKey, "kid-1") // JWKS only advertises the IdP key
	defer jwks.Close()

	cfg := oidcVerifyCfg(jwks.URL)
	// Attacker signs with their own key but claims kid-1.
	forged := signedIDToken(t, attackerKey, "kid-1", baseClaims("https://idp.example", "weknora-client"))

	svc := &userService{}
	if _, err := svc.verifyOIDCIDToken(context.Background(), cfg, forged); err == nil {
		t.Fatal("token signed by attacker key was accepted")
	}
}

// Wrong audience (token minted for a different client) is rejected.
func TestVerifyOIDCIDToken_WrongAudienceRejected(t *testing.T) {
	withOIDCSSRFWhitelist(t, "127.0.0.1")

	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	jwks := jwksServer(t, key, "kid-1")
	defer jwks.Close()

	cfg := oidcVerifyCfg(jwks.URL)
	other := signedIDToken(t, key, "kid-1", baseClaims("https://idp.example", "some-other-client"))

	svc := &userService{}
	if _, err := svc.verifyOIDCIDToken(context.Background(), cfg, other); err == nil {
		t.Fatal("token with wrong audience was accepted")
	}
}

// An expired token is rejected.
func TestVerifyOIDCIDToken_ExpiredRejected(t *testing.T) {
	withOIDCSSRFWhitelist(t, "127.0.0.1")

	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	jwks := jwksServer(t, key, "kid-1")
	defer jwks.Close()

	cfg := oidcVerifyCfg(jwks.URL)
	claims := baseClaims("https://idp.example", "weknora-client")
	claims["exp"] = time.Now().Add(-time.Hour).Unix()
	expired := signedIDToken(t, key, "kid-1", claims)

	svc := &userService{}
	if _, err := svc.verifyOIDCIDToken(context.Background(), cfg, expired); err == nil {
		t.Fatal("expired token was accepted")
	}
}

func TestVerifyOIDCIDToken_MissingExpRejected(t *testing.T) {
	withOIDCSSRFWhitelist(t, "127.0.0.1")

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	jwks := jwksServer(t, key, "kid-1")
	defer jwks.Close()

	cfg := oidcVerifyCfg(jwks.URL)
	claims := baseClaims("https://idp.example", "weknora-client")
	delete(claims, "exp")
	token := signedIDToken(t, key, "kid-1", claims)

	svc := &userService{}
	if _, err := svc.verifyOIDCIDToken(context.Background(), cfg, token); err == nil {
		t.Fatal("token without exp was accepted")
	}
}

func TestVerifyOIDCIDToken_MissingSubRejected(t *testing.T) {
	withOIDCSSRFWhitelist(t, "127.0.0.1")

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	jwks := jwksServer(t, key, "kid-1")
	defer jwks.Close()

	cfg := oidcVerifyCfg(jwks.URL)
	claims := baseClaims("https://idp.example", "weknora-client")
	delete(claims, "sub")
	token := signedIDToken(t, key, "kid-1", claims)

	svc := &userService{}
	if _, err := svc.verifyOIDCIDToken(context.Background(), cfg, token); err == nil {
		t.Fatal("token without sub was accepted")
	}
}

func TestVerifyOIDCIDToken_MissingIssuerRejected(t *testing.T) {
	withOIDCSSRFWhitelist(t, "127.0.0.1")

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	jwks := jwksServer(t, key, "kid-1")
	defer jwks.Close()

	cfg := oidcVerifyCfg(jwks.URL)
	cfg.IssuerURL = ""
	token := signedIDToken(t, key, "kid-1", baseClaims("https://idp.example", "weknora-client"))

	svc := &userService{}
	if _, err := svc.verifyOIDCIDToken(context.Background(), cfg, token); err == nil {
		t.Fatal("token accepted without configured issuer")
	}
}

func TestVerifyOIDCIDToken_EmptyKidDoesNotStealMatchingKey(t *testing.T) {
	withOIDCSSRFWhitelist(t, "127.0.0.1")

	decoyKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	idpKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	jwks := jwksServerFromKeys(t, rsaJWK(decoyKey, ""), rsaJWK(idpKey, "kid-1"))
	defer jwks.Close()

	cfg := oidcVerifyCfg(jwks.URL)
	token := signedIDToken(t, idpKey, "kid-1", baseClaims("https://idp.example", "weknora-client"))

	svc := &userService{}
	if _, err := svc.verifyOIDCIDToken(context.Background(), cfg, token); err != nil {
		t.Fatalf("valid kid-1 token rejected because empty-kid decoy was tried first: %v", err)
	}
}

func TestVerifyOIDCIDToken_MultipleKeysRequireKid(t *testing.T) {
	withOIDCSSRFWhitelist(t, "127.0.0.1")

	keyA, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	keyB, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	jwks := jwksServerFromKeys(t, rsaJWK(keyA, "kid-a"), rsaJWK(keyB, "kid-b"))
	defer jwks.Close()

	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, baseClaims("https://idp.example", "weknora-client"))
	token, err := tok.SignedString(keyA)
	if err != nil {
		t.Fatal(err)
	}

	svc := &userService{}
	if _, err := svc.verifyOIDCIDToken(context.Background(), oidcVerifyCfg(jwks.URL), token); err == nil {
		t.Fatal("token without kid was accepted against a multi-key JWKS")
	}
}

func TestResolveOIDCUserInfo_NoJwksDoesNotTrustUnsignedIDToken(t *testing.T) {
	withOIDCSSRFWhitelist(t, "127.0.0.1")

	userinfo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"sub":   "real-user",
			"email": "real@example.com",
			"name":  "Real User",
		})
	}))
	defer userinfo.Close()

	cfg := oidcVerifyCfg("")
	cfg.JwksURI = ""
	cfg.UserInfoEndpoint = userinfo.URL
	forged := unsignedJWT(jwt.MapClaims{
		"sub":   "attacker",
		"email": "admin@example.com",
		"name":  "Admin",
	})

	svc := &userService{}
	info, err := svc.resolveOIDCUserInfo(context.Background(), cfg, &oidcTokenResponse{
		IDToken:     forged,
		AccessToken: "access-token",
	})
	if err != nil {
		t.Fatalf("resolveOIDCUserInfo: %v", err)
	}
	if info.Email != "real@example.com" {
		t.Fatalf("email = %q, want userinfo email (not unsigned id_token)", info.Email)
	}
	if info.Subject != "real-user" {
		t.Fatalf("subject = %q, want userinfo sub", info.Subject)
	}
}

func TestResolveOIDCUserInfo_UserinfoFailureDoesNotFallBackToUnsignedToken(t *testing.T) {
	withOIDCSSRFWhitelist(t, "127.0.0.1")

	userinfo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer userinfo.Close()

	cfg := oidcVerifyCfg("")
	cfg.JwksURI = ""
	cfg.UserInfoEndpoint = userinfo.URL
	forged := unsignedJWT(baseClaims("https://idp.example", "weknora-client"))

	svc := &userService{}
	_, err := svc.resolveOIDCUserInfo(context.Background(), cfg, &oidcTokenResponse{
		IDToken:     forged,
		AccessToken: "access-token",
	})
	if err == nil {
		t.Fatal("expected error when userinfo fails and id_token is unverified")
	}
}

func TestResolveOIDCUserInfo_VerifiedIDTokenUsedWhenUserinfoFails(t *testing.T) {
	withOIDCSSRFWhitelist(t, "127.0.0.1")

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	jwks := jwksServer(t, key, "kid-1")
	defer jwks.Close()

	userinfo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer userinfo.Close()

	cfg := oidcVerifyCfg(jwks.URL)
	cfg.UserInfoEndpoint = userinfo.URL
	idToken := signedIDToken(t, key, "kid-1", baseClaims("https://idp.example", "weknora-client"))

	svc := &userService{}
	info, err := svc.resolveOIDCUserInfo(context.Background(), cfg, &oidcTokenResponse{
		IDToken:     idToken,
		AccessToken: "access-token",
	})
	if err != nil {
		t.Fatalf("resolveOIDCUserInfo: %v", err)
	}
	if info.Email != "user@example.com" {
		t.Fatalf("email = %q, want verified id_token email", info.Email)
	}
}
