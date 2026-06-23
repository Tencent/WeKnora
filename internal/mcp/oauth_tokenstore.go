package mcp

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/mark3labs/mcp-go/client/transport"
)

// dbTokenStore is a transport.TokenStore backed by the MCPOAuthRepository,
// scoped to a single (tenant, user, service) tuple. The mcp-go OAuth handler
// calls GetToken before each request (refreshing via refresh_token when
// expired) and SaveToken after a successful authorization or refresh, so this
// store transparently persists refreshed tokens back to the database.
type dbTokenStore struct {
	repo      interfaces.MCPOAuthRepository
	tenantID  uint64
	userID    string
	serviceID string
}

// newDBTokenStore creates a per-user, per-service token store.
func newDBTokenStore(
	repo interfaces.MCPOAuthRepository, tenantID uint64, userID, serviceID string,
) *dbTokenStore {
	return &dbTokenStore{repo: repo, tenantID: tenantID, userID: userID, serviceID: serviceID}
}

// GetToken returns the persisted token, or transport.ErrNoToken when the user
// has not authorized this service yet.
func (s *dbTokenStore) GetToken(ctx context.Context) (*transport.Token, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	row, err := s.repo.GetToken(ctx, s.tenantID, s.userID, s.serviceID)
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
	expiresAt := token.ExpiresAt
	if expiresAt.IsZero() && token.ExpiresIn > 0 {
		expiresAt = time.Now().Add(time.Duration(token.ExpiresIn) * time.Second)
	}
	return s.repo.SaveToken(ctx, &types.MCPOAuthToken{
		TenantID:     s.tenantID,
		UserID:       s.userID,
		ServiceID:    s.serviceID,
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		TokenType:    token.TokenType,
		ExpiresAt:    expiresAt,
	})
}
