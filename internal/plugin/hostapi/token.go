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

// TokenTTL is how long a token lives: one call and its follow-ups.
const TokenTTL = 5 * time.Minute

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

// Issue signs a token for one call.
func (i *Issuer) Issue(pluginID, version string, tenantID uint64, scopes []string) (string, time.Time, error) {
	now := i.now()
	exp := now.Add(TokenTTL)
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
