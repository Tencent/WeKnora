package dto

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
)

// TestMCPServiceResponse_BuiltinKeepsAuthType pins the OAuth UX contract
// from #5618: a builtin service must still expose auth_type and scopes so
// the settings page can render the Authorize button, while every secret
// and every transport detail stays stripped.
func TestMCPServiceResponse_BuiltinKeepsAuthType(t *testing.T) {
	url := "https://tenant-private.example.com"
	svc := &types.MCPService{
		ID:        "builtin-oauth",
		IsBuiltin: true,
		URL:       &url,
		Headers:   types.MCPHeaders{"X-Tenant-Secret": "shhh"},
		AuthConfig: &types.MCPAuthConfig{
			AuthType:     types.MCPAuthOAuth,
			Scopes:       []string{"read", "write"},
			ClientID:     "cid-public",
			ClientSecret: "cs-secret-do-not-leak",
			APIKey:       "should-not-leak-via-builtin",
		},
	}

	resp := NewMCPServiceResponse(adminContext(), svc)

	// Secrets and transport details stay stripped.
	assert.Nil(t, resp.URL, "builtin must not leak per-tenant URL")
	assert.Nil(t, resp.Headers, "builtin must not leak per-tenant headers")
	assert.Nil(t, resp.StdioConfig)
	assert.Nil(t, resp.EnvVars)
	assert.Nil(t, resp.Credentials, "builtin rows carry no per-tenant credentials map")

	// OAuth UX contract: auth_type and scopes survive the strip.
	require := assert.New(t)
	require.NotNil(t, resp.AuthConfig, "builtin OAuth rows must keep auth_config for the settings dialog")
	require.Equal(types.MCPAuthOAuth, resp.AuthConfig.AuthType)
	require.Equal([]string{"read", "write"}, resp.AuthConfig.Scopes)

	// ...but nothing secret rides along.
	body, err := json.Marshal(resp)
	require.NoError(err)
	s := string(body)
	assert.False(t, strings.Contains(s, "should-not-leak-via-builtin"))
	assert.False(t, strings.Contains(s, "cs-secret-do-not-leak"))
	assert.False(t, strings.Contains(s, "X-Tenant-Secret"))
	assert.True(t, strings.Contains(s, "cid-public"), "pre-registered client_id is public OAuth config")
}
