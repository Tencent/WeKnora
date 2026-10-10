package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// PlatformAuthProviderService owns the two platform-level login providers that
// the SystemAdmin "用户管理" screen configures: the generic OIDC provider and
// the LDAP directory.
//
// Resolution order for both providers is DB row → legacy environment
// variables → built-in defaults. A deployment that has never opened the new
// screen therefore keeps behaving exactly as before, and the very first save
// simply freezes the effective configuration into the database.
//
// Credentials (OIDC client_secret, LDAP bind password) are write-only: the
// Read* methods return views whose secret field is replaced by a boolean, and
// only the login-path resolvers expose the plaintext.
type PlatformAuthProviderService interface {
	// ---- OIDC ----------------------------------------------------------

	// ResolveOIDCConfig returns the effective OIDC configuration, or nil when
	// the provider is disabled / not configured. Consumed by the login path.
	ResolveOIDCConfig(ctx context.Context) *types.ResolvedOIDCConfig
	// GetOIDCProvider returns the admin view (no secret).
	GetOIDCProvider(ctx context.Context) (*types.OIDCProviderView, error)
	// UpdateOIDCProvider persists the OIDC settings. A nil/empty ClientSecret
	// keeps the stored one; ClearSecret wipes it.
	UpdateOIDCProvider(
		ctx context.Context, req *types.OIDCProviderUpdateRequest,
	) (*types.OIDCProviderView, error)
	// TestOIDCProvider resolves the endpoints, performing a discovery fetch
	// when only a discovery URL / issuer is configured, and reports what it
	// found. It never mutates state.
	TestOIDCProvider(ctx context.Context) *types.OIDCTestResponse

	// ---- LDAP ----------------------------------------------------------

	// ResolveLDAPConfig returns the effective LDAP configuration, or nil when
	// the provider is disabled / not configured.
	ResolveLDAPConfig(ctx context.Context) *types.ResolvedLDAPConfig
	// GetLDAPProvider returns the admin view (no bind password).
	GetLDAPProvider(ctx context.Context) (*types.LDAPProviderView, error)
	// UpdateLDAPProvider persists the LDAP settings. A nil/empty BindPassword
	// keeps the stored one; ClearBindPassword wipes it.
	UpdateLDAPProvider(
		ctx context.Context, req *types.LDAPProviderUpdateRequest,
	) (*types.LDAPProviderView, error)
	// TestLDAPProvider probes the directory: service bind, optional user
	// search, optional user bind. Both request fields are optional.
	TestLDAPProvider(ctx context.Context, req *types.LDAPTestRequest) *types.LDAPTestResponse

	// ---- login fallback ------------------------------------------------

	// AuthenticateLDAP resolves a directory entry for `identifier` (the email
	// the user typed on the login form) and verifies `password` by binding as
	// that entry. It returns (nil, nil) when the provider is disabled or the
	// entry/password does not match, so callers can treat every failure as a
	// plain "invalid credentials" without leaking which step failed.
	AuthenticateLDAP(
		ctx context.Context, identifier, password string,
	) (*types.LDAPUserInfo, error)
}

// PlatformAuthProviderRepository persists the platform auth provider rows.
type PlatformAuthProviderRepository interface {
	// GetByKind returns the row for `kind`, or (nil, nil) when the deployment
	// has never configured that provider.
	GetByKind(
		ctx context.Context, kind types.PlatformAuthProviderKind,
	) (*types.PlatformAuthProvider, error)
	// Upsert inserts or updates the row keyed by kind.
	Upsert(ctx context.Context, provider *types.PlatformAuthProvider) error
}
