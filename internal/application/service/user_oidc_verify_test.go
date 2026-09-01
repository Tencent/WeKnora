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

// jwksServer serves a JWKS document exposing the public half of key under kid.
func jwksServer(t *testing.T, key *rsa.PrivateKey, kid string) *httptest.Server {
	t.Helper()
	pub := key.Public().(*rsa.PublicKey)
	eBytes := big.NewInt(int64(pub.E)).Bytes()
	doc := oidcJWKS{Keys: []oidcJWK{{
		Kty: "RSA",
		Kid: kid,
		Alg: "RS256",
		Use: "sig",
		N:   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		E:   base64.RawURLEncoding.EncodeToString(eBytes),
	}}}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(doc)
	}))
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

	cfg := &config.OIDCAuthConfig{
		JwksURI:   jwks.URL,
		IssuerURL: "https://idp.example",
		ClientID:  "weknora-client",
	}
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

	cfg := &config.OIDCAuthConfig{JwksURI: jwks.URL, IssuerURL: "https://idp.example", ClientID: "weknora-client"}

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

	cfg := &config.OIDCAuthConfig{JwksURI: jwks.URL, IssuerURL: "https://idp.example", ClientID: "weknora-client"}
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

	cfg := &config.OIDCAuthConfig{JwksURI: jwks.URL, IssuerURL: "https://idp.example", ClientID: "weknora-client"}
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

	cfg := &config.OIDCAuthConfig{JwksURI: jwks.URL, IssuerURL: "https://idp.example", ClientID: "weknora-client"}
	claims := baseClaims("https://idp.example", "weknora-client")
	claims["exp"] = time.Now().Add(-time.Hour).Unix()
	expired := signedIDToken(t, key, "kid-1", claims)

	svc := &userService{}
	if _, err := svc.verifyOIDCIDToken(context.Background(), cfg, expired); err == nil {
		t.Fatal("expired token was accepted")
	}
}
