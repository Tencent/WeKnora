package types

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDataSourceConfig_Redacted(t *testing.T) {
	t.Run("redacts all string values in Credentials", func(t *testing.T) {
		cfg := DataSourceConfig{
			Type: "github",
			Credentials: map[string]interface{}{
				"access_token": "real-pat",
				"installation_id": "real-id",
			},
			ResourceIDs: []string{"repo-1"},
		}
		out := cfg.Redacted()
		assert.Equal(t, RedactedSecretPlaceholder, out.Credentials["access_token"])
		assert.Equal(t, RedactedSecretPlaceholder, out.Credentials["installation_id"])
		assert.Equal(t, "github", out.Type, "non-credential fields preserved")
		assert.Equal(t, []string{"repo-1"}, out.ResourceIDs)
	})

	t.Run("leaves empty string credentials empty", func(t *testing.T) {
		cfg := DataSourceConfig{
			Credentials: map[string]interface{}{"token": ""},
		}
		out := cfg.Redacted()
		assert.Equal(t, "", out.Credentials["token"],
			"empty must stay empty to signal 'not set' to the frontend")
	})

	t.Run("non-string credential values pass through unchanged", func(t *testing.T) {
		cfg := DataSourceConfig{
			Credentials: map[string]interface{}{
				"token":          "secret",
				"installation_id": float64(12345),
				"metadata":       map[string]interface{}{"nested": "value"},
			},
		}
		out := cfg.Redacted()
		assert.Equal(t, RedactedSecretPlaceholder, out.Credentials["token"])
		assert.Equal(t, float64(12345), out.Credentials["installation_id"])
		assert.NotNil(t, out.Credentials["metadata"])
	})

	t.Run("does not mutate original", func(t *testing.T) {
		cfg := DataSourceConfig{
			Credentials: map[string]interface{}{"token": "secret"},
		}
		_ = cfg.Redacted()
		assert.Equal(t, "secret", cfg.Credentials["token"])
	})

	t.Run("nil credentials map stays nil", func(t *testing.T) {
		cfg := DataSourceConfig{Type: "github"}
		out := cfg.Redacted()
		assert.Nil(t, out.Credentials)
	})

	t.Run("ClearCredentials flag stripped from response", func(t *testing.T) {
		cfg := DataSourceConfig{ClearCredentials: true, Type: "github"}
		out := cfg.Redacted()
		assert.False(t, out.ClearCredentials, "write-only flag must not echo back")
	})
}

func TestDataSourceConfig_MergeUpdate(t *testing.T) {
	existing := DataSourceConfig{
		Type: "github",
		Credentials: map[string]interface{}{
			"access_token":   "stored-pat",
			"installation_id": "stored-id",
		},
		ResourceIDs: []string{"old-repo"},
	}

	t.Run("empty credential value preserves existing", func(t *testing.T) {
		in := DataSourceConfig{
			Type: "github",
			Credentials: map[string]interface{}{
				"access_token": "",
			},
		}
		out := in.MergeUpdate(existing)
		assert.Equal(t, "stored-pat", out.Credentials["access_token"])
		assert.Equal(t, "stored-id", out.Credentials["installation_id"],
			"keys not present in incoming carry over from existing")
	})

	t.Run("redacted placeholder preserves existing", func(t *testing.T) {
		in := DataSourceConfig{
			Credentials: map[string]interface{}{
				"access_token": RedactedSecretPlaceholder,
			},
		}
		out := in.MergeUpdate(existing)
		assert.Equal(t, "stored-pat", out.Credentials["access_token"])
	})

	t.Run("real value replaces existing", func(t *testing.T) {
		in := DataSourceConfig{
			Credentials: map[string]interface{}{
				"access_token": "new-pat",
			},
		}
		out := in.MergeUpdate(existing)
		assert.Equal(t, "new-pat", out.Credentials["access_token"])
		assert.Equal(t, "stored-id", out.Credentials["installation_id"],
			"other keys preserved from existing")
	})

	t.Run("mixed preserve and replace", func(t *testing.T) {
		in := DataSourceConfig{
			Credentials: map[string]interface{}{
				"access_token":   RedactedSecretPlaceholder, // preserve
				"installation_id": "new-id",                 // replace
			},
		}
		out := in.MergeUpdate(existing)
		assert.Equal(t, "stored-pat", out.Credentials["access_token"])
		assert.Equal(t, "new-id", out.Credentials["installation_id"])
	})

	t.Run("ClearCredentials wipes entire map", func(t *testing.T) {
		in := DataSourceConfig{ClearCredentials: true}
		out := in.MergeUpdate(existing)
		assert.Nil(t, out.Credentials,
			"ClearCredentials removes all credential keys atomically")
	})

	t.Run("ClearCredentials takes precedence over submitted values", func(t *testing.T) {
		in := DataSourceConfig{
			ClearCredentials: true,
			Credentials: map[string]interface{}{
				"access_token": "will-be-ignored",
			},
		}
		out := in.MergeUpdate(existing)
		assert.Nil(t, out.Credentials)
	})

	t.Run("ClearCredentials flag never persists on merged result", func(t *testing.T) {
		in := DataSourceConfig{ClearCredentials: true}
		out := in.MergeUpdate(existing)
		assert.False(t, out.ClearCredentials, "write-only flag must not leak to storage")
	})

	t.Run("non-string incoming values always overwrite", func(t *testing.T) {
		in := DataSourceConfig{
			Credentials: map[string]interface{}{
				"installation_id": float64(999),
			},
		}
		out := in.MergeUpdate(existing)
		assert.Equal(t, float64(999), out.Credentials["installation_id"],
			"non-string values are never treated as redacted/empty")
	})

	t.Run("non-credential fields flow from incoming", func(t *testing.T) {
		in := DataSourceConfig{
			Type:        "notion",
			ResourceIDs: []string{"new-repo"},
		}
		out := in.MergeUpdate(existing)
		assert.Equal(t, "notion", out.Type)
		assert.Equal(t, []string{"new-repo"}, out.ResourceIDs)
	})
}

func TestDataSource_RedactSensitiveData(t *testing.T) {
	t.Run("redacts string credentials in Config jsonb", func(t *testing.T) {
		cfg := DataSourceConfig{
			Type: "github",
			Credentials: map[string]interface{}{
				"access_token": "real-pat",
			},
		}
		blob, err := json.Marshal(cfg)
		require.NoError(t, err)

		ds := &DataSource{Config: JSON(blob)}
		ds.RedactSensitiveData()

		parsed, err := ds.ParseConfig()
		require.NoError(t, err)
		assert.Equal(t, RedactedSecretPlaceholder, parsed.Credentials["access_token"])
	})

	t.Run("no-op on empty config", func(t *testing.T) {
		ds := &DataSource{Config: nil}
		ds.RedactSensitiveData() // must not panic
		assert.Nil(t, ds.Config)
	})

	t.Run("no-op on malformed config", func(t *testing.T) {
		ds := &DataSource{Config: JSON(`not valid json`)}
		ds.RedactSensitiveData() // must not panic
		// Config remains unchanged — better than dropping the response.
		assert.Equal(t, JSON(`not valid json`), ds.Config)
	})
}
