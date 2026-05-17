package types

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// 32-byte AES-256 key used across encryption round-trip tests.
const testAESKey32 = "0123456789abcdef0123456789abcdef"

func withAESKey(t *testing.T, key string) {
	t.Helper()
	prev := os.Getenv("SYSTEM_AES_KEY")
	t.Setenv("SYSTEM_AES_KEY", key)
	t.Cleanup(func() { _ = os.Setenv("SYSTEM_AES_KEY", prev) })
}

func TestMCPAuthConfig_Value_EncryptsWhenKeySet(t *testing.T) {
	withAESKey(t, testAESKey32)
	cfg := &MCPAuthConfig{
		APIKey: "sk-real-secret",
		Token:  "bearer-real-secret",
	}
	v, err := cfg.Value()
	assert.NoError(t, err)
	raw, ok := v.([]byte)
	assert.True(t, ok)

	// The persisted bytes must be ciphertext, never the plaintext value.
	assert.NotContains(t, string(raw), "sk-real-secret")
	assert.NotContains(t, string(raw), "bearer-real-secret")

	var stored MCPAuthConfig
	assert.NoError(t, json.Unmarshal(raw, &stored))
	assert.True(t, strings.HasPrefix(stored.APIKey, "enc:v1:"))
	assert.True(t, strings.HasPrefix(stored.Token, "enc:v1:"))

	// In-memory caller's struct must not be mutated — encryption operates
	// on a local copy. Otherwise the next read of cfg.APIKey would surface
	// ciphertext as if it were the API key.
	assert.Equal(t, "sk-real-secret", cfg.APIKey)
	assert.Equal(t, "bearer-real-secret", cfg.Token)
}

func TestMCPAuthConfig_Value_PassthroughWhenNoKey(t *testing.T) {
	withAESKey(t, "")
	cfg := &MCPAuthConfig{APIKey: "plain-key", Token: "plain-tok"}
	v, err := cfg.Value()
	assert.NoError(t, err)
	raw := v.([]byte)
	// Without SYSTEM_AES_KEY we fall back to plaintext storage (matches
	// the existing Model/WebSearch convention so deployments without a
	// key don't break).
	assert.Contains(t, string(raw), "plain-key")
	assert.Contains(t, string(raw), "plain-tok")
}

func TestMCPAuthConfig_ScanRoundtrip(t *testing.T) {
	withAESKey(t, testAESKey32)
	original := &MCPAuthConfig{
		APIKey:        "sk-roundtrip",
		Token:         "tok-roundtrip",
		CustomHeaders: map[string]string{"X-Trace": "abc"},
	}
	v, err := original.Value()
	assert.NoError(t, err)

	var scanned MCPAuthConfig
	assert.NoError(t, scanned.Scan(v.([]byte)))
	assert.Equal(t, "sk-roundtrip", scanned.APIKey)
	assert.Equal(t, "tok-roundtrip", scanned.Token)
	assert.Equal(t, "abc", scanned.CustomHeaders["X-Trace"])
}

func TestMCPAuthConfig_Scan_LegacyPlaintextPassesThrough(t *testing.T) {
	// Rows written before encryption was enabled have no enc:v1: prefix —
	// DecryptStoredSecret must return them unchanged so historical data
	// continues to work without a migration.
	withAESKey(t, testAESKey32)
	legacy := []byte(`{"api_key":"legacy-plain","token":"legacy-plain-tok"}`)
	var scanned MCPAuthConfig
	assert.NoError(t, scanned.Scan(legacy))
	assert.Equal(t, "legacy-plain", scanned.APIKey)
	assert.Equal(t, "legacy-plain-tok", scanned.Token)
}

func TestMCPAuthConfig_Scan_MissingKeyDegradesGracefully(t *testing.T) {
	// Encrypt with key set, drop the key, then try to load. The row must
	// still Scan successfully (otherwise a single broken row would crash
	// list endpoints and hide every MCP service from the operator). The
	// secret blanks out so the UI shows "credential not configured" and
	// no ciphertext is ever sent upstream as the API key.
	withAESKey(t, testAESKey32)
	v, err := (&MCPAuthConfig{APIKey: "x", Token: "y", CustomHeaders: map[string]string{"a": "b"}}).Value()
	assert.NoError(t, err)

	withAESKey(t, "")
	var scanned MCPAuthConfig
	assert.NoError(t, scanned.Scan(v.([]byte)))
	assert.Empty(t, scanned.APIKey, "encrypted api_key must blank out when key is missing, not leak ciphertext")
	assert.Empty(t, scanned.Token, "encrypted token must blank out when key is missing")
	assert.Equal(t, "b", scanned.CustomHeaders["a"], "non-secret fields must still load")
}
