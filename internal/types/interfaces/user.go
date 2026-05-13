package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// UserService defines the user service interface
type UserService interface {
	// Register creates a new user account
	Register(ctx context.Context, req *types.RegisterRequest) (*types.User, error)
	// Login authenticates a user and returns tokens
	Login(ctx context.Context, req *types.LoginRequest) (*types.LoginResponse, error)
	// GetOIDCAuthorizationURL builds the third-party OIDC authorization URL
	GetOIDCAuthorizationURL(ctx context.Context, redirectURI string) (*types.OIDCAuthURLResponse, error)
	// LoginWithOIDC exchanges the callback code, auto-provisions users if needed, and completes login
	LoginWithOIDC(ctx context.Context, code, redirectURI string) (*types.OIDCCallbackResponse, error)
	// GetUserByID gets a user by ID
	GetUserByID(ctx context.Context, id string) (*types.User, error)
	// GetUsersByIDs batch-fetches users by id, returning a map keyed by
	// user id. Missing ids are simply absent from the result; the call
	// is not an error when some ids resolve to no row. Used on hot list
	// endpoints (tenant members, audit logs) to avoid N+1 queries.
	GetUsersByIDs(ctx context.Context, ids []string) (map[string]*types.User, error)
	// GetUserByEmail gets a user by email
	GetUserByEmail(ctx context.Context, email string) (*types.User, error)
	// GetUserByUsername gets a user by username
	GetUserByUsername(ctx context.Context, username string) (*types.User, error)
	// GetUserByTenantID gets the first user (owner) of a tenant
	GetUserByTenantID(ctx context.Context, tenantID uint64) (*types.User, error)
	// UpdateUser updates user information
	UpdateUser(ctx context.Context, user *types.User) error
	// DeleteUser deletes a user
	DeleteUser(ctx context.Context, id string) error
	// ChangePassword changes user password
	ChangePassword(ctx context.Context, userID string, oldPassword, newPassword string) error
	// ValidatePassword validates user password
	ValidatePassword(ctx context.Context, userID string, password string) error
	// GenerateTokens generates access and refresh tokens for user
	GenerateTokens(ctx context.Context, user *types.User) (accessToken, refreshToken string, err error)
	// BuildLoginMemberships projects the user's tenant memberships into
	// the login-response shape. activeTenant is reused (without an extra
	// lookup) for the matching row's TenantName. The slice is guaranteed
	// non-nil so callers can serialise it as an empty JSON array when the
	// membership table is unavailable.
	BuildLoginMemberships(ctx context.Context, user *types.User, activeTenant *types.Tenant) []types.Membership
	// SwitchTenant issues a new token pair scoped to targetTenantID and
	// returns the corresponding LoginResponse. The caller's previous
	// refresh token (passed in for revocation) is invalidated. Membership
	// is verified via the TenantMember service before tokens are issued.
	SwitchTenant(ctx context.Context, user *types.User, targetTenantID uint64, currentRefreshToken string) (*types.LoginResponse, error)
	// ValidateToken validates an access token. It returns the user
	// referenced by the token plus the active tenant ID encoded in the
	// JWT's `tenant_id` claim — the latter lets the auth middleware
	// honour /auth/switch-tenant sessions that were minted with a
	// non-home tenant. Falls back to user.TenantID when the claim is
	// missing (old tokens issued before tenant-level RBAC).
	ValidateToken(ctx context.Context, token string) (*types.User, uint64, error)
	// RefreshToken refreshes access token using refresh token
	RefreshToken(ctx context.Context, refreshToken string) (accessToken, newRefreshToken string, err error)
	// RevokeToken revokes a token
	RevokeToken(ctx context.Context, token string) error
	// GetCurrentUser gets current user from context
	GetCurrentUser(ctx context.Context) (*types.User, error)
	// SearchUsers searches users by username or email
	SearchUsers(ctx context.Context, query string, limit int) ([]*types.User, error)
}

// UserRepository defines the user repository interface
type UserRepository interface {
	// CreateUser creates a user
	CreateUser(ctx context.Context, user *types.User) error
	// GetUserByID gets a user by ID
	GetUserByID(ctx context.Context, id string) (*types.User, error)
	// GetUsersByIDs batch-fetches users by id, returning a map keyed by
	// user id. Missing ids are simply absent from the result.
	GetUsersByIDs(ctx context.Context, ids []string) (map[string]*types.User, error)
	// GetUserByEmail gets a user by email
	GetUserByEmail(ctx context.Context, email string) (*types.User, error)
	// GetUserByUsername gets a user by username
	GetUserByUsername(ctx context.Context, username string) (*types.User, error)
	// GetUserByTenantID gets the first user (owner) of a tenant
	GetUserByTenantID(ctx context.Context, tenantID uint64) (*types.User, error)
	// UpdateUser updates a user
	UpdateUser(ctx context.Context, user *types.User) error
	// DeleteUser deletes a user
	DeleteUser(ctx context.Context, id string) error
	// ListUsers lists users with pagination
	ListUsers(ctx context.Context, offset, limit int) ([]*types.User, error)
	// SearchUsers searches users by username or email
	SearchUsers(ctx context.Context, query string, limit int) ([]*types.User, error)
}

// AuthTokenRepository defines the auth token repository interface
type AuthTokenRepository interface {
	// CreateToken creates an auth token
	CreateToken(ctx context.Context, token *types.AuthToken) error
	// GetTokenByValue gets a token by its value
	GetTokenByValue(ctx context.Context, tokenValue string) (*types.AuthToken, error)
	// GetTokensByUserID gets all tokens for a user
	GetTokensByUserID(ctx context.Context, userID string) ([]*types.AuthToken, error)
	// UpdateToken updates a token
	UpdateToken(ctx context.Context, token *types.AuthToken) error
	// DeleteToken deletes a token
	DeleteToken(ctx context.Context, id string) error
	// DeleteExpiredTokens deletes all expired tokens
	DeleteExpiredTokens(ctx context.Context) error
	// RevokeTokensByUserID revokes all tokens for a user
	RevokeTokensByUserID(ctx context.Context, userID string) error
}
