// Package webhook gives each workspace a secret URL per plugin webhook.
// Third parties call WeKnora there; the URL alone names the workspace, so
// it carries the tenant ID and a MAC that only WeKnora can compute.
package webhook

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"os"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/utils"
)

// PathPrefix is where webhooks are received:
// {prefix}/{pluginId}/{webhookId}/{token}[/...].
const PathPrefix = "/api/v1/plugin-callbacks"

// macBytes is the length of a token's MAC.
const macBytes = 16

// Tokens signs and checks webhook URLs.
type Tokens struct{ key []byte }

// NewTokens signs with key.
func NewTokens(key []byte) *Tokens { return &Tokens{key: key} }

// NewTokensFromEnv derives the key from SYSTEM_AES_KEY or JWT_SECRET, the
// same on every node. Without either, URLs change on restart.
func NewTokensFromEnv() *Tokens {
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
	_, _ = mac.Write([]byte("weknora plugin webhooks"))
	return &Tokens{key: mac.Sum(nil)}
}

func (t *Tokens) mac(pluginID, webhookID string, tenantID uint64) []byte {
	m := hmac.New(sha256.New, t.key)
	_, _ = m.Write([]byte(pluginID + "\x00" + webhookID + "\x00" + strconv.FormatUint(tenantID, 10)))
	return m.Sum(nil)[:macBytes]
}

// Token is the URL segment naming a workspace: "<tenant>.<mac>".
func (t *Tokens) Token(pluginID, webhookID string, tenantID uint64) string {
	return strconv.FormatUint(tenantID, 36) + "." +
		base64.RawURLEncoding.EncodeToString(t.mac(pluginID, webhookID, tenantID))
}

// Verify returns the workspace a token names, if it is genuine.
func (t *Tokens) Verify(pluginID, webhookID, token string) (uint64, bool) {
	tenant, sig, ok := strings.Cut(token, ".")
	if !ok {
		return 0, false
	}
	tenantID, err := strconv.ParseUint(tenant, 36, 64)
	if err != nil || tenantID == 0 {
		return 0, false
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || !hmac.Equal(got, t.mac(pluginID, webhookID, tenantID)) {
		return 0, false
	}
	return tenantID, true
}

// Path is the URL path of a webhook for a workspace.
func (t *Tokens) Path(pluginID, webhookID string, tenantID uint64) string {
	return PathPrefix + "/" + pluginID + "/" + webhookID + "/" + t.Token(pluginID, webhookID, tenantID)
}

// PublicBase is WeKnora's public address (APP_EXTERNAL_URL), without a
// trailing slash; empty when not configured.
func PublicBase() string {
	return strings.TrimSuffix(strings.TrimSpace(os.Getenv("APP_EXTERNAL_URL")), "/")
}
