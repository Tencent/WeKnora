package dto

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
)

// The most important guarantee of the DTO layer is structural: the serialized
// response body must NEVER contain api_key or token under any condition,
// regardless of the underlying entity's state. We assert this via the
// serialized JSON (not just struct shape) because reflection-based
// custom-marshalers anywhere downstream could otherwise reintroduce a leak.

func TestMCPServiceResponse_OmitsSecrets(t *testing.T) {
	svc := &types.MCPService{
		ID:   "svc-1",
		Name: "svc",
		AuthConfig: &types.MCPAuthConfig{
			APIKey:        "sk-real-api-key-do-not-leak",
			Token:         "tok-real-bearer-token-do-not-leak",
			CustomHeaders: map[string]string{"X-Trace": "abc"},
		},
	}
	body, err := json.Marshal(NewMCPServiceResponse(svc))
	assert.NoError(t, err)
	s := string(body)
	assert.NotContains(t, s, "sk-real-api-key-do-not-leak",
		"raw api_key must never appear in MCPServiceResponse")
	assert.NotContains(t, s, "tok-real-bearer-token-do-not-leak",
		"raw token must never appear in MCPServiceResponse")
	// auth_config no longer has api_key/token fields (only custom_headers
	// survived). Verify the auth_config sub-object contains no secret keys.
	var raw map[string]json.RawMessage
	assert.NoError(t, json.Unmarshal(body, &raw))
	if ac, ok := raw["auth_config"]; ok {
		acStr := string(ac)
		assert.NotContains(t, acStr, `"api_key"`)
		assert.NotContains(t, acStr, `"token"`)
	}
	// The new credentials map exposes "configured?" booleans by design
	// (replaces the standalone GET /credentials endpoint). Verify the
	// values are booleans, not strings.
	assert.Contains(t, s, `"credentials"`)
	assert.Contains(t, s, `"api_key":{"configured":true}`)
	assert.Contains(t, s, `"token":{"configured":true}`)
	// CustomHeaders is structural metadata and SHOULD pass through.
	assert.Contains(t, s, `"custom_headers"`)
	assert.Contains(t, s, `"X-Trace"`)
}

func TestMCPServiceResponse_BuiltinStripsTenantConfig(t *testing.T) {
	url := "https://tenant-private.example.com"
	svc := &types.MCPService{
		ID:        "builtin-1",
		IsBuiltin: true,
		URL:       &url,
		Headers:   types.MCPHeaders{"X-Tenant-Secret": "shhh"},
		AuthConfig: &types.MCPAuthConfig{
			APIKey: "should-not-leak-via-builtin",
		},
	}
	resp := NewMCPServiceResponse(svc)
	assert.Nil(t, resp.URL, "builtin must not leak per-tenant URL")
	assert.Nil(t, resp.Headers, "builtin must not leak per-tenant headers")
	assert.Nil(t, resp.AuthConfig, "builtin must not leak auth config")

	body, _ := json.Marshal(resp)
	assert.False(t, strings.Contains(string(body), "should-not-leak-via-builtin"))
	assert.False(t, strings.Contains(string(body), "X-Tenant-Secret"))
}

func TestMCPServiceResponse_NilSafe(t *testing.T) {
	assert.Nil(t, NewMCPServiceResponse(nil))
	assert.Equal(t, []*MCPServiceResponse{}, NewMCPServiceResponses(nil))
}
