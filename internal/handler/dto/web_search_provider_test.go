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
	body, err := json.Marshal(NewWebSearchProviderResponse(adminContext(), e))
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

func TestWebSearchProviderResponse_ViewerStripsIntegrationDetail(t *testing.T) {
	e := &types.WebSearchProviderEntity{
		ID: "wsp-2",
		Parameters: types.WebSearchProviderParameters{
			ProxyURL:    "http://proxy:8080",
			ExtraConfig: map[string]string{"token": "secret"},
		},
	}
	resp := NewWebSearchProviderResponse(viewerContext(), e)
	assert.Empty(t, resp.Parameters.ProxyURL)
	assert.Nil(t, resp.Parameters.ExtraConfig)
}

func TestWebSearchProviderResponse_NilSafe(t *testing.T) {
	assert.Nil(t, NewWebSearchProviderResponse(adminContext(), nil))
	assert.Equal(t, []*WebSearchProviderResponse{}, NewWebSearchProviderResponses(adminContext(), nil))
}
