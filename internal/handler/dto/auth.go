package dto

import "github.com/Tencent/WeKnora/internal/types"

// AuthLoginResponse is the HTTP-safe login / switch-tenant response shape.
type AuthLoginResponse struct {
	Success      bool               `json:"success"`
	Message      string             `json:"message,omitempty"`
	User         *types.User        `json:"user,omitempty"`
	ActiveTenant *TenantResponse    `json:"active_tenant,omitempty"`
	Memberships  []types.Membership `json:"memberships"`
	Token        string             `json:"token,omitempty"`
	RefreshToken string             `json:"refresh_token,omitempty"`
}

// AuthOIDCCallbackResponse is the HTTP-safe OIDC callback payload shape.
type AuthOIDCCallbackResponse struct {
	Success      bool               `json:"success"`
	Message      string             `json:"message,omitempty"`
	User         *types.User        `json:"user,omitempty"`
	Tenant       *TenantResponse    `json:"tenant,omitempty"`
	Memberships  []types.Membership `json:"memberships"`
	Token        string             `json:"token,omitempty"`
	RefreshToken string             `json:"refresh_token,omitempty"`
	IsNewUser    bool               `json:"is_new_user,omitempty"`
}

// MaskCrossTenantFlag returns u with can_access_all_tenants forced to false
// when the deployment does not enable cross-tenant access.
//
// The users column is only a *permission*; the deployment-wide
// TENANT_ENABLE_CROSS_TENANT_ACCESS switch decides whether it is honoured.
// GET /auth/me has always applied that AND. Every other endpoint handing out a
// raw users row must apply the same one, or the flag flips mid-session —
// /auth/login reports the raw column while the next /auth/me reports the
// effective value — and the client briefly renders permissions the deployment
// does not actually grant.
//
// The struct is copied, never mutated: the caller's user may alias a row that
// is still referenced elsewhere.
func MaskCrossTenantFlag(u *types.User, crossTenantEnabled bool) *types.User {
	if u == nil || crossTenantEnabled || !u.CanAccessAllTenants {
		return u
	}
	clone := *u
	clone.CanAccessAllTenants = false
	return &clone
}

// NewAuthLoginResponse converts a service-layer login response for HTTP output.
//
// crossTenantEnabled is the deployment-wide TENANT_ENABLE_CROSS_TENANT_ACCESS
// switch; see MaskCrossTenantFlag for why the DTO — rather than each caller —
// owns this gate.
func NewAuthLoginResponse(resp *types.LoginResponse, crossTenantEnabled bool) *AuthLoginResponse {
	if resp == nil {
		return nil
	}
	var role types.TenantRole
	if resp.ActiveTenant != nil {
		role = membershipRoleForTenant(resp.Memberships, resp.ActiveTenant.ID)
	}
	return &AuthLoginResponse{
		Success:      resp.Success,
		Message:      resp.Message,
		User:         MaskCrossTenantFlag(resp.User, crossTenantEnabled),
		ActiveTenant: NewTenantResponseWithRole(resp.ActiveTenant, role),
		Memberships:  resp.Memberships,
		Token:        resp.Token,
		RefreshToken: resp.RefreshToken,
	}
}

// NewAuthOIDCCallbackResponse converts an OIDC callback response for HTTP
// output. crossTenantEnabled is applied for the same reason as in
// NewAuthLoginResponse.
func NewAuthOIDCCallbackResponse(resp *types.OIDCCallbackResponse, crossTenantEnabled bool) *AuthOIDCCallbackResponse {
	if resp == nil {
		return nil
	}
	var role types.TenantRole
	if resp.Tenant != nil {
		role = membershipRoleForTenant(resp.Memberships, resp.Tenant.ID)
	}
	return &AuthOIDCCallbackResponse{
		Success:      resp.Success,
		Message:      resp.Message,
		User:         MaskCrossTenantFlag(resp.User, crossTenantEnabled),
		Tenant:       NewTenantResponseWithRole(resp.Tenant, role),
		Memberships:  resp.Memberships,
		Token:        resp.Token,
		RefreshToken: resp.RefreshToken,
		IsNewUser:    resp.IsNewUser,
	}
}

func membershipRoleForTenant(memberships []types.Membership, tenantID uint64) types.TenantRole {
	for _, m := range memberships {
		if m.TenantID == tenantID && m.Role.IsValid() {
			return m.Role
		}
	}
	return ""
}
