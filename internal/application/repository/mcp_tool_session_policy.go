package repository

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type mcpToolSessionPolicyRepository struct{ db *gorm.DB }

// NewMCPToolSessionPolicyRepository creates tenant-scoped IM policy storage.
func NewMCPToolSessionPolicyRepository(db *gorm.DB) interfaces.MCPToolSessionPolicyRepository {
	return &mcpToolSessionPolicyRepository{db: db}
}

func (r *mcpToolSessionPolicyRepository) ListByService(
	ctx context.Context, tenantID uint64, serviceID string,
) ([]*types.MCPToolSessionPolicy, error) {
	rows := []*types.MCPToolSessionPolicy{}
	err := r.db.WithContext(ctx).Where("tenant_id = ? AND service_id = ?", tenantID, serviceID).
		Order("tool_name ASC").Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list MCP tool session policies: %w", err)
	}
	return rows, nil
}

func (r *mcpToolSessionPolicyRepository) Upsert(ctx context.Context, row *types.MCPToolSessionPolicy) error {
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "service_id"}, {Name: "tool_name"}},
		DoUpdates: clause.AssignmentColumns([]string{"allowed_sessions", "updated_at"}),
	}).Create(row).Error
}

func (r *mcpToolSessionPolicyRepository) Delete(
	ctx context.Context, tenantID uint64, serviceID, toolName string,
) error {
	return r.db.WithContext(ctx).
		Where("tenant_id = ? AND service_id = ? AND tool_name = ?", tenantID, serviceID, toolName).
		Delete(&types.MCPToolSessionPolicy{}).Error
}
