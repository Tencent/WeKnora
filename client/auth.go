package client

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// LoginRequest is the body for POST /api/v1/auth/login.
type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// LoginResponse is the body returned by POST /api/v1/auth/login.
//
// Token is the JWT access token; RefreshToken renews it via Auth.Refresh.
type LoginResponse struct {
	Success      bool        `json:"success"`
	Message      string      `json:"message,omitempty"`
	User         *AuthUser   `json:"user,omitempty"`
	Tenant       *AuthTenant `json:"tenant,omitempty"`
	Token        string      `json:"token,omitempty"`
	RefreshToken string      `json:"refresh_token,omitempty"`
}

// AuthUser is the principal returned by /auth/login and /auth/me.
//
// Fields mirror the server's UserInfo projection (no PasswordHash).
type AuthUser struct {
	ID                  string    `json:"id"`
	Username            string    `json:"username"`
	Email               string    `json:"email"`
	Avatar              string    `json:"avatar,omitempty"`
	TenantID            uint64    `json:"tenant_id"`
	IsActive            bool      `json:"is_active"`
	CanAccessAllTenants bool      `json:"can_access_all_tenants,omitempty"`
	CreatedAt           time.Time `json:"created_at,omitempty"`
}

// AuthTenant is the tenant projection returned alongside AuthUser.
type AuthTenant struct {
	ID   uint64 `json:"id"`
	Name string `json:"name"`
}

// CurrentUserResponse is the body of GET /api/v1/auth/me.
type CurrentUserResponse struct {
	Success bool `json:"success"`
	Data    struct {
		User   *AuthUser   `json:"user,omitempty"`
		Tenant *AuthTenant `json:"tenant,omitempty"`
	} `json:"data"`
}

// Canonical paths for the auth endpoints. Exposed so callers building
// HTTP middleware (e.g. the CLI's 401-retry transport) can classify
// requests without re-hardcoding the literals.
const (
	PathAuthLogin   = "/api/v1/auth/login"
	PathAuthRefresh = "/api/v1/auth/refresh"
)

// RefreshTokenResponse is the body of POST /api/v1/auth/refresh.
type RefreshTokenResponse struct {
	Success      bool   `json:"success"`
	Message      string `json:"message,omitempty"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

// Login authenticates with email + password and returns the JWT access token,
// refresh token, and principal info. Maps to POST /api/v1/auth/login.
//
// Used by `weknora auth login`.
func (c *Client) Login(ctx context.Context, req LoginRequest) (*LoginResponse, error) {
	resp, err := c.doRequest(ctx, http.MethodPost, "/api/v1/auth/login", req, nil)
	if err != nil {
		return nil, fmt.Errorf("login request: %w", err)
	}
	var out LoginResponse
	if err := parseResponse(resp, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetCurrentUser returns the currently authenticated principal and the
// tenant projection. Maps to GET /api/v1/auth/me; the bearer token must
// already be set on the client (use WithBearerToken).
//
// Used by `weknora auth status` and `weknora whoami`.
func (c *Client) GetCurrentUser(ctx context.Context) (*CurrentUserResponse, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/api/v1/auth/me", nil, nil)
	if err != nil {
		return nil, fmt.Errorf("get current user: %w", err)
	}
	var out CurrentUserResponse
	if err := parseResponse(resp, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RefreshToken renews the JWT access token using a refresh token.
// Maps to POST /api/v1/auth/refresh.
//
// Callers (`weknora auth refresh`) read the refresh token from secrets,
// invoke this method, and persist both new tokens. The SDK does not touch
// the secrets store directly — it stays a transport-only layer.
func (c *Client) RefreshToken(ctx context.Context, refreshToken string) (*RefreshTokenResponse, error) {
	body := struct {
		RefreshToken string `json:"refreshToken"`
	}{RefreshToken: refreshToken}
	resp, err := c.doRequest(ctx, http.MethodPost, "/api/v1/auth/refresh", body, nil)
	if err != nil {
		return nil, fmt.Errorf("refresh token: %w", err)
	}
	var out RefreshTokenResponse
	if err := parseResponse(resp, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
