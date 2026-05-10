package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// MCPToolApprovalRepository implements interfaces.MCPToolApprovalRepository.
type MCPToolApprovalRepository struct {
	db *gorm.DB
}

// NewMCPToolApprovalRepository creates a repository backed by GORM.
func NewMCPToolApprovalRepository(db *gorm.DB) interfaces.MCPToolApprovalRepository {
	return &MCPToolApprovalRepository{db: db}
}

// ListByService returns all stored approval rows for an MCP service (may be empty).
func (r *MCPToolApprovalRepository) ListByService(ctx context.Context, tenantID uint64, serviceID string) ([]*types.MCPToolApproval, error) {
	var rows []*types.MCPToolApproval
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND service_id = ?", tenantID, serviceID).
		Order("tool_name ASC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list mcp tool approvals: %w", err)
	}
	return rows, nil
}

// IsRequired returns true when a row exists with require_approval = true.
func (r *MCPToolApprovalRepository) IsRequired(ctx context.Context, tenantID uint64, serviceID, toolName string) (bool, error) {
	var row types.MCPToolApproval
	err := r.db.WithContext(ctx).
		Select("require_approval").
		Where("tenant_id = ? AND service_id = ? AND tool_name = ?", tenantID, serviceID, toolName).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("get mcp tool approval: %w", err)
	}
	return row.RequireApproval, nil
}

// Upsert creates or updates the approval flag for a tool.
func (r *MCPToolApprovalRepository) Upsert(ctx context.Context, row *types.MCPToolApproval) error {
	if row == nil {
		return errors.New("row is nil")
	}
	var existing types.MCPToolApproval
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND service_id = ? AND tool_name = ?", row.TenantID, row.ServiceID, row.ToolName).
		First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if row.ID == "" {
			row.ID = uuid.New().String()
		}
		if err := r.db.WithContext(ctx).Create(row).Error; err != nil {
			return fmt.Errorf("create mcp tool approval: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("get mcp tool approval for upsert: %w", err)
	}
	return r.db.WithContext(ctx).Model(&existing).Updates(map[string]interface{}{
		"require_approval": row.RequireApproval,
		"updated_at":       time.Now(),
	}).Error
}
