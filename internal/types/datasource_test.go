package types

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDataSourceConfig_ToJSON_EncryptsStringCredentials(t *testing.T) {
	withAESKey(t, testAESKey32)
	cfg := &DataSourceConfig{
		Type: "github",
		Credentials: map[string]interface{}{
			"token":           "ghp-real-secret",
			"client_secret":   "cs-real-secret",
			"refresh_count":   42,   // non-string, must pass through untouched
			"refresh_enabled": true, // non-string, must pass through untouched
		},
		Settings: map[string]interface{}{"branch": "main"},
	}
	blob, err := cfg.ToJSON()
	assert.NoError(t, err)
	s := string(blob)

	// Plaintext secrets must not appear in the persisted JSON.
	assert.NotContains(t, s, "ghp-real-secret")
	assert.NotContains(t, s, "cs-real-secret")

	// String credentials are encrypted with the enc:v1: prefix.
	var raw map[string]json.RawMessage
	assert.NoError(t, json.Unmarshal(blob, &raw))
	var creds map[string]interface{}
	assert.NoError(t, json.Unmarshal(raw["credentials"], &creds))
	assert.True(t, strings.HasPrefix(creds["token"].(string), "enc:v1:"))
	assert.True(t, strings.HasPrefix(creds["client_secret"].(string), "enc:v1:"))

	// Non-string credentials survive verbatim (some connectors persist
	// integers / bools alongside the secrets — refresh_count and friends).
	assert.EqualValues(t, 42, creds["refresh_count"])
	assert.EqualValues(t, true, creds["refresh_enabled"])

	// In-memory caller's map must not be mutated.
	assert.Equal(t, "ghp-real-secret", cfg.Credentials["token"])
	assert.Equal(t, "cs-real-secret", cfg.Credentials["client_secret"])
}

func TestDataSourceConfig_ToJSON_PassthroughWhenNoKey(t *testing.T) {
	withAESKey(t, "")
	cfg := &DataSourceConfig{
		Credentials: map[string]interface{}{"token": "plain-token"},
	}
	blob, err := cfg.ToJSON()
	assert.NoError(t, err)
	assert.Contains(t, string(blob), "plain-token")
}

func TestDataSource_ParseConfig_Roundtrip(t *testing.T) {
	withAESKey(t, testAESKey32)
	original := &DataSourceConfig{
		Type: "notion",
		Credentials: map[string]interface{}{
			"token":   "ntn-roundtrip",
			"team_id": "team-public-id",
		},
		ResourceIDs: []string{"page-1"},
	}
	blob, err := original.ToJSON()
	assert.NoError(t, err)

	ds := &DataSource{Config: blob}
	parsed, err := ds.ParseConfig()
	assert.NoError(t, err)
	assert.Equal(t, "ntn-roundtrip", parsed.Credentials["token"])
	assert.Equal(t, "team-public-id", parsed.Credentials["team_id"])
	assert.Equal(t, []string{"page-1"}, parsed.ResourceIDs)
}

func TestDataSource_ParseConfig_LegacyPlaintext(t *testing.T) {
	// Rows persisted before encryption was wired in have no enc:v1: prefix
	// — DecryptStoredSecret returns them verbatim. Historical data must
	// keep working without an offline migration.
	withAESKey(t, testAESKey32)
	legacy := JSON([]byte(`{"type":"github","credentials":{"token":"legacy-plain"}}`))
	ds := &DataSource{Config: legacy}
	parsed, err := ds.ParseConfig()
	assert.NoError(t, err)
	assert.Equal(t, "legacy-plain", parsed.Credentials["token"])
}

func TestDataSource_ParseConfig_MissingKeyDegradesGracefully(t *testing.T) {
	// Encrypt with key, drop the key, then load. ParseConfig must succeed
	// so the data source row is still visible — the credential blanks
	// out so HasCredentials() returns false and the UI shows
	// "not configured". A loud Scan failure would break the whole list.
	withAESKey(t, testAESKey32)
	cfg := &DataSourceConfig{
		Type: "github",
		Credentials: map[string]interface{}{
			"token":         "secret",
			"refresh_count": 42, // non-string survives regardless
		},
		ResourceIDs: []string{"repo-1"},
	}
	blob, err := cfg.ToJSON()
	assert.NoError(t, err)

	withAESKey(t, "")
	ds := &DataSource{Config: blob}
	parsed, err := ds.ParseConfig()
	assert.NoError(t, err)
	assert.Equal(t, "", parsed.Credentials["token"], "encrypted credential must blank out, not leak ciphertext")
	assert.EqualValues(t, 42, parsed.Credentials["refresh_count"], "non-string credentials unaffected")
	assert.Equal(t, []string{"repo-1"}, parsed.ResourceIDs, "non-credential config survives")
	assert.False(t, parsed.HasCredentials() && parsed.Credentials["token"] != "",
		"HasCredentials should not report a usable credential when decrypt failed")
}

func TestDataSourceConfig_HasConfiguredCredentials_RSS(t *testing.T) {
	feedOnly := DataSourceConfig{
		Credentials: map[string]interface{}{
			"feed_urls": "https://example.com/feed.xml",
		},
	}
	assert.False(t, feedOnly.HasConfiguredCredentials(ConnectorTypeRSS))
	assert.True(t, feedOnly.HasCredentials())

	withAuth := DataSourceConfig{
		Credentials: map[string]interface{}{
			"feed_urls":    "https://example.com/feed.xml",
			"auth_headers": "Authorization: Bearer x",
		},
	}
	assert.True(t, withAuth.HasConfiguredCredentials(ConnectorTypeRSS))

	t.Run("strip feed_urls", func(t *testing.T) {
		cfg := feedOnly
		cfg.StripNonSecretCredentials(ConnectorTypeRSS)
		assert.Nil(t, cfg.Credentials)
		assert.False(t, cfg.HasCredentials())
	})
}

func TestDataSourceConfigStripDingTalkEndpointOverride(t *testing.T) {
	cfg := DataSourceConfig{
		Credentials: map[string]interface{}{
			"app_key":     "ding-app",
			"app_secret":  "secret",
			"operator_id": "operator",
			"base_url":    "https://attacker.example",
		},
	}

	cfg.StripNonSecretCredentials(ConnectorTypeDingTalk)

	assert.NotContains(t, cfg.Credentials, "base_url")
	assert.Equal(t, "ding-app", cfg.Credentials["app_key"])
	assert.Equal(t, "secret", cfg.Credentials["app_secret"])
	assert.Equal(t, "operator", cfg.Credentials["operator_id"])
}

// TestSubtreeChildID_MatchesPrefix locks the producer/consumer contract: a child
// ID built by SubtreeChildID must start with the parent's SubtreeChildPrefix
// (what the sweep queries), and the prefix must not match a sibling node whose
// external_id merely shares a leading substring.
func TestSubtreeChildID_MatchesPrefix(t *testing.T) {
	parent := "node123"
	prefix := SubtreeChildPrefix(parent)
	assert.Equal(t, "node123#", prefix)

	fileChild := SubtreeChildID(parent, "file", "tokABC")
	imageChild := SubtreeChildID(parent, "image", "tokXYZ")
	assert.Equal(t, "node123#file#tokABC", fileChild)
	assert.Equal(t, "node123#image#tokXYZ", imageChild)

	// Every child of this node is swept by the prefix.
	assert.True(t, strings.HasPrefix(fileChild, prefix))
	assert.True(t, strings.HasPrefix(imageChild, prefix))

	// A sibling node "node1234" (shares the "node123" leading substring but is a
	// different node) must NOT be caught by node123's prefix — the '#' guards it.
	sibling := SubtreeChildID("node1234", "file", "tokABC")
	assert.False(t, strings.HasPrefix(sibling, prefix),
		"sibling node whose id shares a leading substring must not match the prefix")
}
