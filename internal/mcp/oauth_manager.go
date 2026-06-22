package mcp

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/redis/go-redis/v9"
)

// clientRegistrationName is sent as client_name during dynamic client
// registration (RFC 7591).
const clientRegistrationName = "WeKnora"

// OAuthManager orchestrates the MCP OAuth2 authorization-code flow:
// discovery, dynamic client registration, the authorize redirect, and the
// callback code exchange. Tokens are persisted per (tenant, user, service);
// the registered client is persisted per (tenant, service) and reused.
type OAuthManager struct {
	repo        interfaces.MCPOAuthRepository
	serviceRepo interfaces.MCPServiceRepository
	states      *oauthStateStore
}

// NewOAuthManager constructs the OAuth manager. rdb may be nil (Lite mode),
// in which case in-flight authorization states are kept in memory.
func NewOAuthManager(
	repo interfaces.MCPOAuthRepository,
	serviceRepo interfaces.MCPServiceRepository,
	rdb *redis.Client,
) *OAuthManager {
	return &OAuthManager{
		repo:        repo,
		serviceRepo: serviceRepo,
		states:      newOAuthStateStore(rdb),
	}
}

// newHandler builds an OAuth handler bound to a service + per-user token store.
func (m *OAuthManager) newHandler(
	ctx context.Context, service *types.MCPService, tenantID uint64, userID, redirectURI string,
) (*transport.OAuthHandler, error) {
	if service.URL == nil || *service.URL == "" {
		return nil, fmt.Errorf("MCP service URL is required for OAuth")
	}
	cfg := transport.OAuthConfig{
		RedirectURI:           redirectURI,
		Scopes:                service.AuthConfig.Scopes,
		TokenStore:            newDBTokenStore(m.repo, tenantID, userID, service.ID),
		PKCEEnabled:           true,
		AuthServerMetadataURL: service.AuthConfig.AuthServerMetadataURL,
	}
	if existing, err := m.repo.GetClient(ctx, tenantID, service.ID); err == nil && existing != nil {
		cfg.ClientID = existing.ClientID
		cfg.ClientSecret = existing.ClientSecret
	}
	h := transport.NewOAuthHandler(cfg)
	h.SetBaseURL(*service.URL)
	return h, nil
}

// StartAuthorization performs discovery + (one-time) dynamic client
// registration, then returns the authorization URL the browser should visit.
// redirectURI is the backend callback URL registered with the auth server;
// frontendRedirect is where the callback bounces the browser when finished.
func (m *OAuthManager) StartAuthorization(
	ctx context.Context,
	service *types.MCPService,
	tenantID uint64,
	userID, redirectURI, frontendRedirect string,
) (string, error) {
	if !service.AuthConfig.IsOAuth() {
		return "", fmt.Errorf("MCP service %s does not use OAuth", service.ID)
	}

	h, err := m.newHandler(ctx, service, tenantID, userID, redirectURI)
	if err != nil {
		return "", err
	}

	// Register a client dynamically if we don't have one yet for this service.
	existing, _ := m.repo.GetClient(ctx, tenantID, service.ID)
	if existing == nil {
		if err := h.RegisterClient(ctx, clientRegistrationName); err != nil {
			return "", fmt.Errorf("dynamic client registration failed: %w", err)
		}
		clientID := h.GetClientID()
		if clientID == "" {
			return "", fmt.Errorf("dynamic client registration returned an empty client_id")
		}
		if err := m.repo.SaveClient(ctx, &types.MCPOAuthClient{
			TenantID:    tenantID,
			ServiceID:   service.ID,
			ClientID:    clientID,
			RedirectURI: redirectURI,
		}); err != nil {
			logger.GetLogger(ctx).Warnf("failed to persist MCP oauth client: %v", err)
		}
	}

	verifier, err := transport.GenerateCodeVerifier()
	if err != nil {
		return "", fmt.Errorf("failed to generate PKCE verifier: %w", err)
	}
	challenge := transport.GenerateCodeChallenge(verifier)
	state, err := transport.GenerateState()
	if err != nil {
		return "", fmt.Errorf("failed to generate state: %w", err)
	}

	authURL, err := h.GetAuthorizationURL(ctx, state, challenge)
	if err != nil {
		return "", fmt.Errorf("failed to build authorization URL: %w", err)
	}

	if err := m.states.Put(ctx, state, OAuthState{
		TenantID:         tenantID,
		UserID:           userID,
		ServiceID:        service.ID,
		CodeVerifier:     verifier,
		ClientID:         h.GetClientID(),
		RedirectURI:      redirectURI,
		FrontendRedirect: frontendRedirect,
	}); err != nil {
		return "", fmt.Errorf("failed to persist authorization state: %w", err)
	}

	return authURL, nil
}

// CompleteAuthorization handles the provider callback: it validates state,
// exchanges the code for tokens (PKCE), and persists the per-user token.
// Returns the frontend redirect URL recorded at StartAuthorization time.
func (m *OAuthManager) CompleteAuthorization(
	ctx context.Context, state, code string,
) (frontendRedirect string, err error) {
	st, err := m.states.Take(ctx, state)
	if err != nil {
		return "", err
	}
	frontendRedirect = st.FrontendRedirect

	service, err := m.serviceRepo.GetByID(ctx, st.TenantID, st.ServiceID)
	if err != nil {
		return frontendRedirect, fmt.Errorf("failed to load MCP service: %w", err)
	}
	if service == nil {
		return frontendRedirect, fmt.Errorf("MCP service not found")
	}

	h, err := m.newHandler(ctx, service, st.TenantID, st.UserID, st.RedirectURI)
	if err != nil {
		return frontendRedirect, err
	}
	// Re-prime the expected state so the library's CSRF check passes after
	// reconstructing the handler in this separate request.
	h.SetExpectedState(state)

	if err := h.ProcessAuthorizationResponse(ctx, code, state, st.CodeVerifier); err != nil {
		return frontendRedirect, fmt.Errorf("token exchange failed: %w", err)
	}
	// ProcessAuthorizationResponse persists the token via the TokenStore.
	logger.GetLogger(ctx).Infof(
		"MCP OAuth authorized: service=%s user=%s", st.ServiceID, st.UserID,
	)
	return frontendRedirect, nil
}

// IsAuthorized reports whether the given user has a stored (non-empty) token
// for the service.
func (m *OAuthManager) IsAuthorized(
	ctx context.Context, tenantID uint64, userID, serviceID string,
) (bool, error) {
	tok, err := m.repo.GetToken(ctx, tenantID, userID, serviceID)
	if err != nil {
		return false, err
	}
	return tok != nil && tok.AccessToken != "", nil
}

// Revoke removes the user's stored token for the service.
func (m *OAuthManager) Revoke(
	ctx context.Context, tenantID uint64, userID, serviceID string,
) error {
	return m.repo.DeleteToken(ctx, tenantID, userID, serviceID)
}
