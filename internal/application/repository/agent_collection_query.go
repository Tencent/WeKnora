package repository

import (
	"context"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
)

func (r *agentCollectionRepository) ListProfiles(
	ctx context.Context,
	filter types.AgentCollectionProfileFilter,
) (*types.AgentCollectionProfilePage, error) {
	page, pageSize := collectionPageBounds(filter.Page, filter.PageSize)
	query := r.db.WithContext(ctx).Model(&types.AgentCollectionProfile{})
	query = applyCollectionProfileFilters(query, filter)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, err
	}
	var items []*types.AgentCollectionProfile
	if err := query.Order("updated_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&items).Error; err != nil {
		return nil, err
	}
	return &types.AgentCollectionProfilePage{Items: items, Total: total, Page: page, PageSize: pageSize}, nil
}

func (r *agentCollectionRepository) ListProfilesForExport(
	ctx context.Context,
	filter types.AgentCollectionProfileFilter,
	limit int,
) ([]*types.AgentCollectionProfile, error) {
	if limit < 1 || limit > 100001 {
		limit = 100001
	}
	query := r.db.WithContext(ctx).Model(&types.AgentCollectionProfile{})
	query = applyCollectionProfileFilters(query, filter)
	var profiles []*types.AgentCollectionProfile
	err := query.Order("updated_at DESC, id DESC").Limit(limit).Find(&profiles).Error
	return profiles, err
}

func (r *agentCollectionRepository) SummarizeProfiles(
	ctx context.Context,
	filter types.AgentCollectionProfileFilter,
) (*types.AgentCollectionSummary, error) {
	query := r.db.WithContext(ctx).Model(&types.AgentCollectionProfile{})
	query = applyCollectionProfileFilters(query, filter)
	summary := &types.AgentCollectionSummary{}
	if err := query.Count(&summary.Profiles).Error; err != nil {
		return nil, err
	}
	if err := query.Distinct("user_id").Count(&summary.Users).Error; err != nil {
		return nil, err
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	if err := query.Where("updated_at >= ?", today).Count(&summary.UpdatedToday).Error; err != nil {
		return nil, err
	}
	if err := query.Where("is_complete = ?", false).Count(&summary.Incomplete).Error; err != nil {
		return nil, err
	}
	return summary, nil
}

func applyCollectionProfileFilters(
	query *gorm.DB,
	filter types.AgentCollectionProfileFilter,
) *gorm.DB {
	if filter.TenantID != 0 {
		query = query.Where("tenant_id = ?", filter.TenantID)
	}
	if filter.AgentID != "" {
		query = query.Where("agent_id = ?", filter.AgentID)
	}
	if filter.UserID != "" {
		query = query.Where("user_id = ?", filter.UserID)
	}
	if filter.Complete != nil {
		query = query.Where("is_complete = ?", *filter.Complete)
	}
	if filter.UpdatedFrom != nil {
		query = query.Where("updated_at >= ?", *filter.UpdatedFrom)
	}
	if filter.UpdatedTo != nil {
		query = query.Where("updated_at <= ?", *filter.UpdatedTo)
	}
	if filter.Keyword != "" {
		pattern := "%" + filter.Keyword + "%"
		query = query.Where("agent_id LIKE ? OR user_id LIKE ?", pattern, pattern)
	}
	return applyCollectionFieldFilter(query, filter)
}

func applyCollectionFieldFilter(
	query *gorm.DB,
	filter types.AgentCollectionProfileFilter,
) *gorm.DB {
	if filter.FieldKey == "" {
		return query
	}
	if query.Dialector.Name() == "sqlite" {
		path := "$." + filter.FieldKey + ".value"
		if filter.FieldValue == "" {
			return query.Where("json_extract(values, ?) IS NOT NULL", path)
		}
		return query.Where("CAST(json_extract(values, ?) AS TEXT) = ?", path, filter.FieldValue)
	}
	if filter.FieldValue == "" {
		return query.Where("jsonb_exists(values, ?)", filter.FieldKey)
	}
	return query.Where("values -> ? ->> 'value' = ?", filter.FieldKey, filter.FieldValue)
}

func (r *agentCollectionRepository) ListHistory(
	ctx context.Context,
	profileID string,
	page, pageSize int,
) (*types.AgentCollectionHistoryPage, error) {
	page, pageSize = collectionPageBounds(page, pageSize)
	query := r.db.WithContext(ctx).Model(&types.AgentCollectionHistory{}).Where("profile_id = ?", profileID)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, err
	}
	var items []*types.AgentCollectionHistory
	err := query.Order("created_at DESC, id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&items).Error
	if err != nil {
		return nil, err
	}
	return &types.AgentCollectionHistoryPage{Items: items, Total: total, Page: page, PageSize: pageSize}, nil
}

func collectionPageBounds(page, pageSize int) (int, int) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 200 {
		pageSize = 200
	}
	return page, pageSize
}

func (r *agentCollectionRepository) CreateExport(
	ctx context.Context,
	export *types.AgentCollectionExport,
) error {
	return r.db.WithContext(ctx).Create(export).Error
}

func (r *agentCollectionRepository) UpdateExport(
	ctx context.Context,
	export *types.AgentCollectionExport,
) error {
	return r.db.WithContext(ctx).Save(export).Error
}

func (r *agentCollectionRepository) GetExport(
	ctx context.Context,
	exportID string,
) (*types.AgentCollectionExport, error) {
	var export types.AgentCollectionExport
	err := r.db.WithContext(ctx).Where("id = ?", exportID).First(&export).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrAgentCollectionExportNotFound
	}
	if err != nil {
		return nil, err
	}
	return &export, nil
}

func (r *agentCollectionRepository) SoftDeleteByAgent(ctx context.Context, agentID string) error {
	return r.db.WithContext(ctx).Where("agent_id = ?", agentID).Delete(&types.AgentCollectionProfile{}).Error
}

func (r *agentCollectionRepository) SoftDeleteByUser(ctx context.Context, userID string) error {
	return r.db.WithContext(ctx).Where("user_id = ?", userID).Delete(&types.AgentCollectionProfile{}).Error
}

func (r *agentCollectionRepository) PurgeProfile(ctx context.Context, profileID string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("profile_id = ?", profileID).Delete(&types.AgentCollectionHistory{}).Error; err != nil {
			return err
		}
		result := tx.Unscoped().Where("id = ?", profileID).Delete(&types.AgentCollectionProfile{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrAgentCollectionProfileNotFound
		}
		return nil
	})
}
