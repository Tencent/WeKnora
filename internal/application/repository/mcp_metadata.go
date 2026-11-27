package repository

import (
	"context"
	"errors"
	"fmt"

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

func metadataToolCountExpr(db *gorm.DB) string {
	if db.Name() == "postgres" {
		return "jsonb_array_length(tools)"
	}
	return "json_array_length(tools)"
}

func (r *mcpServiceRepository) ListMetadataSummaries(
	ctx context.Context,
	tenant uint64,
	principals []string,
) ([]*types.MCPMetadataSummary, error) {
	if len(principals) == 0 {
		return nil, nil
	}
	var rows []*types.MCPMetadataSummary
	err := r.db.WithContext(ctx).Model(&types.MCPMetadata{}).
		Select("service_id, principal, config_fingerprint, synced_at, server_name, "+
			metadataToolCountExpr(r.db)+" AS tool_count").
		Where("tenant_id = ? AND principal IN ?", tenant, principals).
		Find(&rows).Error
	return rows, err
}

func (r *mcpServiceRepository) SaveMetadata(
	ctx context.Context, snapshot *types.MCPMetadata, sourceEnabled bool,
) (*types.MCPMetadata, error) {
	checkConnection := func(db *gorm.DB) error {
		repo := mcpServiceRepository{db: db}
		service, err := repo.GetByID(ctx, snapshot.TenantID, snapshot.ServiceID)
		if err != nil {
			return fmt.Errorf("%w: %w", types.ErrMCPMetadataConnectionUnverified, err)
		}
		if service == nil || service.Enabled != sourceEnabled ||
			types.MCPConfigFingerprint(service) != snapshot.ConfigFingerprint {
			return types.ErrMCPMetadataConnectionChanged
		}
		return nil
	}

	var stored *types.MCPMetadata
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// PostgreSQL holds the service row through commit. SQLite refuses to
		// write through an outdated read transaction if another writer won.
		if err := checkConnection(tx.Clauses(clause.Locking{Strength: "SHARE"})); err != nil {
			return err
		}
		if err := upsertMCPMetadata(tx, snapshot); err != nil {
			return err
		}
		repo := mcpServiceRepository{db: tx}
		var err error
		stored, err = repo.GetMetadata(ctx, snapshot.TenantID, snapshot.ServiceID, snapshot.Principal)
		if err == nil && stored == nil {
			return gorm.ErrRecordNotFound
		}
		return err
	})
	if err != nil {
		if errors.Is(err, types.ErrMCPMetadataConnectionChanged) ||
			errors.Is(err, types.ErrMCPMetadataConnectionUnverified) {
			return nil, err
		}
		// A storage error permits chat to use the live listing. Recheck after
		// rollback in case a concurrent settings edit caused the write to fail.
		if checkErr := checkConnection(r.db); checkErr != nil {
			return nil, checkErr
		}
		return nil, fmt.Errorf("%w: %w", types.ErrMCPMetadataStorage, err)
	}
	stored.Stale = stored.ConfigFingerprint != snapshot.ConfigFingerprint
	return stored, nil
}

func upsertMCPMetadata(tx *gorm.DB, snapshot *types.MCPMetadata) error {
	return tx.Clauses(clause.OnConflict{
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
