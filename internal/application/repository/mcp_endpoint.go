package repository

import (
	"context"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

type mcpEndpointRepository struct {
	db *gorm.DB
}

// NewMCPEndpointRepository creates a GORM-backed MCP endpoint repository.
func NewMCPEndpointRepository(db *gorm.DB) interfaces.MCPEndpointRepository {
	return &mcpEndpointRepository{db: db}
}

func (r *mcpEndpointRepository) Create(ctx context.Context, ep *types.MCPEndpoint) error {
	return r.db.WithContext(ctx).Create(ep).Error
}

func (r *mcpEndpointRepository) GetByID(ctx context.Context, id string) (*types.MCPEndpoint, error) {
	var ep types.MCPEndpoint
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&ep).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &ep, nil
}

func (r *mcpEndpointRepository) GetByTokenHash(ctx context.Context, tokenHash string) (*types.MCPEndpoint, error) {
	if tokenHash == "" {
		return nil, nil
	}
	var ep types.MCPEndpoint
	err := r.db.WithContext(ctx).Where("token_hash = ?", tokenHash).First(&ep).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &ep, nil
}

func (r *mcpEndpointRepository) ListByTenant(ctx context.Context, tenantID uint64) ([]*types.MCPEndpoint, error) {
	var rows []*types.MCPEndpoint
	err := r.db.WithContext(ctx).
		Where("tenant_id = ?", tenantID).
		Order("created_at DESC").
		Find(&rows).Error
	return rows, err
}

func (r *mcpEndpointRepository) Update(ctx context.Context, ep *types.MCPEndpoint) error {
	return r.db.WithContext(ctx).Save(ep).Error
}

func (r *mcpEndpointRepository) Delete(ctx context.Context, tenantID uint64, id string) error {
	return r.db.WithContext(ctx).
		Where("tenant_id = ? AND id = ?", tenantID, id).
		Delete(&types.MCPEndpoint{}).Error
}

func (r *mcpEndpointRepository) TouchLastUsed(ctx context.Context, id string) error {
	now := time.Now()
	return r.db.WithContext(ctx).
		Model(&types.MCPEndpoint{}).
		Where("id = ?", id).
		UpdateColumn("last_used_at", now).Error
}
