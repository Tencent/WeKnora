package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

func (r *knowledgeBaseRepository) FindOriginalForCleanup(ctx context.Context, tenantID uint64, kbID, ref string) (*types.StoredResource, bool, error) {
	db := r.db.WithContext(ctx)
	var resource *types.StoredResource
	paths := []string{ref}
	if handle, ok := types.ParseResourcePath(ref); ok {
		resource = &types.StoredResource{}
		if err := db.Unscoped().Where("tenant_id = ? AND handle = ?", tenantID, handle).Take(resource).Error; err != nil {
			return nil, false, fmt.Errorf("find original resource: %w", err)
		}
		paths = append(paths, resource.PhysicalPath)
		var external int64
		owners := db.Model(&types.Knowledge{}).Select("id").
			Where("tenant_id = ? AND knowledge_base_id = ?", tenantID, kbID)
		if err := db.Model(&types.ResourceBinding{}).
			Where("resource_id = ? AND (tenant_id <> ? OR owner_type <> ? OR owner_id NOT IN (?))",
				resource.ID, tenantID, types.ResourceOwnerKnowledge, owners).
			Count(&external).Error; err != nil {
			return nil, false, err
		}
		if external != 0 {
			return resource, true, nil
		}
	}
	var other types.Knowledge
	if err := db.Select("id").Where("file_path IN ? AND (tenant_id <> ? OR knowledge_base_id <> ?)",
		paths, tenantID, kbID).Limit(1).Find(&other).Error; err != nil {
		return nil, false, err
	}
	if other.ID != "" {
		return resource, true, nil
	}
	var alias types.StoredResource
	registered := db.Select("id").Where("state = ?", types.ResourceStateActive)
	if resource == nil {
		registered = registered.Where("physical_path = ?", ref)
	} else {
		registered = registered.Where("id <> ? AND (physical_path = ? OR (location_hash <> '' AND location_hash = ?))",
			resource.ID, resource.PhysicalPath, resource.LocationHash)
	}
	if err := registered.Limit(1).Find(&alias).Error; err != nil {
		return nil, false, err
	}
	return resource, alias.ID != "", nil
}

func (r *knowledgeBaseRepository) MarkOriginalDeleted(ctx context.Context, tenantID uint64, resourceID string) error {
	return r.db.WithContext(ctx).Model(&types.StoredResource{}).
		Where("tenant_id = ? AND id = ?", tenantID, resourceID).
		Updates(map[string]any{"state": types.ResourceStateDeleted, "deleted_at": time.Now()}).Error
}
