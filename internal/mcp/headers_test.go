package mcp

import (
	"context"
	"net/http"
	"testing"

	"github.com/Tencent/WeKnora/internal/mcp/headertemplate"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func templateContext(department string) context.Context {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	ctx = types.WithPrincipal(ctx, types.Principal{Type: types.PrincipalWebUser, ID: "alice"})
	return types.WithMCPHeaderContext(ctx, headertemplate.NewContext(map[string]string{
		"user.email": "alice@example.com", "tenant.id": "7", "principal.id": "alice", "principal.type": "web_user",
	}, http.Header{"X-Department-Id": {department}}))
}

func TestPreparedHeadersUseRequestValuesAndIsolateEachEffectiveContext(t *testing.T) {
	service := &types.MCPService{ID: "service", Headers: types.MCPHeaders{
		"X-Dept": "{{request.headers.X-Department-ID}}", "X-User": "{{user.email}}",
	}}
	a, err := PrepareClientConfig(templateContext("A"), service, nil)
	require.NoError(t, err)
	b, err := PrepareClientConfig(templateContext("B"), service, nil)
	require.NoError(t, err)
	require.Equal(t, "A", a.headers["X-Dept"])
	require.Equal(t, "B", b.headers["X-Dept"])
	require.NotEqual(t, a.headerScope, b.headerScope)
	require.NotContains(t, a.headerScope, "alice")
	bobCtx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	bobCtx = types.WithPrincipal(bobCtx, types.Principal{Type: types.PrincipalWebUser, ID: "bob"})
	bobCtx = types.WithMCPHeaderContext(bobCtx, headertemplate.NewContext(
		map[string]string{"user.email": "bob@example.com"}, http.Header{"X-Department-Id": {"A"}},
	))
	bob, err := PrepareClientConfig(bobCtx, service, nil)
	require.NoError(t, err)
	require.NotEqual(t, a.headerScope, bob.headerScope, "different users must not share a connection")
	again, err := PrepareClientConfig(templateContext("A"), service, nil)
	require.NoError(t, err)
	require.Equal(t, a.headerScope, again.headerScope)
	missing, err := PrepareClientConfig(templateContext(""), service, nil)
	require.NoError(t, err)
	require.NotContains(t, missing.headers, "X-Dept")
	require.NotEqual(t, a.headerScope, missing.headerScope)
	require.Equal(t, "{{request.headers.X-Department-ID}}", service.Headers["X-Dept"])
}

func TestHeaderValidationProtectsCredentialsAndTransport(t *testing.T) {
	for _, name := range []string{"Authorization", "Mcp-Session-Id", "Host", "Cookie", "X-Service-Key"} {
		service := &types.MCPService{Headers: types.MCPHeaders{name: "{{user.email}}"}, AuthConfig: &types.MCPAuthConfig{AuthType: types.MCPAuthAPIKey, APIKeyHeader: "X-Service-Key"}}
		require.Error(t, ValidateHeaderTemplates(service), name)
	}
	service := &types.MCPService{Headers: types.MCPHeaders{"AAA": "{{request.headers.X-New-Business-Field}}"}}
	require.NoError(t, ValidateHeaderTemplates(service), "new business fields do not require registration")
}

func TestEffectiveHeadersPreserveCredentialPrecedence(t *testing.T) {
	service := &types.MCPService{Headers: types.MCPHeaders{"authorization": "old", "x-dept": "old"}, AuthConfig: &types.MCPAuthConfig{
		AuthType: types.MCPAuthBearer, Token: "stored-token", CustomHeaders: map[string]string{"X-Dept": "{{request.headers.X-Department-ID}}"},
	}}
	config, err := PrepareClientConfig(templateContext("new"), service, nil)
	require.NoError(t, err)
	require.Equal(t, "Bearer stored-token", config.headers["Authorization"])
	require.Equal(t, "new", config.headers["X-Dept"])
	require.Len(t, config.headers, 2)
}

func TestMissingCustomHeaderOverrideDoesNotRestoreLowerPriorityValue(t *testing.T) {
	service := &types.MCPService{Headers: types.MCPHeaders{"X-Dept": "static"}, AuthConfig: &types.MCPAuthConfig{
		AuthType: types.MCPAuthBearer, Token: "stored-token",
		CustomHeaders: map[string]string{"x-dept": "prefix-{{request.headers.X-Department-ID}}"},
	}}
	config, err := PrepareClientConfig(templateContext(""), service, nil)
	require.NoError(t, err)
	require.NotContains(t, config.headers, "X-Dept")
	require.Equal(t, "Bearer stored-token", config.headers["Authorization"])
}
