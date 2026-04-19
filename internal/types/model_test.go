package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestModel_RedactSensitiveData(t *testing.T) {
	t.Run("redacts both APIKey and AppSecret when set", func(t *testing.T) {
		m := &Model{
			Parameters: ModelParameters{
				APIKey:    "real-api",
				AppSecret: "real-secret",
				BaseURL:   "https://example.com",
			},
		}
		m.RedactSensitiveData()
		assert.Equal(t, RedactedSecretPlaceholder, m.Parameters.APIKey)
		assert.Equal(t, RedactedSecretPlaceholder, m.Parameters.AppSecret)
		assert.Equal(t, "https://example.com", m.Parameters.BaseURL,
			"non-secret fields must be preserved")
	})

	t.Run("leaves empty secrets empty", func(t *testing.T) {
		m := &Model{
			Parameters: ModelParameters{BaseURL: "https://example.com"},
		}
		m.RedactSensitiveData()
		assert.Empty(t, m.Parameters.APIKey)
		assert.Empty(t, m.Parameters.AppSecret)
	})

	t.Run("redacts only set secret when one is empty", func(t *testing.T) {
		m := &Model{
			Parameters: ModelParameters{APIKey: "real-api"}, // AppSecret empty
		}
		m.RedactSensitiveData()
		assert.Equal(t, RedactedSecretPlaceholder, m.Parameters.APIKey)
		assert.Empty(t, m.Parameters.AppSecret)
	})
}

func TestModelParameters_MergeUpdate(t *testing.T) {
	existing := ModelParameters{
		APIKey:    "stored-api",
		AppSecret: "stored-secret",
		BaseURL:   "https://stored.example.com",
		Provider:  "openai",
	}

	t.Run("empty secrets preserve existing", func(t *testing.T) {
		in := ModelParameters{BaseURL: "https://new.example.com"}
		out := in.MergeUpdate(existing)
		assert.Equal(t, "stored-api", out.APIKey)
		assert.Equal(t, "stored-secret", out.AppSecret)
		assert.Equal(t, "https://new.example.com", out.BaseURL,
			"non-secret fields flow from incoming")
	})

	t.Run("redacted placeholders preserve existing", func(t *testing.T) {
		in := ModelParameters{
			APIKey:    RedactedSecretPlaceholder,
			AppSecret: RedactedSecretPlaceholder,
		}
		out := in.MergeUpdate(existing)
		assert.Equal(t, "stored-api", out.APIKey)
		assert.Equal(t, "stored-secret", out.AppSecret)
	})

	t.Run("real values replace existing", func(t *testing.T) {
		in := ModelParameters{APIKey: "new-api", AppSecret: "new-secret"}
		out := in.MergeUpdate(existing)
		assert.Equal(t, "new-api", out.APIKey)
		assert.Equal(t, "new-secret", out.AppSecret)
	})

	t.Run("mixed preserve and replace", func(t *testing.T) {
		in := ModelParameters{
			APIKey:    RedactedSecretPlaceholder, // preserve
			AppSecret: "new-secret",              // replace
		}
		out := in.MergeUpdate(existing)
		assert.Equal(t, "stored-api", out.APIKey)
		assert.Equal(t, "new-secret", out.AppSecret)
	})

	t.Run("ClearAPIKey wipes only APIKey", func(t *testing.T) {
		in := ModelParameters{ClearAPIKey: true}
		out := in.MergeUpdate(existing)
		assert.Empty(t, out.APIKey)
		assert.Equal(t, "stored-secret", out.AppSecret,
			"ClearAPIKey must not affect AppSecret")
	})

	t.Run("ClearAppSecret wipes only AppSecret", func(t *testing.T) {
		in := ModelParameters{ClearAppSecret: true}
		out := in.MergeUpdate(existing)
		assert.Equal(t, "stored-api", out.APIKey)
		assert.Empty(t, out.AppSecret)
	})

	t.Run("both clear flags wipe both secrets", func(t *testing.T) {
		in := ModelParameters{ClearAPIKey: true, ClearAppSecret: true}
		out := in.MergeUpdate(existing)
		assert.Empty(t, out.APIKey)
		assert.Empty(t, out.AppSecret)
	})

	t.Run("clear takes precedence over submitted value", func(t *testing.T) {
		in := ModelParameters{APIKey: "will-be-ignored", ClearAPIKey: true}
		out := in.MergeUpdate(existing)
		assert.Empty(t, out.APIKey)
	})

	t.Run("clear flags never persist on merged result", func(t *testing.T) {
		in := ModelParameters{ClearAPIKey: true, ClearAppSecret: true}
		out := in.MergeUpdate(existing)
		assert.False(t, out.ClearAPIKey, "write-only flag must not leak to storage")
		assert.False(t, out.ClearAppSecret, "write-only flag must not leak to storage")
	})
}
