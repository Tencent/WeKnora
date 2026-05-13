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
// ActiveTenant is the tenant whose ID is encoded in the JWT — every
// subsequent request is scoped to it until /auth/switch-tenant is called.
// Memberships lists every tenant the user can access along with their
// role in each, so callers can build a tenant switcher UI without a
// follow-up request.
type LoginResponse struct {
	Success bool      `json:"success"`
	Message string    `json:"message,omitempty"`
	User    *AuthUser `json:"user,omitempty"`
	// ActiveTenant is the tenant whose ID is encoded in the JWT. Use
	// this field; Tenant is preserved as a compatibility alias only.
	ActiveTenant *AuthTenant `json:"active_tenant,omitempty"`
	// Tenant is a deprecated alias kept so SDK callers compiled against
	// the pre-RBAC release continue to find the field by name. The
	// server emits active_tenant; this field is populated on the client
	// side by GetTenant() and will be removed in a future major release.
	//
	// Deprecated: use ActiveTenant.
	Tenant       *AuthTenant      `json:"-"`
	Memberships  []AuthMembership `json:"memberships,omitempty"`
	Token        string           `json:"token,omitempty"`
	RefreshToken string           `json:"refresh_token,omitempty"`
}

// GetTenant returns the active tenant, preferring ActiveTenant and
// falling back to the deprecated Tenant alias. SDK code should prefer
// reading ActiveTenant directly; this helper exists to keep callers that
// only know about the old field name compiling and working.
func (r *LoginResponse) GetTenant() *AuthTenant {
	if r == nil {
		return nil
	}
	if r.ActiveTenant != nil {
		return r.ActiveTenant
	}
	return r.Tenant
}

// AuthMembership pairs a tenant ID with the user's role in that tenant.
// Mirrors types.Membership on the server.
type AuthMembership struct {
	TenantID   uint64 `json:"tenant_id"`
	TenantName string `json:"tenant_name,omitempty"`
	Role       string `json:"role"`
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
	// 后端已将 tenant 字段重命名为 active_tenant；为照顾仍读取旧字段名的下游
	// 调用者，在反序列化后镜像一份到 Tenant 上。两者总是指向同一指针。
	out.Tenant = out.ActiveTenant
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
