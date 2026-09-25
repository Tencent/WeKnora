package mcp

import (
	"context"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/mark3labs/mcp-go/client/transport"
)

// dbTokenStore is a transport.TokenStore backed by the MCPOAuthRepository,
// scoped to a single (tenant, principal, service) tuple. The mcp-go OAuth handler
// calls SaveToken after a successful authorization or refresh. Runtime MCP
// transports receive the managedTokenStore wrapper below so refresh decisions
// stay in WeKnora's coordinated lifecycle instead of the dependency.
type dbTokenStore struct {
	repo      interfaces.MCPOAuthRepository
	tenantID  uint64
	principal types.Principal
	serviceID string
}

// managedTokenStore hides local expiry from mcp-go transports. WeKnora checks
// the persisted ExpiresAt before every operation and performs the coordinated
// refresh itself; allowing the dependency to also auto-refresh would bypass
// the cross-instance lease and collapse refresh failures into a generic
// authorization-required error.
type managedTokenStore struct {
	*dbTokenStore
}

func newManagedTokenStore(
	repo interfaces.MCPOAuthRepository, tenantID uint64, principal types.Principal, serviceID string,
) *managedTokenStore {
	return &managedTokenStore{dbTokenStore: newDBTokenStore(repo, tenantID, principal, serviceID)}
}

func (s *managedTokenStore) GetToken(ctx context.Context) (*transport.Token, error) {
	token, err := s.dbTokenStore.GetToken(ctx)
	if err != nil {
		return nil, err
	}
	token.ExpiresAt = time.Time{}
	return token, nil
}

// newDBTokenStore creates a per-principal, per-service token store.
func newDBTokenStore(
	repo interfaces.MCPOAuthRepository, tenantID uint64, principal types.Principal, serviceID string,
) *dbTokenStore {
	return &dbTokenStore{
		repo:      repo,
		tenantID:  tenantID,
		principal: principal.Normalize(),
		serviceID: serviceID,
	}
}

// GetToken returns the persisted token, or transport.ErrNoToken when the user
// has not authorized this service yet.
func (s *dbTokenStore) GetToken(ctx context.Context) (*transport.Token, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	row, err := s.repo.GetTokenForPrincipal(ctx, s.tenantID, s.principal, s.serviceID)
	if err != nil {
		return nil, err
	}
	if row == nil || row.AccessToken == "" {
		return nil, transport.ErrNoToken
	}
	return &transport.Token{
		AccessToken:  row.AccessToken,
		RefreshToken: row.RefreshToken,
		TokenType:    row.TokenType,
		ExpiresAt:    row.ExpiresAt,
	}, nil
}

// SaveToken persists a freshly issued or refreshed token.
func (s *dbTokenStore) SaveToken(ctx context.Context, token *transport.Token) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if token == nil || token.AccessToken == "" {
		return fmt.Errorf("OAuth token response did not contain an access_token")
	}
	if token.TokenType == "" {
		token.TokenType = "Bearer"
	}
	expiresAt := token.ExpiresAt
	if expiresAt.IsZero() && token.ExpiresIn > 0 {
		expiresAt = time.Now().Add(time.Duration(token.ExpiresIn) * time.Second)
	}
	principal := s.principal.Normalize()
	return s.repo.SaveTokenForPrincipal(ctx, &types.MCPOAuthToken{
		TenantID:      s.tenantID,
		PrincipalType: principal.Type,
		PrincipalID:   principal.ID,
		UserID:        principal.StorageID(),
		ServiceID:     s.serviceID,
		AccessToken:   token.AccessToken,
		RefreshToken:  token.RefreshToken,
		TokenType:     token.TokenType,
		ExpiresAt:     expiresAt,
	})
}
