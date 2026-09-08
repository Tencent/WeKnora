package repository

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (r *mcpServiceRepository) GetMetadata(
	ctx context.Context,
	tenant uint64,
	service, principal string,
) (*types.MCPMetadata, error) {
	var snapshot types.MCPMetadata
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND service_id = ? AND principal = ?", tenant, service, principal).
		First(&snapshot).
		Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &snapshot, nil
}

func (r *mcpServiceRepository) SaveMetadata(ctx context.Context, snapshot *types.MCPMetadata) error {
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "tenant_id"}, {Name: "service_id"}, {Name: "principal"}},
		DoUpdates: clause.AssignmentColumns(
			[]string{
				"config_fingerprint",
				"tools",
				"instructions",
				"server_name",
				"server_version",
				"server_description",
				"synced_at",
			},
		),
		// An older, slower refresh must not overwrite a newer refresh.
		Where: clause.Where{
			Exprs: []clause.Expression{clause.Expr{SQL: "mcp_metadata.synced_at <= excluded.synced_at"}},
		},
	}).Create(snapshot).Error
}
