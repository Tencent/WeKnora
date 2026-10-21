package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// storedMCPServices answers reads with a fixed stored service.
type storedMCPServices struct {
	updateCapturingMCPServices
	stored *types.MCPService
}

func (s *storedMCPServices) GetMCPServiceByID(context.Context, uint64, string) (*types.MCPService, error) {
	return s.stored, nil
}

const storedMCPURL = "https://mcp.example.com/sse"

func storedMCPService(auth types.MCPAuthType) *storedMCPServices {
	url := storedMCPURL
	return &storedMCPServices{stored: &types.MCPService{
		ID: "svc-1", TransportType: types.MCPTransportSSE, URL: &url,
		AuthConfig: &types.MCPAuthConfig{AuthType: auth, APIKey: "stored-key"},
	}}
}

func putMCPServiceAs(t *testing.T, assistant bool, svc *storedMCPServices, body string) []string {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := &MCPServiceHandler{mcpServiceService: svc}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx := context.Background()
	if assistant {
		ctx = types.WithAuditAgentAction(ctx, types.AuditAgentAction{SessionID: "s1", Operation: "mcp_service.update"})
	}
	c.Request = httptest.NewRequestWithContext(ctx, http.MethodPut, "/mcp-services/svc-1", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "id", Value: "svc-1"}}
	c.Set(types.TenantIDContextKey.String(), uint64(7))
	h.UpdateMCPService(c)
	return c.Errors.Errors()
}

func guardMove(auth types.MCPAuthType, update types.MCPService) (map[string]bool, error) {
	svc := storedMCPService(auth)
	fields := map[string]bool{}
	update.ID = "svc-1"
	err := (&MCPServiceHandler{mcpServiceService: svc}).guardAssistantEndpointMove(
		context.Background(), 7, &update, fields)
	return fields, err
}

func TestAssistantMovingAnMCPServiceDropsItsSecrets(t *testing.T) {
	moved := "https://other.example.com/sse"
	fields, err := guardMove(types.MCPAuthAPIKey, types.MCPService{URL: &moved})
	require.NoError(t, err)
	assert.True(t, fields[types.MCPUpdateDropSecrets])

	fields, err = guardMove(types.MCPAuthBearer, types.MCPService{TransportType: types.MCPTransportHTTPStreamable})
	require.NoError(t, err)
	assert.True(t, fields[types.MCPUpdateDropSecrets])

	svc := storedMCPService(types.MCPAuthAPIKey)
	require.Empty(t, putMCPServiceAs(t, true, svc, `{"transport_type":"http-streamable"}`))
	assert.True(t, svc.fields[types.MCPUpdateDropSecrets], "the handler applies the guard")
}

func TestAssistantKeepsSecretsWhenTheAddressStays(t *testing.T) {
	same := storedMCPURL
	fields, err := guardMove(types.MCPAuthAPIKey, types.MCPService{URL: &same, TransportType: types.MCPTransportSSE})
	require.NoError(t, err)
	assert.False(t, fields[types.MCPUpdateDropSecrets])

	svc := storedMCPService(types.MCPAuthAPIKey)
	require.Empty(t, putMCPServiceAs(t, true, svc, `{"name":"Docs","enabled":false}`))
	assert.False(t, svc.fields[types.MCPUpdateDropSecrets])
}

func TestAssistantCannotMoveAnOAuthMCPService(t *testing.T) {
	moved := "https://other.example.com/sse"
	_, err := guardMove(types.MCPAuthOAuth, types.MCPService{URL: &moved})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "OAuth")

	svc := storedMCPService(types.MCPAuthOAuth)
	require.NotEmpty(t, putMCPServiceAs(t, true, svc, `{"transport_type":"http-streamable"}`))
	assert.Nil(t, svc.service, "nothing was updated")
}

func TestUserMovingAnMCPServiceKeepsItsSecrets(t *testing.T) {
	svc := storedMCPService(types.MCPAuthOAuth)
	require.Empty(t, putMCPServiceAs(t, false, svc, `{"transport_type":"http-streamable"}`))
	assert.False(t, svc.fields[types.MCPUpdateDropSecrets])
}
