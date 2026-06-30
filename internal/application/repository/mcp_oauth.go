package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// mcpOAuthRepository implements interfaces.MCPOAuthRepository.
type mcpOAuthRepository struct {
	db *gorm.DB
}

// NewMCPOAuthRepository creates a new MCP OAuth repository.
func NewMCPOAuthRepository(db *gorm.DB) interfaces.MCPOAuthRepository {
	return &mcpOAuthRepository{db: db}
}

func (r *mcpOAuthRepository) GetClient(
	ctx context.Context, tenantID uint64, serviceID string,
) (*types.MCPOAuthClient, error) {
	var client types.MCPOAuthClient
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND service_id = ?", tenantID, serviceID).
		First(&client).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &client, nil
}

func (r *mcpOAuthRepository) SaveClient(ctx context.Context, client *types.MCPOAuthClient) error {
	client.UpdatedAt = time.Now()
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "service_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"client_id", "client_secret", "redirect_uri", "updated_at"}),
		}).
		Create(client).Error
}

func (r *mcpOAuthRepository) DeleteClient(ctx context.Context, tenantID uint64, serviceID string) error {
	return r.db.WithContext(ctx).
		Where("tenant_id = ? AND service_id = ?", tenantID, serviceID).
		Delete(&types.MCPOAuthClient{}).Error
}

func (r *mcpOAuthRepository) GetToken(
	ctx context.Context, tenantID uint64, userID, serviceID string,
) (*types.MCPOAuthToken, error) {
	return r.GetTokenForPrincipal(ctx, tenantID, types.Principal{
		Type: types.PrincipalWebUser,
		ID:   userID,
	}, serviceID)
}

func (r *mcpOAuthRepository) GetTokenForPrincipal(
	ctx context.Context, tenantID uint64, principal types.Principal, serviceID string,
) (*types.MCPOAuthToken, error) {
	principal = principal.Normalize()
	if !principal.Valid() {
		return nil, nil
	}
	var token types.MCPOAuthToken
	err := r.db.WithContext(ctx).
		Where(
			"tenant_id = ? AND principal_type = ? AND principal_id = ? AND service_id = ?",
			tenantID, principal.Type, principal.ID, serviceID,
		).
		First(&token).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &token, nil
}

func (r *mcpOAuthRepository) SaveToken(ctx context.Context, token *types.MCPOAuthToken) error {
	if token.PrincipalType == "" || token.PrincipalID == "" {
		token.PrincipalType = types.PrincipalWebUser
		token.PrincipalID = token.UserID
	}
	return r.SaveTokenForPrincipal(ctx, token)
}

func (r *mcpOAuthRepository) SaveTokenForPrincipal(ctx context.Context, token *types.MCPOAuthToken) error {
	if token.PrincipalType == "" || token.PrincipalID == "" {
		return fmt.Errorf("mcp oauth token requires principal_type and principal_id")
	}
	if token.UserID == "" {
		token.UserID = (types.Principal{Type: token.PrincipalType, ID: token.PrincipalID}).StorageID()
	}
	token.UpdatedAt = time.Now()
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{
				{Name: "tenant_id"},
				{Name: "principal_type"},
				{Name: "principal_id"},
				{Name: "service_id"},
			},
			DoUpdates: clause.AssignmentColumns([]string{
				"user_id", "access_token", "refresh_token", "token_type", "expires_at", "updated_at",
			}),
		}).
		Create(token).Error
}

func (r *mcpOAuthRepository) DeleteToken(
	ctx context.Context, tenantID uint64, userID, serviceID string,
) error {
	return r.DeleteTokenForPrincipal(ctx, tenantID, types.Principal{
		Type: types.PrincipalWebUser,
		ID:   userID,
	}, serviceID)
}

func (r *mcpOAuthRepository) DeleteTokenForPrincipal(
	ctx context.Context, tenantID uint64, principal types.Principal, serviceID string,
) error {
	principal = principal.Normalize()
	if !principal.Valid() {
		return nil
	}
	return r.db.WithContext(ctx).
		Where(
			"tenant_id = ? AND principal_type = ? AND principal_id = ? AND service_id = ?",
			tenantID, principal.Type, principal.ID, serviceID,
		).
		Delete(&types.MCPOAuthToken{}).Error
}
