package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/mark3labs/mcp-go/client/transport"
)

// jwtBearerGrantType is the JWT bearer authorization grant (RFC 7523 section
// 2.1). MCP's Enterprise-Managed Authorization extension sends an Identity
// Assertion JWT Authorization Grant (ID-JAG) with it.
const jwtBearerGrantType = "urn:ietf:params:oauth:grant-type:jwt-bearer"

// oauthAssertionTimeout bounds the token request and storing its result. It
// is detached from the caller: once the authorization server has issued a
// token for an assertion that can only be used once, a caller that went away
// must not leave it unsaved.
const oauthAssertionTimeout = 60 * time.Second

// maxTokenResponseBytes caps the token endpoint response that is read.
const maxTokenResponseBytes = 1 << 20

// ErrOAuthClientNotRegistered is returned when the service has no OAuth client
// to send the assertion grant with: neither a pre-registered one nor a stored
// registration. The grant does not register one: dynamic client registration
// describes a redirect-based client, which is not what a server-to-server
// grant needs.
var ErrOAuthClientNotRegistered = errors.New("no OAuth client is registered for this MCP service")

// OAuthGrantRejectedError reports that the authorization server refused the
// grant: a 4xx response other than 408 and 429, or a 2xx response carrying an
// OAuth error. Sending the same request again does not help; the assertion or
// the client has to change.
type OAuthGrantRejectedError struct {
	StatusCode int
	Code       string
}

func (e *OAuthGrantRejectedError) Error() string {
	if e.Code == "" {
		return fmt.Sprintf("authorization server rejected the assertion with status %d", e.StatusCode)
	}
	return fmt.Sprintf("authorization server rejected the assertion: %s", e.Code)
}

// AuthorizeWithAssertion obtains principal's token for service with the JWT
// bearer grant and stores it the way an authorization code callback does.
//
// It serves deployments where an application in front of WeKnora signs the
// user in and calls WeKnora for that user, so no browser can follow an
// authorization redirect. That application presents an assertion of the
// user's identity; WeKnora forwards it unread, because validating it is up to
// the authorization server. The grant is sent with the service's
// pre-registered client, or else the client already registered for it.
func (m *OAuthManager) AuthorizeWithAssertion(
	ctx context.Context,
	service *types.MCPService,
	tenantID uint64,
	principal types.Principal,
	assertion string,
) error {
	if !service.AuthConfig.IsOAuth() {
		return fmt.Errorf("MCP service %s does not use OAuth", service.ID)
	}
	principal = principal.Normalize()
	if !principal.Valid() {
		return fmt.Errorf("principal context is required to authorize OAuth MCP service %s", service.ID)
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), oauthAssertionTimeout)
	defer cancel()

	if _, _, preRegistered := preRegisteredClient(service); !preRegistered {
		existing, err := m.repo.GetClient(ctx, tenantID, service.ID)
		if err != nil {
			return fmt.Errorf("failed to read MCP OAuth client registration: %w", err)
		}
		if existing == nil {
			return ErrOAuthClientNotRegistered
		}
	}
	h, err := m.newHandler(ctx, service, tenantID, principal, "")
	if err != nil {
		return err
	}
	metadata, err := h.GetServerMetadata(ctx)
	if err != nil {
		return fmt.Errorf("failed to get authorization server metadata: %w", err)
	}
	if metadata.TokenEndpoint == "" {
		return fmt.Errorf("authorization server metadata has no token_endpoint")
	}

	form := url.Values{}
	form.Set("grant_type", jwtBearerGrantType)
	form.Set("assertion", assertion)
	form.Set("client_id", h.GetClientID())
	if secret := h.GetClientSecret(); secret != "" {
		form.Set("client_secret", secret)
	}
	if len(service.AuthConfig.Scopes) > 0 {
		form.Set("scope", strings.Join(service.AuthConfig.Scopes, " "))
	}
	token, err := requestToken(ctx, newOAuthHTTPClient(), metadata.TokenEndpoint, form)
	if err != nil {
		return err
	}
	if err := newDBTokenStore(m.repo, tenantID, principal, service.ID).SaveToken(ctx, token); err != nil {
		return fmt.Errorf("failed to save MCP OAuth token: %w", err)
	}
	logger.GetLogger(ctx).Infof(
		"MCP OAuth authorized with assertion: service=%s principal=%s", service.ID, principal.StorageID(),
	)
	return nil
}

// requestToken posts a token request and reads the token response. A 5xx,
// 408 or 429 response is a temporary failure whatever its body says; another
// 4xx response, or a 2xx response whose body has an OAuth error, is an
// OAuthGrantRejectedError.
// Response bodies are not copied into errors, which callers may return to
// their own clients.
func requestToken(
	ctx context.Context, client *http.Client, endpoint string, form url.Values,
) (*transport.Token, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send token request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxTokenResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to read token response: %w", err)
	}

	if resp.StatusCode >= http.StatusInternalServerError ||
		resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("token endpoint returned status %d", resp.StatusCode)
	}
	var oauthErr transport.OAuthError
	_ = json.Unmarshal(body, &oauthErr)
	if resp.StatusCode >= http.StatusBadRequest ||
		(resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices && oauthErr.ErrorCode != "") {
		return nil, &OAuthGrantRejectedError{StatusCode: resp.StatusCode, Code: oauthErr.ErrorCode}
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("token endpoint returned status %d", resp.StatusCode)
	}

	var token transport.Token
	if err := json.Unmarshal(body, &token); err != nil {
		return nil, fmt.Errorf("failed to decode token response: %w", err)
	}
	if token.ExpiresIn > 0 {
		token.ExpiresAt = time.Now().Add(time.Duration(token.ExpiresIn) * time.Second)
	}
	return &token, nil
}
