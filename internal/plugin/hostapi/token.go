// Package hostapi is the Host API: how a code plugin calls back into
// WeKnora. Every call to a plugin that was granted Host API scopes carries a
// short-lived token bound to the plugin, the tenant and those scopes; the
// plugin presents it to /api/v1/plugin-host/*.
package hostapi

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/Tencent/WeKnora/internal/utils"
)

// Audience of Host API tokens; they are refused anywhere else.
const Audience = "weknora-host-api"

// Token lifetimes. A token lives as long as the call it was issued for:
// until the call's deadline plus TokenSlack, at least TokenTTL (also the
// lifetime of a call without a deadline), at most MaxTokenTTL. A data
// source sync streams for up to two hours on one call.
const (
	TokenTTL    = 5 * time.Minute
	TokenSlack  = time.Minute
	MaxTokenTTL = 6 * time.Hour
)

// Claims are what a Host API token asserts.
type Claims struct {
	PluginID string   `json:"plugin"`
	Version  string   `json:"ver"`
	TenantID uint64   `json:"tenant"`
	Scopes   []string `json:"scopes"`
	jwt.RegisteredClaims
}

// Has reports whether the token grants a scope.
func (c *Claims) Has(scope string) bool { return slices.Contains(c.Scopes, scope) }

// Issuer signs and verifies Host API tokens.
type Issuer struct {
	key []byte
	now func() time.Time
}

// NewIssuer signs with key. Nodes must share it for a token issued on one
// node to verify on another.
func NewIssuer(key []byte) *Issuer { return &Issuer{key: key, now: time.Now} }

// NewIssuerFromEnv derives the signing key from the cluster-wide secrets:
// SYSTEM_AES_KEY, else JWT_SECRET. Without either it uses a random key, which
// only works while plugins call back the node that called them (the
// embedded host always does).
func NewIssuerFromEnv() *Issuer {
	var secret []byte
	switch {
	case utils.GetAESKey() != nil:
		secret = utils.GetAESKey()
	case strings.TrimSpace(os.Getenv("JWT_SECRET")) != "":
		secret = []byte(strings.TrimSpace(os.Getenv("JWT_SECRET")))
	default:
		secret = make([]byte, 32)
		_, _ = rand.Read(secret)
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte("weknora plugin host api tokens"))
	return NewIssuer(mac.Sum(nil))
}

// Issue signs a token for one call without a deadline.
func (i *Issuer) Issue(pluginID, version string, tenantID uint64, scopes []string) (string, time.Time, error) {
	return i.IssueUntil(pluginID, version, tenantID, scopes, time.Time{})
}

// IssueUntil signs a token for one call that ends at deadline (zero: no
// deadline), so the plugin can call back for as long as the call runs.
func (i *Issuer) IssueUntil(
	pluginID, version string, tenantID uint64, scopes []string, deadline time.Time,
) (string, time.Time, error) {
	now := i.now()
	ttl := TokenTTL
	if !deadline.IsZero() {
		ttl = min(max(deadline.Sub(now)+TokenSlack, TokenTTL), MaxTokenTTL)
	}
	exp := now.Add(ttl)
	claims := Claims{
		PluginID: pluginID, Version: version, TenantID: tenantID, Scopes: scopes,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "plugin:" + pluginID,
			Audience:  jwt.ClaimStrings{Audience},
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now.Add(-30 * time.Second)),
			ExpiresAt: jwt.NewNumericDate(exp),
		},
	}
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(i.key)
	return tok, exp, err
}

// ErrInvalidToken is any token that does not verify.
var ErrInvalidToken = errors.New("invalid plugin token")

// Verify checks a token's signature, audience and lifetime.
func (i *Issuer) Verify(token string) (*Claims, error) {
	var c Claims
	parsed, err := jwt.ParseWithClaims(token, &c, func(*jwt.Token) (any, error) {
		return i.key, nil
	},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithAudience(Audience),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(i.now),
	)
	if err != nil || !parsed.Valid {
		return nil, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	if c.PluginID == "" || c.TenantID == 0 || c.Subject != "plugin:"+c.PluginID {
		return nil, fmt.Errorf("%w: incomplete claims", ErrInvalidToken)
	}
	return &c, nil
}
