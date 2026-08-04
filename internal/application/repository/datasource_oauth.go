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

type dataSourceOAuthRepository struct {
	db *gorm.DB
}

// NewDataSourceOAuthRepository creates persistent storage for delegated OAuth grants.
func NewDataSourceOAuthRepository(db *gorm.DB) interfaces.DataSourceOAuthRepository {
	return &dataSourceOAuthRepository{db: db}
}

func (r *dataSourceOAuthRepository) Get(
	ctx context.Context, tenantID uint64, dataSourceID string,
) (*types.DataSourceOAuthToken, error) {
	var token types.DataSourceOAuthToken
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND data_source_id = ?", tenantID, dataSourceID).
		First(&token).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &token, nil
}

func (r *dataSourceOAuthRepository) Save(ctx context.Context, token *types.DataSourceOAuthToken) error {
	if token == nil || token.TenantID == 0 || token.DataSourceID == "" {
		return fmt.Errorf("invalid data source oauth token")
	}
	toSave := *token
	toSave.UpdatedAt = time.Now().UTC()
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "tenant_id"}, {Name: "data_source_id"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"provider", "access_token", "refresh_token", "token_type", "scopes",
				"expires_at", "provider_account_id", "provider_tenant_id",
				"authorized_drive_id", "account_display_name", "authorized_by_user_id",
				"connection_version", "updated_at",
			}),
		}).Create(&toSave).Error
}

func (r *dataSourceOAuthRepository) SaveAuthorization(
	ctx context.Context,
	token *types.DataSourceOAuthToken,
	expectedConnectionVersion uint64,
	replaceConnection bool,
	resetConfig types.JSON,
) (uint64, error) {
	if token == nil || token.TenantID == 0 || token.DataSourceID == "" {
		return 0, fmt.Errorf("invalid data source oauth token")
	}
	newVersion := expectedConnectionVersion
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var ds types.DataSource
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND tenant_id = ? AND deleted_at IS NULL", token.DataSourceID, token.TenantID).
			First(&ds).Error; err != nil {
			return err
		}
		if ds.ConnectionVersion != expectedConnectionVersion {
			return fmt.Errorf("oauth connection version changed")
		}

		var existing types.DataSourceOAuthToken
		existingErr := tx.Where("tenant_id = ? AND data_source_id = ?", token.TenantID, token.DataSourceID).
			First(&existing).Error
		if existingErr != nil && !errors.Is(existingErr, gorm.ErrRecordNotFound) {
			return existingErr
		}
		if existingErr == nil && !replaceConnection &&
			(existing.ProviderAccountID != token.ProviderAccountID ||
				existing.ProviderTenantID != token.ProviderTenantID ||
				existing.AuthorizedDriveID != token.AuthorizedDriveID) {
			return fmt.Errorf("oauth account does not match the existing data source connection")
		}

		if replaceConnection && existingErr == nil {
			newVersion++
			if err := tx.Model(&types.DataSource{}).
				Where(
					"id = ? AND tenant_id = ? AND connection_version = ?",
					token.DataSourceID, token.TenantID, expectedConnectionVersion,
				).
				Updates(map[string]interface{}{
					"connection_version": newVersion,
					"config":             resetConfig,
					"last_sync_cursor":   nil,
					"last_sync_result":   nil,
					"last_sync_at":       nil,
					"status":             types.DataSourceStatusConnecting,
					"error_message":      "",
					"updated_at":         time.Now().UTC(),
				}).Error; err != nil {
				return err
			}
			if err := tx.Where("tenant_id = ? AND data_source_id = ?", token.TenantID, token.DataSourceID).
				Delete(&types.DataSourceItem{}).Error; err != nil {
				return err
			}
		}
		if !replaceConnection && ds.Status == types.DataSourceStatusReauthorizationRequired {
			if err := tx.Model(&types.DataSource{}).
				Where(
					"id = ? AND tenant_id = ? AND connection_version = ?",
					token.DataSourceID, token.TenantID, expectedConnectionVersion,
				).
				Updates(map[string]interface{}{
					"status": types.DataSourceStatusPaused, "error_message": "", "updated_at": time.Now().UTC(),
				}).Error; err != nil {
				return err
			}
		}

		token.ConnectionVersion = newVersion
		toSave := *token
		toSave.UpdatedAt = time.Now().UTC()
		return tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "tenant_id"}, {Name: "data_source_id"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"provider", "access_token", "refresh_token", "token_type", "scopes",
				"expires_at", "provider_account_id", "provider_tenant_id",
				"authorized_drive_id", "account_display_name", "authorized_by_user_id",
				"connection_version", "updated_at",
			}),
		}).Create(&toSave).Error
	})
	return newVersion, err
}

func (r *dataSourceOAuthRepository) RevokeAuthorization(
	ctx context.Context, tenantID uint64, dataSourceID string, expectedConnectionVersion uint64, resetConfig types.JSON,
) (uint64, error) {
	newVersion := expectedConnectionVersion + 1
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var ds types.DataSource
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND tenant_id = ? AND deleted_at IS NULL", dataSourceID, tenantID).
			First(&ds).Error; err != nil {
			return err
		}
		if ds.ConnectionVersion != expectedConnectionVersion {
			return fmt.Errorf("oauth connection version changed")
		}
		if err := tx.Model(&types.DataSource{}).
			Where("id = ? AND tenant_id = ? AND connection_version = ?", dataSourceID, tenantID, expectedConnectionVersion).
			Updates(map[string]interface{}{
				"connection_version": newVersion,
				"config":             resetConfig,
				"last_sync_cursor":   nil,
				"status":             types.DataSourceStatusPaused,
				"error_message":      "",
				"updated_at":         time.Now().UTC(),
			}).Error; err != nil {
			return err
		}
		if err := tx.Where("tenant_id = ? AND data_source_id = ?", tenantID, dataSourceID).
			Delete(&types.DataSourceOAuthToken{}).Error; err != nil {
			return err
		}
		return tx.Where("tenant_id = ? AND data_source_id = ?", tenantID, dataSourceID).
			Delete(&types.DataSourceItem{}).Error
	})
	return newVersion, err
}

func (r *dataSourceOAuthRepository) RefreshWithLock(
	ctx context.Context,
	tenantID uint64,
	dataSourceID string,
	connectionVersion uint64,
	refreshFn func(*types.DataSourceOAuthToken) error,
) (*types.DataSourceOAuthToken, error) {
	if refreshFn == nil {
		return nil, fmt.Errorf("refresh callback is nil")
	}
	var refreshed *types.DataSourceOAuthToken
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var token types.DataSourceOAuthToken
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("tenant_id = ? AND data_source_id = ?", tenantID, dataSourceID).
			First(&token).Error; err != nil {
			return err
		}
		if token.ConnectionVersion != connectionVersion {
			return fmt.Errorf("oauth connection version changed")
		}
		if err := refreshFn(&token); err != nil {
			return err
		}
		token.UpdatedAt = time.Now().UTC()
		toSave := token
		if err := tx.Save(&toSave).Error; err != nil {
			return err
		}
		refreshed = &token
		return nil
	})
	return refreshed, err
}

func (r *dataSourceOAuthRepository) Delete(ctx context.Context, tenantID uint64, dataSourceID string) error {
	return r.db.WithContext(ctx).
		Where("tenant_id = ? AND data_source_id = ?", tenantID, dataSourceID).
		Delete(&types.DataSourceOAuthToken{}).Error
}

type dataSourceItemRepository struct {
	db *gorm.DB
}

// NewDataSourceItemRepository creates storage for the durable remote-item projection.
func NewDataSourceItemRepository(db *gorm.DB) interfaces.DataSourceItemRepository {
	return &dataSourceItemRepository{db: db}
}

func (r *dataSourceItemRepository) Upsert(ctx context.Context, item *types.DataSourceItem) error {
	if item == nil || item.TenantID == 0 || item.DataSourceID == "" || item.DriveID == "" || item.ItemID == "" {
		return fmt.Errorf("invalid data source item")
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "data_source_id"}, {Name: "connection_version"}, {Name: "drive_id"}, {Name: "item_id"},
		},
		DoUpdates: clause.AssignmentColumns([]string{
			"tenant_id", "parent_item_id", "item_type", "selected_root_id", "external_id",
			"last_modified_at", "last_seen_generation", "ingested", "deleted_at", "updated_at",
		}),
	}).Create(item).Error
}

func (r *dataSourceItemRepository) Find(
	ctx context.Context, tenantID uint64, dataSourceID string, connectionVersion uint64, driveID, itemID string,
) (*types.DataSourceItem, error) {
	var item types.DataSourceItem
	err := r.scope(ctx, tenantID, dataSourceID, connectionVersion).
		Where("drive_id = ? AND item_id = ?", driveID, itemID).
		First(&item).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &item, err
}

func (r *dataSourceItemRepository) ListByParent(
	ctx context.Context, tenantID uint64, dataSourceID string, connectionVersion uint64, parentItemID string,
) ([]*types.DataSourceItem, error) {
	var items []*types.DataSourceItem
	err := r.scope(ctx, tenantID, dataSourceID, connectionVersion).
		Where("parent_item_id = ? AND deleted_at IS NULL", parentItemID).Find(&items).Error
	return items, err
}

func (r *dataSourceItemRepository) ListBySelectedRoot(
	ctx context.Context, tenantID uint64, dataSourceID string, connectionVersion uint64, selectedRootID string,
) ([]*types.DataSourceItem, error) {
	var items []*types.DataSourceItem
	err := r.scope(ctx, tenantID, dataSourceID, connectionVersion).
		Where("selected_root_id = ?", selectedRootID).Find(&items).Error
	return items, err
}

func (r *dataSourceItemRepository) ListNotSeen(
	ctx context.Context, tenantID uint64, dataSourceID string, connectionVersion uint64, generation string,
) ([]*types.DataSourceItem, error) {
	var items []*types.DataSourceItem
	err := r.scope(ctx, tenantID, dataSourceID, connectionVersion).
		Where("COALESCE(last_seen_generation, '') <> ? AND deleted_at IS NULL", generation).Find(&items).Error
	return items, err
}

func (r *dataSourceItemRepository) ListRetainedDeleted(
	ctx context.Context, tenantID uint64, dataSourceID string, connectionVersion uint64,
) ([]*types.DataSourceItem, error) {
	var items []*types.DataSourceItem
	err := r.scope(ctx, tenantID, dataSourceID, connectionVersion).
		Where("deleted_at IS NOT NULL AND ingested = ?", true).Find(&items).Error
	return items, err
}

func (r *dataSourceItemRepository) MarkDeleted(
	ctx context.Context, tenantID uint64, dataSourceID string, connectionVersion uint64,
	driveID, itemID string, deletedAt time.Time,
) error {
	return r.scope(ctx, tenantID, dataSourceID, connectionVersion).
		Where("drive_id = ? AND item_id = ?", driveID, itemID).
		Updates(map[string]interface{}{"deleted_at": deletedAt, "updated_at": time.Now().UTC()}).Error
}

func (r *dataSourceItemRepository) SetIngested(
	ctx context.Context, tenantID uint64, dataSourceID string, connectionVersion uint64,
	driveID, itemID string, ingested bool,
) error {
	updates := map[string]interface{}{"ingested": ingested, "updated_at": time.Now().UTC()}
	if ingested {
		updates["deleted_at"] = nil
	}
	return r.scope(ctx, tenantID, dataSourceID, connectionVersion).
		Where("drive_id = ? AND item_id = ?", driveID, itemID).
		Updates(updates).Error
}

func (r *dataSourceItemRepository) DeleteConnection(
	ctx context.Context, tenantID uint64, dataSourceID string, connectionVersion uint64,
) error {
	return r.scope(ctx, tenantID, dataSourceID, connectionVersion).Delete(&types.DataSourceItem{}).Error
}

func (r *dataSourceItemRepository) scope(
	ctx context.Context, tenantID uint64, dataSourceID string, connectionVersion uint64,
) *gorm.DB {
	return r.db.WithContext(ctx).Model(&types.DataSourceItem{}).
		Where("tenant_id = ? AND data_source_id = ? AND connection_version = ?", tenantID, dataSourceID, connectionVersion)
}
