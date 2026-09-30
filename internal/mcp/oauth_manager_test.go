package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/stretchr/testify/require"
)

type fakeMCPServiceRepo struct {
	interfaces.MCPServiceRepository
	service *types.MCPService
}

func (r fakeMCPServiceRepo) GetByID(_ context.Context, _ uint64, id string) (*types.MCPService, error) {
	if r.service != nil && r.service.ID == id {
		return r.service, nil
	}
	return nil, nil
}

// An authorization server may register a confidential client and return a
// client secret along with its ID. The code exchange runs in the callback
// request, with a handler rebuilt from the stored client, so the secret has
// to be stored too or the token request goes out without it.
func TestOAuthManagerKeepsTheRegisteredClientSecret(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)

	var tokenRequestSecret string
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer":                 server.URL,
				"authorization_endpoint": server.URL + "/authorize",
				"token_endpoint":         server.URL + "/token",
				"registration_endpoint":  server.URL + "/register",
			})
		case "/register":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"client_id":     "registered-client",
				"client_secret": "registered-secret",
			})
		case "/token":
			_ = r.ParseForm()
			tokenRequestSecret = r.PostForm.Get("client_secret")
			if tokenRequestSecret != "registered-secret" {
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": "invalid_client"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "access",
				"token_type":   "Bearer",
				"expires_in":   3600,
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	serviceURL := server.URL + "/mcp"
	service := &types.MCPService{
		ID:            "svc-1",
		TenantID:      7,
		TransportType: types.MCPTransportHTTPStreamable,
		URL:           &serviceURL,
		AuthConfig: &types.MCPAuthConfig{
			AuthType:              types.MCPAuthOAuth,
			AuthServerMetadataURL: server.URL + "/.well-known/oauth-authorization-server",
		},
	}
	repo := newFakeOAuthRepo()
	manager := NewOAuthManager(repo, fakeMCPServiceRepo{service: service}, nil)
	principal := types.Principal{Type: types.PrincipalWebUser, ID: "42"}
	ctx := context.Background()

	_, state, err := manager.StartAuthorization(ctx, service, 7, principal, "http://localhost:8080/callback", "/")
	require.NoError(t, err)
	stored, err := repo.GetClient(ctx, 7, "svc-1")
	require.NoError(t, err)
	require.NotNil(t, stored)
	require.Equal(t, "registered-client", stored.ClientID)
	require.Equal(t, "registered-secret", stored.ClientSecret)

	_, _, err = manager.CompleteAuthorization(ctx, state, "code")
	require.NoError(t, err)
	require.Equal(t, "registered-secret", tokenRequestSecret)

	token, err := repo.GetTokenForPrincipal(ctx, 7, principal, "svc-1")
	require.NoError(t, err)
	require.NotNil(t, token)
}
