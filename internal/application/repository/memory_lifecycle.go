package repository

import (
	"context"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// SaveItem 保留原有保存接口，并使用默认待确认记忆上限。
func (r *memoryRepository) SaveItem(
	ctx context.Context, scope interfaces.MemoryScope, item *types.MemoryItem, replacesID string,
) error {
	return r.SaveItemWithPendingLimit(ctx, scope, item, replacesID, types.DefaultMemoryMaxItems)
}

// SaveItemWithPendingLimit 在主体事务内保存替换关系，并归档超限的最旧待确认记忆。
// 待确认记忆不会因容量限制而淘汰已确认的事实。
func (r *memoryRepository) SaveItemWithPendingLimit(
	ctx context.Context, scope interfaces.MemoryScope, item *types.MemoryItem, replacesID string, pendingLimit int,
) error {
	if pendingLimit <= 0 {
		pendingLimit = types.DefaultMemoryMaxItems
	}
	if pendingLimit > 2000 {
		pendingLimit = 2000
	}
	return r.withSubject(ctx, scope, func(tx *gorm.DB, _ *types.MemorySubject) error {
		// Recheck rejection under the same subject lock as RejectPendingItem.
		// A service preflight can race a rejection while a model result is saved.
		txRepo := &memoryRepository{db: tx}
		forgotten, err := txRepo.HasTombstone(ctx, scope, types.MemoryFingerprint(item.Content))
		if err != nil {
			return err
		}
		if !forgotten && item.Origin == types.MemoryOriginExtracted && item.SourceMessageID != "" {
			forgotten, err = txRepo.HasTombstoneForMessage(
				ctx, scope, item.SourceMessageID, types.MemoryRejectedMessageWindow,
			)
			if err != nil {
				return err
			}
		}
		if forgotten {
			return types.ErrMemoryPreviouslyForgotten
		}
		query := func() *gorm.DB { return tx.Where("tenant_id = ? AND subject_id = ?", scope.TenantID, scope.SubjectID) }
		trimPending := func() error {
			if item.Status != types.MemoryStatusPending {
				return nil
			}
			// Only bounded survivor IDs enter memory, even for a legacy backlog.
			var keep []string
			if err := query().Model(&types.MemoryItem{}).Where("status = ?", types.MemoryStatusPending).
				Order("created_at DESC, id DESC").Limit(pendingLimit).Pluck("id", &keep).Error; err != nil {
				return err
			}
			if err := query().Model(&types.MemoryItem{}).
				Where("status = ? AND id NOT IN ?", types.MemoryStatusPending, keep).
				Updates(map[string]interface{}{
					"status": types.MemoryStatusArchived, "updated_at": time.Now(),
				}).Error; err != nil {
				return err
			}
			// A replay or backdated proposal may itself be archived by a lowered
			// cap. Return the committed status rather than the old pending copy.
			return query().First(item, "id = ?", item.ID).Error
		}
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
					return trimPending()
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
				return trimPending()
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
		if len(ids) > 0 {
			if err := query().Model(&types.MemoryItem{}).
				Where("id <> ? AND (id IN ? OR replaces_id IN ?) AND status IN ?", item.ID, ids, ids,
					[]string{types.MemoryStatusActive, types.MemoryStatusPending}).
				Updates(map[string]interface{}{
					"status": types.MemoryStatusSuperseded, "invalid_at": time.Now(),
					"superseded_by": item.ID, "updated_at": time.Now(),
				}).Error; err != nil {
				return err
			}
		}
		return trimPending()
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
