package dto

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
)

func TestModelResponse_OmitsSecrets(t *testing.T) {
	m := &types.Model{
		ID:   "m-1",
		Name: "gpt-x",
		Parameters: types.ModelParameters{
			APIKey:    "sk-real-api-key-do-not-leak",
			AppSecret: "app-real-secret-do-not-leak",
			AppID:     "appid-public-ok-to-show",
			BaseURL:   "https://api.example.com",
			Provider:  "openai",
		},
	}
	body, err := json.Marshal(NewModelResponse(m))
	assert.NoError(t, err)
	s := string(body)
	assert.NotContains(t, s, "sk-real-api-key-do-not-leak")
	assert.NotContains(t, s, "app-real-secret-do-not-leak")
	// Parameters sub-object must contain no secret keys.
	var raw map[string]json.RawMessage
	assert.NoError(t, json.Unmarshal(body, &raw))
	params := string(raw["parameters"])
	assert.NotContains(t, params, `"api_key"`)
	assert.NotContains(t, params, `"app_secret"`)
	// Credential metadata map exposes booleans only.
	assert.Contains(t, s, `"credentials"`)
	assert.Contains(t, s, `"api_key":{"configured":true}`)
	assert.Contains(t, s, `"app_secret":{"configured":true}`)
	// Non-secret fields pass through.
	assert.Contains(t, s, "appid-public-ok-to-show")
	assert.Contains(t, s, "api.example.com")
}

func TestModelResponse_BuiltinStripsTenantConfig(t *testing.T) {
	m := &types.Model{
		ID:        "builtin-1",
		IsBuiltin: true,
		Parameters: types.ModelParameters{
			BaseURL:        "https://tenant-private.example.com",
			APIKey:         "should-not-leak",
			AppID:          "tenant-app-id",
			SupportsVision: true,
			ExtraConfig:    map[string]string{"region": "cn-hangzhou"},
		},
	}
	resp := NewModelResponse(m)
	assert.Empty(t, resp.Parameters.BaseURL,
		"builtin must not leak per-tenant base URL")
	assert.Empty(t, resp.Parameters.AppID,
		"builtin must not leak per-tenant app_id")
	assert.Nil(t, resp.Parameters.ExtraConfig,
		"builtin must not leak per-tenant extra_config")
	assert.True(t, resp.Parameters.SupportsVision,
		"capability metadata must survive (not per-tenant)")

	body, _ := json.Marshal(resp)
	assert.False(t, strings.Contains(string(body), "should-not-leak"))
	assert.False(t, strings.Contains(string(body), "tenant-private.example.com"))
}

func TestModelResponse_NilSafe(t *testing.T) {
	assert.Nil(t, NewModelResponse(nil))
	assert.Equal(t, []*ModelResponse{}, NewModelResponses(nil))
}
