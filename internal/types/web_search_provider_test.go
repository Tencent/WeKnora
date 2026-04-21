package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWebSearchProviderEntity_RedactSensitiveData(t *testing.T) {
	t.Run("redacts set api key", func(t *testing.T) {
		e := &WebSearchProviderEntity{
			Parameters: WebSearchProviderParameters{APIKey: "real-key", EngineID: "cse-id"},
		}
		e.RedactSensitiveData()
		assert.Equal(t, RedactedSecretPlaceholder, e.Parameters.APIKey)
		assert.Equal(t, "cse-id", e.Parameters.EngineID, "non-secret fields preserved")
	})

	t.Run("leaves empty api key empty", func(t *testing.T) {
		e := &WebSearchProviderEntity{
			Parameters: WebSearchProviderParameters{APIKey: "", EngineID: "cse-id"},
		}
		e.RedactSensitiveData()
		assert.Empty(t, e.Parameters.APIKey, "empty must stay empty to signal 'not set' to the frontend")
	})
}

func TestWebSearchProviderParameters_MergeUpdate(t *testing.T) {
	existing := WebSearchProviderParameters{
		APIKey:   "stored-key",
		EngineID: "stored-engine",
		ProxyURL: "http://stored-proxy",
	}

	t.Run("empty api key preserves existing", func(t *testing.T) {
		in := WebSearchProviderParameters{APIKey: "", EngineID: "new-engine"}
		out := in.MergeUpdate(existing)
		assert.Equal(t, "stored-key", out.APIKey)
		assert.Equal(t, "new-engine", out.EngineID, "non-secret fields flow from incoming")
	})

	t.Run("redacted placeholder preserves existing", func(t *testing.T) {
		in := WebSearchProviderParameters{APIKey: RedactedSecretPlaceholder}
		out := in.MergeUpdate(existing)
		assert.Equal(t, "stored-key", out.APIKey)
	})

	t.Run("real value replaces existing", func(t *testing.T) {
		in := WebSearchProviderParameters{APIKey: "new-key"}
		out := in.MergeUpdate(existing)
		assert.Equal(t, "new-key", out.APIKey)
	})

	t.Run("ClearAPIKey wipes stored value", func(t *testing.T) {
		in := WebSearchProviderParameters{ClearAPIKey: true}
		out := in.MergeUpdate(existing)
		assert.Empty(t, out.APIKey)
	})

	t.Run("ClearAPIKey takes precedence over submitted value", func(t *testing.T) {
		in := WebSearchProviderParameters{APIKey: "will-be-ignored", ClearAPIKey: true}
		out := in.MergeUpdate(existing)
		assert.Empty(t, out.APIKey)
	})

	t.Run("ClearAPIKey flag never persists on merged result", func(t *testing.T) {
		in := WebSearchProviderParameters{ClearAPIKey: true}
		out := in.MergeUpdate(existing)
		assert.False(t, out.ClearAPIKey, "write-only flag must not leak to storage")
	})

	t.Run("merge against empty existing", func(t *testing.T) {
		in := WebSearchProviderParameters{APIKey: RedactedSecretPlaceholder}
		out := in.MergeUpdate(WebSearchProviderParameters{})
		assert.Empty(t, out.APIKey, "placeholder against empty preserves empty")
	})
}
