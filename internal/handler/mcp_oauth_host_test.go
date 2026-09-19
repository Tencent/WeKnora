package handler

import (
	"bytes"
	"context"
	stderrors "errors"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// The request Host is what the OAuth callback validator falls back to when
// APP_EXTERNAL_URL is unset — the default deployment. This test pins the
// wiring in AuthorizeURL: the Host must actually reach the validator, or every
// authorize request in a default deployment is rejected. It observes the wiring
// from the far side of validation: a matching redirect_uri must reach the MCP
// service lookup, a mismatching one must not.
type mcpOAuthHostStubService struct {
	interfaces.MCPServiceService
	lookups int
}

func (s *mcpOAuthHostStubService) GetMCPServiceByID(
	context.Context, uint64, string,
) (*types.MCPService, error) {
	s.lookups++
	return nil, stderrors.New("stub: service lookup reached")
}

func runMCPOAuthAuthorizeURL(
	t *testing.T, svc *mcpOAuthHostStubService, host, redirectURI string,
) (*httptest.ResponseRecorder, *gin.Context) {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "id", Value: "svc-1"}}
	c.Set(types.TenantIDContextKey.String(), uint64(1))
	req := httptest.NewRequest("POST", "/api/v1/mcp-services/svc-1/oauth/authorize-url",
		bytes.NewReader([]byte(`{"redirect_uri":"`+redirectURI+`"}`)))
	req.Host = host
	req.Header.Set("Content-Type", "application/json")
	c.Request = req.WithContext(types.WithPrincipal(req.Context(), types.Principal{
		Type: types.PrincipalWebUser, ID: "user-1",
	}))

	handler := &MCPOAuthHandler{svc: svc}
	handler.AuthorizeURL(c)
	return recorder, c
}

func TestMCPOAuthAuthorizeURLPassesRequestHostToRedirectValidator(t *testing.T) {
	// Default deployment: APP_EXTERNAL_URL unset, nginx forwards the external
	// Host, and the frontend builds window.location.origin + callback path.
	t.Setenv("APP_EXTERNAL_URL", "")
	svc := &mcpOAuthHostStubService{}

	_, c := runMCPOAuthAuthorizeURL(t, svc, "weknora.example.com",
		"https://weknora.example.com/api/v1/mcp-oauth/callback")

	require.Equal(t, 1, svc.lookups,
		"a same-origin redirect_uri must pass validation and reach the service lookup")
	require.NotEmpty(t, c.Errors)
	require.Contains(t, c.Errors.Last().Error(), "MCP service not found",
		"the handler must fail at the service lookup, not at redirect_uri validation")
}

func TestMCPOAuthAuthorizeURLRejectsRedirectURIFromAnotherHost(t *testing.T) {
	t.Setenv("APP_EXTERNAL_URL", "")
	svc := &mcpOAuthHostStubService{}

	_, c := runMCPOAuthAuthorizeURL(t, svc, "weknora.example.com",
		"https://evil.example/api/v1/mcp-oauth/callback")

	require.Zero(t, svc.lookups, "a foreign redirect_uri must never reach the service lookup")
	require.NotEmpty(t, c.Errors)
	require.Contains(t, c.Errors.Last().Error(), "invalid redirect_uri")
}
