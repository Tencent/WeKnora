package dto

import "github.com/Tencent/WeKnora/internal/types"

// AuthLoginResponse is the HTTP-safe login / switch-tenant response shape.
type AuthLoginResponse struct {
	Success      bool              `json:"success"`
	Message      string            `json:"message,omitempty"`
	User         *types.User       `json:"user,omitempty"`
	ActiveTenant *TenantResponse   `json:"active_tenant,omitempty"`
	Memberships  []types.Membership `json:"memberships"`
	Token        string            `json:"token,omitempty"`
	RefreshToken string            `json:"refresh_token,omitempty"`
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

// NewAuthLoginResponse converts a service-layer login response for HTTP output.
func NewAuthLoginResponse(resp *types.LoginResponse) *AuthLoginResponse {
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
		User:         resp.User,
		ActiveTenant: NewTenantResponseWithRole(resp.ActiveTenant, role),
		Memberships:  resp.Memberships,
		Token:        resp.Token,
		RefreshToken: resp.RefreshToken,
	}
}

// NewAuthOIDCCallbackResponse converts an OIDC callback response for HTTP output.
func NewAuthOIDCCallbackResponse(resp *types.OIDCCallbackResponse) *AuthOIDCCallbackResponse {
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
		User:         resp.User,
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
