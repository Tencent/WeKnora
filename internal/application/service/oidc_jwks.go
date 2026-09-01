package service

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/golang-jwt/jwt/v5"
)

// oidcAllowedSignatureAlgs is the allowlist for id_token signature
// verification. HS* is intentionally absent: a symmetric key cannot be
// validated from a public JWKS endpoint.
var oidcAllowedSignatureAlgs = []string{"RS256", "PS256", "ES256", "EdDSA"}

// oidcJWK is a single JSON Web Key as served by an IdP jwks_uri.
type oidcJWK struct {
	KTY string `json:"kty"`
	KID string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`

	// RSA
	N string `json:"n"`
	E string `json:"e"`

	// EC / OKP
	CRV string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

type oidcJWKSDocument struct {
	Keys []oidcJWK `json:"keys"`
}

// errOIDCJWKSUnavailable marks transport- or document-level JWKS failures
// (network error, non-2xx response, unparseable document, no usable keys,
// SSRF-rejected URI). Callers use it to distinguish "we could not obtain the
// IdP's keys" from "the token failed verification against the IdP's keys":
// the first can degrade with a warning, the second must fail closed.
var errOIDCJWKSUnavailable = errors.New("OIDC JWKS unavailable")

// isOIDCJWKSUnavailable reports whether the error is a JWKS-fetch failure
// rather than a per-token verification failure.
func isOIDCJWKSUnavailable(err error) bool {
	return errors.Is(err, errOIDCJWKSUnavailable)
}

// oidcJWKSCache caches the last successful JWKS fetch per URI. Identity
// providers rotate keys slowly and the fetch happens on every login, so a
// short TTL keeps logins fast without serving stale keys for too long.
type oidcJWKSCacheEntry struct {
	keys    []oidcJWK
	fetched time.Time
}

const oidcJWKSCacheTTL = 10 * time.Minute

var (
	oidcJWKSCacheMu sync.Mutex
	oidcJWKSCache   = map[string]oidcJWKSCacheEntry{}
)

// loadOIDCJWKS fetches (or serves from cache) the IdP's signing keys.
// A transport-level failure surfaces as an error so the caller can decide
// whether to fail closed or degrade; a successful fetch with no usable
// signing keys is also an error.
func loadOIDCJWKS(ctx context.Context, jwksURI string) ([]oidcJWK, error) {
	if err := validateOIDCEndpoint("jwks", jwksURI, true); err != nil {
		return nil, fmt.Errorf("%w: %v", errOIDCJWKSUnavailable, err)
	}

	oidcJWKSCacheMu.Lock()
	if entry, ok := oidcJWKSCache[jwksURI]; ok && time.Since(entry.fetched) < oidcJWKSCacheTTL {
		keys := entry.keys
		oidcJWKSCacheMu.Unlock()
		return keys, nil
	}
	oidcJWKSCacheMu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jwksURI, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to create OIDC JWKS request: %v", errOIDCJWKSUnavailable, err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := newOIDCHTTPClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to load OIDC JWKS: %v", errOIDCJWKSUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("%w: OIDC JWKS request failed: status=%d", errOIDCJWKSUnavailable, resp.StatusCode)
	}

	var doc oidcJWKSDocument
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&doc); err != nil {
		return nil, fmt.Errorf("%w: failed to decode OIDC JWKS document: %v", errOIDCJWKSUnavailable, err)
	}
	usable := make([]oidcJWK, 0, len(doc.Keys))
	for _, key := range doc.Keys {
		// Only signature keys are relevant; some providers also list
		// encryption keys in the same document.
		if key.Use != "" && key.Use != "sig" {
			continue
		}
		if _, err := key.PublicKey(); err != nil {
			continue
		}
		usable = append(usable, key)
	}
	if len(usable) == 0 {
		return nil, fmt.Errorf("%w: JWKS document contained no usable signature keys", errOIDCJWKSUnavailable)
	}

	oidcJWKSCacheMu.Lock()
	oidcJWKSCache[jwksURI] = oidcJWKSCacheEntry{keys: usable, fetched: time.Now()}
	oidcJWKSCacheMu.Unlock()
	return usable, nil
}

// PublicKey converts the JWK into a crypto public key usable by golang-jwt.
func (k oidcJWK) PublicKey() (interface{}, error) {
	switch k.KTY {
	case "RSA":
		if k.N == "" || k.E == "" {
			return nil, errors.New("RSA JWK missing n/e")
		}
		nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
		if err != nil {
			return nil, err
		}
		eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
		if err != nil {
			return nil, err
		}
		if len(eBytes) == 0 {
			return nil, errors.New("RSA JWK exponent is empty")
		}
		exp := 0
		for _, b := range eBytes {
			exp = exp<<8 | int(b)
		}
		return &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: exp}, nil
	case "EC":
		if k.CRV == "" || k.X == "" || k.Y == "" {
			return nil, errors.New("EC JWK missing crv/x/y")
		}
		xBytes, err := base64.RawURLEncoding.DecodeString(k.X)
		if err != nil {
			return nil, err
		}
		yBytes, err := base64.RawURLEncoding.DecodeString(k.Y)
		if err != nil {
			return nil, err
		}
		curve, ok := oidcECCurve(k.CRV)
		if !ok {
			return nil, fmt.Errorf("unsupported EC curve %q", k.CRV)
		}
		return &ecdsa.PublicKey{Curve: curve, X: new(big.Int).SetBytes(xBytes), Y: new(big.Int).SetBytes(yBytes)}, nil
	case "OKP":
		if k.CRV != "Ed25519" || k.X == "" {
			return nil, errors.New("OKP JWK must be Ed25519 with x present")
		}
		xBytes, err := base64.RawURLEncoding.DecodeString(k.X)
		if err != nil {
			return nil, err
		}
		if len(xBytes) != ed25519.PublicKeySize {
			return nil, errors.New("Ed25519 JWK has invalid public key length")
		}
		return ed25519.PublicKey(xBytes), nil
	default:
		return nil, fmt.Errorf("unsupported JWK kty %q", k.KTY)
	}
}

func oidcECCurve(crv string) (elliptic.Curve, bool) {
	switch crv {
	case "P-256":
		return elliptic.P256(), true
	case "P-384":
		return elliptic.P384(), true
	case "P-521":
		return elliptic.P521(), true
	default:
		return nil, false
	}
}

// verifyOIDCIDToken verifies the id_token's JWS signature against the IdP's
// JWKS, restricting to the allowlisted asymmetric algorithms and validating
// iss/aud/exp when the corresponding configuration is present.
//
// Returns an error whenever the signature cannot be proven to come from the
// IdP: wrong key, unknown kid, tampered payload or a token signed with a
// disallowed algorithm. Transport-level JWKS fetch failures also surface as
// errors; the caller decides whether to fail closed or degrade.
func verifyOIDCIDToken(ctx context.Context, cfg *config.OIDCAuthConfig, idToken string) error {
	jwksURI := strings.TrimSpace(cfg.JWKSURI)
	if jwksURI == "" {
		return errors.New("OIDC jwks_uri is not configured")
	}

	keys, err := loadOIDCJWKS(ctx, jwksURI)
	if err != nil {
		return err
	}

	opts := []jwt.ParserOption{
		jwt.WithValidMethods(oidcAllowedSignatureAlgs),
	}
	if strings.TrimSpace(cfg.IssuerURL) != "" {
		opts = append(opts, jwt.WithIssuer(cfg.IssuerURL))
	}
	if strings.TrimSpace(cfg.ClientID) != "" {
		opts = append(opts, jwt.WithAudience(cfg.ClientID))
	}

	var lastErr error
	for _, key := range keys {
		pub, err := key.PublicKey()
		if err != nil {
			continue
		}
		token, err := jwt.ParseWithClaims(
			idToken,
			&jwt.MapClaims{},
			func(_ *jwt.Token) (interface{}, error) { return pub, nil },
			opts...,
		)
		if err == nil && token.Valid {
			return nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = errors.New("no usable JWKS key")
	}
	return fmt.Errorf("OIDC id_token signature verification failed: %w", lastErr)
}

// validateIDTokenExpiry is a best-effort expiry check used when no JWKS is
// available to verify the signature. Expired tokens are rejected regardless;
// a missing exp claim is left to the verified path (golang-jwt enforces it
// there when present).
func validateIDTokenExpiry(claims map[string]interface{}) error {
	expRaw, ok := claims["exp"]
	if !ok {
		return nil
	}
	var exp float64
	switch v := expRaw.(type) {
	case float64:
		exp = v
	case json.Number:
		f, err := v.Float64()
		if err != nil {
			return nil
		}
		exp = f
	case int64:
		exp = float64(v)
	default:
		return nil
	}
	if int64(exp) <= time.Now().Unix() {
		return errors.New("OIDC id_token is expired")
	}
	return nil
}
