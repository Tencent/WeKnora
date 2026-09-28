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

// mac covers the workspace's generation of URLs (epoch), so bumping it
// retires the URLs given out before. Generation 0 signs as URLs always did.
func (t *Tokens) mac(pluginID, webhookID string, tenantID uint64, epoch int64) []byte {
	m := hmac.New(sha256.New, t.key)
	msg := pluginID + "\x00" + webhookID + "\x00" + strconv.FormatUint(tenantID, 10)
	if epoch != 0 {
		msg += "\x00" + strconv.FormatInt(epoch, 10)
	}
	_, _ = m.Write([]byte(msg))
	return m.Sum(nil)[:macBytes]
}

// Token is the URL segment naming a workspace: "<tenant>.<mac>".
func (t *Tokens) Token(pluginID, webhookID string, tenantID uint64, epoch int64) string {
	return strconv.FormatUint(tenantID, 36) + "." +
		base64.RawURLEncoding.EncodeToString(t.mac(pluginID, webhookID, tenantID, epoch))
}

// Tenant reads the workspace a token names, before it is checked.
func Tenant(token string) (uint64, bool) {
	tenant, _, ok := strings.Cut(token, ".")
	if !ok {
		return 0, false
	}
	tenantID, err := strconv.ParseUint(tenant, 36, 64)
	return tenantID, err == nil && tenantID != 0
}

// Verify reports whether a token is the workspace's current one: epoch is
// the workspace's generation of URLs for the plugin.
func (t *Tokens) Verify(pluginID, webhookID, token string, tenantID uint64, epoch int64) bool {
	_, sig, ok := strings.Cut(token, ".")
	if !ok {
		return false
	}
	named, ok := Tenant(token)
	if !ok || named != tenantID {
		return false
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	return err == nil && hmac.Equal(got, t.mac(pluginID, webhookID, tenantID, epoch))
}

// Path is the URL path of a webhook for a workspace.
func (t *Tokens) Path(pluginID, webhookID string, tenantID uint64, epoch int64) string {
	return PathPrefix + "/" + pluginID + "/" + webhookID + "/" + t.Token(pluginID, webhookID, tenantID, epoch)
}

// PublicBase is WeKnora's public address (APP_EXTERNAL_URL), without a
// trailing slash; empty when not configured.
func PublicBase() string {
	return strings.TrimSuffix(strings.TrimSpace(os.Getenv("APP_EXTERNAL_URL")), "/")
}
