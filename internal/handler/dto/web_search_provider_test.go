package dto

import (
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
)

func TestWebSearchProviderResponse_OmitsSecrets(t *testing.T) {
	e := &types.WebSearchProviderEntity{
		ID:       "wsp-1",
		Name:     "bing prod",
		Provider: types.WebSearchProviderTypeBing,
		Parameters: types.WebSearchProviderParameters{
			APIKey:   "bing-secret-do-not-leak",
			EngineID: "engine-public-id",
			BaseURL:  "https://example.com",
		},
	}
	body, err := json.Marshal(NewWebSearchProviderResponse(e))
	assert.NoError(t, err)
	s := string(body)
	assert.NotContains(t, s, "bing-secret-do-not-leak")
	// Parameters sub-object must contain no secret keys.
	var raw map[string]json.RawMessage
	assert.NoError(t, json.Unmarshal(body, &raw))
	assert.NotContains(t, string(raw["parameters"]), `"api_key"`)
	// Credential metadata map exposes booleans only.
	assert.Contains(t, s, `"credentials"`)
	assert.Contains(t, s, `"api_key":{"configured":true}`)
	// Non-secret fields pass through.
	assert.Contains(t, s, "engine-public-id")
	assert.Contains(t, s, "example.com")
}

func TestWebSearchProviderResponse_NilSafe(t *testing.T) {
	assert.Nil(t, NewWebSearchProviderResponse(nil))
	assert.Equal(t, []*WebSearchProviderResponse{}, NewWebSearchProviderResponses(nil))
}
