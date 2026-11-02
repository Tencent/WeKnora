package repository

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ListSubjects 分页返回租户内的主体统计，按最近活动时间及主体 UUID 倒序排列。
func (r *memoryRepository) ListSubjects(
	ctx context.Context, tenantID uint64, limit, offset int,
) ([]*types.MemorySubjectSummary, int64, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	db := r.db.WithContext(ctx)
	var total int64
	if err := db.Model(&types.MemorySubject{}).Where("tenant_id = ?", tenantID).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	counts := db.Model(&types.MemoryItem{}).
		Select(`subject_id, COUNT(*) AS item_count,
			SUM(CASE WHEN status = 'active' THEN 1 ELSE 0 END) AS active_count,
			SUM(CASE WHEN status = 'pending' THEN 1 ELSE 0 END) AS pending_count`).
		Where("tenant_id = ?", tenantID).Group("subject_id")
	// Keep timestamps as original columns: SQLite returns time.Time for these,
	// whereas a MAX/CASE expression loses the column's timestamp type.
	var rows []struct {
		types.MemorySubjectSummary
		ItemUpdatedAt *time.Time
	}
	err := db.Table("memory_subjects AS subjects").
		Select(`subjects.id, subjects.subject_id, subjects.enabled, subjects.updated_at AS last_activity_at,
			COALESCE(counts.item_count, 0) AS item_count,
			COALESCE(counts.active_count, 0) AS active_count,
			COALESCE(counts.pending_count, 0) AS pending_count, latest.updated_at AS item_updated_at`).
		Joins("LEFT JOIN (?) AS counts ON counts.subject_id = subjects.subject_id", counts).
		Joins(`LEFT JOIN memory_items AS latest ON latest.id = (
			SELECT id FROM memory_items WHERE tenant_id = subjects.tenant_id AND subject_id = subjects.subject_id
			ORDER BY updated_at DESC, id DESC LIMIT 1)`).
		Where("subjects.tenant_id = ?", tenantID).
		Order(`CASE WHEN latest.updated_at > subjects.updated_at
			THEN latest.updated_at ELSE subjects.updated_at END DESC, subjects.id DESC`).
		Limit(limit).Offset(offset).Scan(&rows).Error
	if err != nil {
		return nil, 0, err
	}
	summaries := make([]*types.MemorySubjectSummary, 0, len(rows))
	for _, row := range rows {
		summary := row.MemorySubjectSummary
		if row.ItemUpdatedAt != nil && row.ItemUpdatedAt.After(summary.LastActivityAt) {
			summary.LastActivityAt = *row.ItemUpdatedAt
		}
		summary.PrincipalType, _, _ = strings.Cut(summary.SubjectID, ":")
		summaries = append(summaries, &summary)
	}
	return summaries, total, nil
}

// GetSubjectByID 按租户和主体 UUID 查找记忆空间，缺失或属于其他租户时均返回 nil。
func (r *memoryRepository) GetSubjectByID(
	ctx context.Context, tenantID uint64, id string,
) (*types.MemorySubject, error) {
	var subject types.MemorySubject
	if err := r.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&subject).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &subject, nil
}

// RejectPendingItem 在主体锁内重新校验待确认状态，并原子记录拒绝及删除记忆。
func (r *memoryRepository) RejectPendingItem(ctx context.Context, scope interfaces.MemoryScope, id string) error {
	return r.withSubject(ctx, scope, func(tx *gorm.DB, _ *types.MemorySubject) error {
		var item types.MemoryItem
		if err := tx.Where("tenant_id = ? AND subject_id = ? AND id = ?", scope.TenantID, scope.SubjectID, id).
			First(&item).Error; err != nil {
			return err
		}
		if item.Status != types.MemoryStatusPending {
			return types.ErrMemoryConflict
		}
		// Bind the existing lifecycle operations to this transaction. DeleteItem's
		// nested transaction is a savepoint; the subject lock remains held until
		// this outer transaction commits, including tombstone creation/trimming.
		scopedRepo := &memoryRepository{db: tx}
		if err := scopedRepo.AddTombstone(ctx, scope, item.Topic,
			types.MemoryFingerprint(item.Content), item.SourceMessageID); err != nil {
			return err
		}
		return scopedRepo.DeleteItem(ctx, scope, id)
	})
}

// ClearSubjectMemory 原子清除指定主体的记忆及派生状态，保留开关和历史提取游标。
func (r *memoryRepository) ClearSubjectMemory(ctx context.Context, scope interfaces.MemoryScope) (int64, error) {
	var removed int64
	err := r.withSubject(ctx, scope, func(tx *gorm.DB, subject *types.MemorySubject) error {
		for _, model := range []any{
			&types.MemoryItemEmbedding{}, &types.MemoryTopicStat{},
			&types.MemoryDocAffinity{}, &types.MemoryTombstone{},
		} {
			if err := tx.Where("tenant_id = ? AND subject_id = ?", scope.TenantID, scope.SubjectID).
				Delete(model).Error; err != nil {
				return err
			}
		}
		result := tx.Where("tenant_id = ? AND subject_id = ?", scope.TenantID, scope.SubjectID).
			Delete(&types.MemoryItem{})
		if result.Error != nil {
			return result.Error
		}
		removed = result.RowsAffected
		// The existing legacy cursor is also the forget cutoff. Advance it and
		// session cursors monotonically so future messages cannot replay history.
		cutoff := time.Now()
		if subject.ExtractCursor != nil && subject.ExtractCursor.After(cutoff) {
			cutoff = *subject.ExtractCursor
		}
		subject.ExtractCursor = &cutoff
		// Preserve legacy queue IDs as drained progress rows too, so redelivered
		// tasks cannot bootstrap those sessions again from their old payload.
		if err := importLegacySessions(tx, subject); err != nil {
			return err
		}
		if err := extractionRows(tx, scope).Update("pending", false).Error; err != nil {
			return err
		}
		if err := advanceClearCursors(tx, scope, cutoff); err != nil {
			return err
		}
		return tx.Model(subject).Updates(map[string]any{
			"block_text": "", "block_updated_at": nil, "item_count": 0,
			"consolidated_at": nil, "forced_consolidated_at": nil,
			"extraction_state": types.MemoryExtractionState{Generation: uuid.NewString()},
			"extract_cursor":   cutoff, "pending_sessions": types.MemoryPendingSessions{},
			"extract_scheduled_at": nil, "updated_at": time.Now(),
		}).Error
	})
	if err != nil {
		return 0, err
	}
	return removed, nil
}

// Compare instants in Go: SQLite stores timestamps with their original offsets,
// so a SQL text comparison can move a newer UTC cursor backwards. Page by the
// scope-local session key to keep even a long conversation history bounded.
func advanceClearCursors(tx *gorm.DB, scope interfaces.MemoryScope, cutoff time.Time) error {
	const batchSize = 200
	var after string
	for {
		var sessions []types.MemoryExtractionSession
		query := extractionRows(tx, scope).Select("session_id", "cursor_at", "cursor_id")
		if after != "" {
			query = query.Where("session_id > ?", after)
		}
		if err := query.Order("session_id").Limit(batchSize).Find(&sessions).Error; err != nil {
			return err
		}
		var ids []string
		for _, session := range sessions {
			if session.Cursor.At.Before(cutoff) {
				ids = append(ids, session.SessionID)
			}
		}
		if len(ids) > 0 {
			if err := extractionRows(tx, scope).Where("session_id IN ?", ids).
				Updates(map[string]any{"cursor_at": cutoff, "cursor_id": "", "updated_at": cutoff}).Error; err != nil {
				return err
			}
		}
		if len(sessions) < batchSize {
			return nil
		}
		after = sessions[len(sessions)-1].SessionID
	}
}
