package repository

import (
	"context"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// SaveItem serializes replacements with confirmation and manual edits. A
// proposal can replace another proposal, but cannot retire an active fact.
func (r *memoryRepository) SaveItem(ctx context.Context, scope interfaces.MemoryScope, item *types.MemoryItem, replacesID string) error {
	return r.withSubject(ctx, scope, func(tx *gorm.DB, _ *types.MemorySubject) error {
		query := func() *gorm.DB { return tx.Where("tenant_id = ? AND subject_id = ?", scope.TenantID, scope.SubjectID) }
		var target types.MemoryItem
		if replacesID != "" {
			if err := query().First(&target, "id = ?", replacesID).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return types.ErrMemoryConflict
				}
				return err
			}
			if target.Status != types.MemoryStatusActive && target.Status != types.MemoryStatusPending {
				// A successfully applied decision may be replayed after a failed
				// checkpoint. Return its replacement instead of writing it twice.
				var replacement types.MemoryItem
				if target.SupersededBy != "" && query().First(&replacement, "id = ?", target.SupersededBy).Error == nil &&
					replacement.Status == item.Status && replacement.Content == item.Content {
					*item = replacement
					return nil
				}
				return types.ErrMemoryConflict
			}
		}
		var live []*types.MemoryItem
		if err := query().Where("normalized_key = ? AND status IN ?", item.NormalizedKey,
			[]string{types.MemoryStatusActive, types.MemoryStatusPending}).Find(&live).Error; err != nil {
			return err
		}
		for _, old := range live {
			if old.Content == item.Content && old.Status == item.Status {
				*item = *old
				return nil
			}
		}
		if item.Status == types.MemoryStatusPending {
			if target.Status == types.MemoryStatusActive {
				item.ReplacesID = target.ID
			}
			if target.Status == types.MemoryStatusPending {
				item.ReplacesID = target.ReplacesID
			}
			if item.ReplacesID == "" {
				for _, old := range live {
					if old.Status == types.MemoryStatusActive {
						item.ReplacesID = old.ID
						break
					}
				}
			}
		}
		item.TenantID, item.SubjectID = scope.TenantID, scope.SubjectID
		if err := tx.Create(item).Error; err != nil {
			return err
		}
		ids := make([]string, 0, len(live)+2)
		if target.ID != "" {
			live = append(live, &target)
		}
		if target.ReplacesID != "" && item.Status == types.MemoryStatusActive {
			ids = append(ids, target.ReplacesID)
		}
		for _, old := range live {
			if item.Status == types.MemoryStatusPending && old.Status == types.MemoryStatusActive {
				continue
			}
			ids = append(ids, old.ID)
		}
		if len(ids) == 0 {
			return nil
		}
		return query().Model(&types.MemoryItem{}).
			Where("id <> ? AND (id IN ? OR replaces_id IN ?) AND status IN ?", item.ID, ids, ids,
				[]string{types.MemoryStatusActive, types.MemoryStatusPending}).
			Updates(map[string]interface{}{"status": types.MemoryStatusSuperseded, "invalid_at": time.Now(), "superseded_by": item.ID, "updated_at": time.Now()}).Error
	})
}

func (r *memoryRepository) ConfirmPendingItem(ctx context.Context, scope interfaces.MemoryScope, id string) error {
	return r.withSubject(ctx, scope, func(tx *gorm.DB, _ *types.MemorySubject) error {
		query := func() *gorm.DB { return tx.Where("tenant_id = ? AND subject_id = ?", scope.TenantID, scope.SubjectID) }
		var item types.MemoryItem
		if err := query().First(&item, "id = ?", id).Error; err != nil {
			return err
		}
		if item.Status == types.MemoryStatusActive {
			return nil
		}
		if item.Status != types.MemoryStatusPending || (item.ExpiresAt != nil && !item.ExpiresAt.After(time.Now())) {
			return types.ErrMemoryConflict
		}
		if item.ReplacesID != "" {
			var target types.MemoryItem
			err := query().First(&target, "id = ?", item.ReplacesID).Error
			if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && target.Status != types.MemoryStatusActive) {
				return types.ErrMemoryConflict
			}
			if err != nil {
				return err
			}
		}
		now := time.Now()
		if err := query().Model(&types.MemoryItem{}).
			Where("id <> ? AND (normalized_key = ? OR id = ? OR (replaces_id <> '' AND replaces_id = ?)) AND status IN ?",
				id, item.NormalizedKey, item.ReplacesID, item.ReplacesID, []string{types.MemoryStatusActive, types.MemoryStatusPending}).
			Updates(map[string]interface{}{"status": types.MemoryStatusSuperseded, "invalid_at": now, "superseded_by": id, "updated_at": now}).Error; err != nil {
			return err
		}
		return query().Model(&types.MemoryItem{}).Where("id = ?", id).
			Updates(map[string]interface{}{"status": types.MemoryStatusActive, "updated_at": now}).Error
	})
}
