package repository

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (r *memoryRepository) withSubject(ctx context.Context, scope interfaces.MemoryScope, fn func(*gorm.DB, *types.MemorySubject) error) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var subject types.MemorySubject
		if err := tx.Where("tenant_id = ? AND subject_id = ?", scope.TenantID, scope.SubjectID).
			Clauses(forUpdateClause()).First(&subject).Error; err != nil {
			return err
		}
		return fn(tx, &subject)
	})
}

func saveExtractionState(tx *gorm.DB, subject *types.MemorySubject) error {
	return tx.Model(subject).Updates(map[string]interface{}{
		"extraction_state": subject.ExtractionState, "pending_sessions": subject.PendingSessions,
		"extract_scheduled_at": subject.ExtractScheduledAt, "updated_at": time.Now(),
	}).Error
}

func extractionRows(tx *gorm.DB, scope interfaces.MemoryScope) *gorm.DB {
	return tx.Model(&types.MemoryExtractionSession{}).
		Where("tenant_id = ? AND subject_id = ?", scope.TenantID, scope.SubjectID)
}

func enqueueExtractionSession(tx *gorm.DB, subject *types.MemorySubject, id string, bump bool) error {
	if id == "" {
		return nil
	}
	row := types.MemoryExtractionSession{
		TenantID: subject.TenantID, SubjectID: subject.SubjectID, SessionID: id, Revision: 1, Pending: true,
	}
	// Preserve the pre-upgrade boundary instead of replaying every historical
	// conversation. This value is frozen; only per-session cursors advance now.
	if subject.ExtractCursor != nil {
		row.Cursor.At = *subject.ExtractCursor
	}
	conflict := clause.OnConflict{
		Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "subject_id"}, {Name: "session_id"}},
		DoNothing: true,
	}
	if bump {
		conflict.DoNothing = false
		conflict.DoUpdates = clause.Assignments(map[string]interface{}{
			"revision": gorm.Expr("memory_extraction_sessions.revision + 1"), "pending": true, "updated_at": time.Now(),
		})
	}
	return tx.Clauses(conflict).Create(&row).Error
}

func importLegacySessions(tx *gorm.DB, subject *types.MemorySubject) error {
	for _, id := range subject.PendingSessions {
		if err := enqueueExtractionSession(tx, subject, id, false); err != nil {
			return err
		}
	}
	subject.PendingSessions = types.MemoryPendingSessions{}
	return nil
}

func hasPendingExtraction(tx *gorm.DB, scope interfaces.MemoryScope) (bool, error) {
	var rows []types.MemoryExtractionSession
	result := extractionRows(tx, scope).Select("session_id").Where("pending = ?", true).Limit(1).Find(&rows)
	return len(rows) > 0, result.Error
}

func (r *memoryRepository) HasPendingExtraction(ctx context.Context, scope interfaces.MemoryScope) (bool, error) {
	return hasPendingExtraction(r.db.WithContext(ctx), scope)
}

func (r *memoryRepository) EnqueuePendingSession(ctx context.Context, scope interfaces.MemoryScope, sessionID string, timeout time.Duration) (*types.MemorySubject, bool, error) {
	var snapshot types.MemorySubject
	shouldSend := false
	err := r.withSubject(ctx, scope, func(tx *gorm.DB, subject *types.MemorySubject) error {
		snapshot = *subject
		if err := importLegacySessions(tx, subject); err != nil {
			return err
		}
		if err := enqueueExtractionSession(tx, subject, sessionID, true); err != nil {
			return err
		}
		now := time.Now()
		running := subject.ExtractionState.LeaseID != "" && subject.ExtractionState.LeaseUntil.After(now)
		queued := subject.ExtractScheduledAt != nil && now.Sub(*subject.ExtractScheduledAt) < timeout
		if !running && !queued {
			pending, err := hasPendingExtraction(tx, scope)
			if err != nil {
				return err
			}
			if pending {
				subject.ExtractScheduledAt = &now
				shouldSend = true
			}
		}
		return saveExtractionState(tx, subject)
	})
	return &snapshot, shouldSend, err
}

func (r *memoryRepository) ClaimPendingSessions(ctx context.Context, scope interfaces.MemoryScope, fallbackSession, leaseID string, ttl time.Duration) (*types.MemoryExtractionBatch, error) {
	var batch *types.MemoryExtractionBatch
	err := r.withSubject(ctx, scope, func(tx *gorm.DB, subject *types.MemorySubject) error {
		now := time.Now()
		if subject.ExtractionState.LeaseID != "" && subject.ExtractionState.LeaseUntil.After(now) {
			batch = &types.MemoryExtractionBatch{RetryAt: subject.ExtractionState.LeaseUntil}
			return nil
		}
		if err := importLegacySessions(tx, subject); err != nil {
			return err
		}
		// Bootstrap legacy payloads once; a duplicate must not reactivate a drained row.
		if err := enqueueExtractionSession(tx, subject, fallbackSession, false); err != nil {
			return err
		}
		var sessions []types.MemoryExtractionSession
		if err := extractionRows(tx, scope).Where("pending = ?", true).
			Order("updated_at ASC, session_id ASC").
			Limit(types.MaxMemoryPendingSessions).Find(&sessions).Error; err != nil {
			return err
		}
		if len(sessions) == 0 {
			return saveExtractionState(tx, subject)
		}
		batch = &types.MemoryExtractionBatch{Sessions: sessions}
		subject.ExtractionState.LeaseID = leaseID
		subject.ExtractionState.LeaseUntil = now.Add(ttl)
		return saveExtractionState(tx, subject)
	})
	return batch, err
}

func validExtractionLease(subject *types.MemorySubject, leaseID string) bool {
	return subject.ExtractionState.LeaseID == leaseID && subject.ExtractionState.LeaseUntil.After(time.Now())
}

func (r *memoryRepository) CheckpointExtraction(ctx context.Context, scope interfaces.MemoryScope, leaseID string, session types.MemoryExtractionSession, cursor types.MemoryMessageCursor, drained bool) error {
	return r.withSubject(ctx, scope, func(tx *gorm.DB, subject *types.MemorySubject) error {
		if !validExtractionLease(subject, leaseID) {
			return types.ErrMemoryExtractionLeaseLost
		}
		var progress types.MemoryExtractionSession
		if err := extractionRows(tx, scope).Where("session_id = ?", session.SessionID).
			First(&progress).Error; err != nil {
			return err
		}
		if cursor.After(progress.Cursor) {
			progress.Cursor = cursor
		}
		// Updating just this row rotates unfinished work without rewriting the
		// subject's entire history. Completed cursors remain small indexed records.
		updates := map[string]interface{}{
			"cursor_at": progress.Cursor.At, "cursor_id": progress.Cursor.ID,
			"pending": !drained || progress.Revision != session.Revision, "updated_at": time.Now(),
		}
		if progress.FailedAt == nil {
			updates["failure_count"] = 0
			updates["failure_code"] = ""
		}
		return extractionRows(tx, scope).Where("session_id = ?", session.SessionID).Updates(updates).Error
	})
}

func (r *memoryRepository) RecordExtractionFailure(
	ctx context.Context, scope interfaces.MemoryScope, leaseID string, failure interfaces.MemoryExtractionFailure,
) (bool, error) {
	skip := false
	err := r.withSubject(ctx, scope, func(tx *gorm.DB, subject *types.MemorySubject) error {
		if !validExtractionLease(subject, leaseID) {
			return types.ErrMemoryExtractionLeaseLost
		}
		var progress types.MemoryExtractionSession
		if err := extractionRows(tx, scope).Where("session_id = ?", failure.Session.SessionID).
			First(&progress).Error; err != nil {
			return err
		}
		attempts := 1
		if progress.FailedFrom.At.Equal(progress.Cursor.At) && progress.FailedFrom.ID == progress.Cursor.ID {
			attempts += progress.FailureCount
		}
		skip = attempts >= 3
		updates := map[string]interface{}{
			"failure_count": attempts, "failure_code": failure.Code, "updated_at": time.Now(),
			"failed_at":      nil,
			"failed_from_at": progress.Cursor.At, "failed_from_id": progress.Cursor.ID,
			"failed_to_at": failure.End.At, "failed_to_id": failure.End.ID,
		}
		if skip {
			updates["failed_at"] = time.Now()
		}
		return extractionRows(tx, scope).Where("session_id = ?", failure.Session.SessionID).Updates(updates).Error
	})
	return skip, err
}

func (r *memoryRepository) FinishExtraction(ctx context.Context, scope interfaces.MemoryScope, leaseID string) error {
	return r.withSubject(ctx, scope, func(tx *gorm.DB, subject *types.MemorySubject) error {
		if subject.ExtractionState.LeaseID != leaseID {
			return types.ErrMemoryExtractionLeaseLost
		}
		subject.ExtractionState.LeaseID = ""
		subject.ExtractionState.LeaseUntil = time.Time{}
		subject.ExtractScheduledAt = nil
		if err := saveExtractionState(tx, subject); err != nil {
			return err
		}
		return tx.Model(subject).Update("last_extracted_at", time.Now()).Error
	})
}

func (r *memoryRepository) ReleaseExtractionSlot(ctx context.Context, scope interfaces.MemoryScope, leaseID string) error {
	return r.withSubject(ctx, scope, func(tx *gorm.DB, subject *types.MemorySubject) error {
		if subject.ExtractionState.LeaseID != leaseID {
			return nil
		}
		subject.ExtractionState.LeaseID = ""
		subject.ExtractionState.LeaseUntil = time.Time{}
		subject.ExtractScheduledAt = nil
		return saveExtractionState(tx, subject)
	})
}
