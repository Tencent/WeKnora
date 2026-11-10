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
	"gorm.io/gorm/clause"
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

// Get returns the stored policy of a tool, or nil when it has none.
func (r *MCPToolApprovalRepository) Get(
	ctx context.Context, tenantID uint64, serviceID, toolName string,
) (*types.MCPToolApproval, error) {
	var row types.MCPToolApproval
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND service_id = ? AND tool_name = ?", tenantID, serviceID, toolName).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get mcp tool policy: %w", err)
	}
	return &row, nil
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

// IsEnabled returns true when a tool has no stored policy row. This preserves
// the historical behaviour for tools discovered before per-tool settings.
func (r *MCPToolApprovalRepository) IsEnabled(ctx context.Context, tenantID uint64, serviceID, toolName string) (bool, error) {
	var row types.MCPToolApproval
	err := r.db.WithContext(ctx).
		Select("enabled").
		Where("tenant_id = ? AND service_id = ? AND tool_name = ?", tenantID, serviceID, toolName).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("get mcp tool enabled state: %w", err)
	}
	return row.Enabled, nil
}

// riskRankSQL ranks a risk column as policy.Risk.Above does; unknown and
// empty risks rank lowest.
func riskRankSQL(column string) string {
	return "(CASE " + column +
		" WHEN 'read' THEN 1 WHEN 'probe' THEN 2 WHEN 'write' THEN 3 WHEN 'destructive' THEN 4 ELSE 0 END)"
}

// UpsertPolicy creates or updates only the supplied policy columns.
// ObservedRisk only ever raises the stored value.
// Concurrent patches of different fields cannot clobber each other because
// ON CONFLICT updates omit unchanged columns. A first insert uses
// require_approval=false and enabled=true for any field the patch omits.
func (r *MCPToolApprovalRepository) UpsertPolicy(
	ctx context.Context, tenantID uint64, serviceID, toolName string, patch types.MCPToolPolicyPatch,
) error {
	if serviceID == "" || toolName == "" {
		return errors.New("service_id and tool_name are required")
	}
	if patch.RequireApproval == nil && patch.Enabled == nil && patch.Risk == nil && patch.ObservedRisk == nil {
		return errors.New("an MCP tool policy field is required")
	}

	now := time.Now()
	requireApproval := false
	if patch.RequireApproval != nil {
		requireApproval = *patch.RequireApproval
	}
	enabled := true
	if patch.Enabled != nil {
		enabled = *patch.Enabled
	}

	updates := map[string]interface{}{"updated_at": now}
	if patch.RequireApproval != nil {
		updates["require_approval"] = *patch.RequireApproval
	}
	if patch.Enabled != nil {
		updates["enabled"] = *patch.Enabled
	}
	risk, observed := "", ""
	if patch.Risk != nil {
		risk = *patch.Risk
		updates["risk"] = risk
	}
	if patch.ObservedRisk != nil {
		observed = *patch.ObservedRisk
		// Compare in the statement itself: a concurrent lower observation
		// read before this one landed must not overwrite it.
		updates["observed_risk"] = gorm.Expr(
			"CASE WHEN " + riskRankSQL("excluded.observed_risk") + " > " +
				riskRankSQL("mcp_tool_approvals.observed_risk") +
				" THEN excluded.observed_risk ELSE mcp_tool_approvals.observed_risk END")
	}

	row := map[string]interface{}{
		"id":               uuid.New().String(),
		"tenant_id":        tenantID,
		"service_id":       serviceID,
		"tool_name":        toolName,
		"require_approval": requireApproval,
		"enabled":          enabled,
		"risk":             risk,
		"observed_risk":    observed,
		"created_at":       now,
		"updated_at":       now,
	}
	// Create via map so enabled=false is persisted. GORM struct inserts omit
	// bool zero values when the field has a default tag.
	err := r.db.WithContext(ctx).Model(&types.MCPToolApproval{}).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "tenant_id"},
			{Name: "service_id"},
			{Name: "tool_name"},
		},
		DoUpdates: clause.Assignments(updates),
	}).Create(row).Error
	if err != nil {
		return fmt.Errorf("upsert mcp tool policy: %w", err)
	}
	return nil
}
