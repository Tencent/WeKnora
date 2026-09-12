package repository

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

func (r *memoryRepository) withSubject(ctx context.Context, scope interfaces.MemoryScope, fn func(*gorm.DB, *types.MemorySubject) error) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var subject types.MemorySubject
		if err := tx.Where("tenant_id = ? AND subject_id = ?", scope.TenantID, scope.SubjectID).
			Clauses(forUpdateClause()).First(&subject).Error; err != nil {
			return err
		}
		if subject.ExtractionState.Sessions == nil {
			subject.ExtractionState.Sessions = map[string]types.MemoryExtractionSession{}
		}
		return fn(tx, &subject)
	})
}

func saveExtractionState(tx *gorm.DB, subject *types.MemorySubject) error {
	return tx.Model(subject).Updates(map[string]interface{}{
		"extraction_state":     subject.ExtractionState,
		"pending_sessions":     subject.PendingSessions,
		"extract_scheduled_at": subject.ExtractScheduledAt,
		"updated_at":           time.Now(),
	}).Error
}

func (r *memoryRepository) EnqueuePendingSession(ctx context.Context, scope interfaces.MemoryScope, sessionID string, timeout time.Duration) (*types.MemorySubject, bool, error) {
	var snapshot types.MemorySubject
	shouldSend := false
	err := r.withSubject(ctx, scope, func(tx *gorm.DB, subject *types.MemorySubject) error {
		snapshot = *subject
		if sessionID != "" {
			progress := subject.ExtractionState.Sessions[sessionID]
			progress.Revision++
			subject.ExtractionState.Sessions[sessionID] = progress
			subject.PendingSessions = subject.PendingSessions.Append(sessionID)
		}
		now := time.Now()
		running := subject.ExtractionState.LeaseID != "" && subject.ExtractionState.LeaseUntil.After(now)
		queued := subject.ExtractScheduledAt != nil && now.Sub(*subject.ExtractScheduledAt) < timeout
		if !running && !queued && len(subject.PendingSessions) > 0 {
			subject.ExtractScheduledAt = &now
			shouldSend = true
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
		// Old queued payloads and direct replay callers may not have scheduled
		// through the new code. Bootstrap them once; duplicate deliveries after
		// completion must not put a drained session back into the queue.
		if _, known := subject.ExtractionState.Sessions[fallbackSession]; fallbackSession != "" && !known {
			subject.ExtractionState.Sessions[fallbackSession] = types.MemoryExtractionSession{Revision: 1}
			subject.PendingSessions = subject.PendingSessions.Append(fallbackSession)
		}
		if len(subject.PendingSessions) == 0 {
			return nil
		}
		batch = &types.MemoryExtractionBatch{}
		for _, id := range subject.PendingSessions {
			progress := subject.ExtractionState.Sessions[id]
			progress.SessionID = id
			batch.Sessions = append(batch.Sessions, progress)
			if len(batch.Sessions) >= types.MaxMemoryPendingSessions {
				break
			}
		}
		subject.ExtractionState.LeaseID = leaseID
		subject.ExtractionState.LeaseUntil = now.Add(ttl)
		return saveExtractionState(tx, subject)
	})
	return batch, err
}

func (r *memoryRepository) CheckpointExtraction(ctx context.Context, scope interfaces.MemoryScope, leaseID string, session types.MemoryExtractionSession, cursor types.MemoryMessageCursor, drained bool) error {
	return r.withSubject(ctx, scope, func(tx *gorm.DB, subject *types.MemorySubject) error {
		if subject.ExtractionState.LeaseID != leaseID || !subject.ExtractionState.LeaseUntil.After(time.Now()) {
			return types.ErrMemoryExtractionLeaseLost
		}
		progress := subject.ExtractionState.Sessions[session.SessionID]
		if cursor.After(progress.Cursor) {
			progress.Cursor = cursor
		}
		subject.ExtractionState.Sessions[session.SessionID] = progress
		pending := make(types.MemoryPendingSessions, 0, len(subject.PendingSessions))
		for _, id := range subject.PendingSessions {
			if id != session.SessionID {
				pending = append(pending, id)
			}
		}
		if !drained || progress.Revision != session.Revision {
			// Rotate unfinished sessions so a busy conversation cannot starve
			// another conversation indefinitely.
			pending = pending.Append(session.SessionID)
		}
		subject.PendingSessions = pending
		if err := saveExtractionState(tx, subject); err != nil {
			return err
		}
		// This legacy field is diagnostic only, and never moves backwards.
		if !cursor.At.IsZero() && (subject.ExtractCursor == nil || cursor.At.After(*subject.ExtractCursor)) {
			return tx.Model(subject).Update("extract_cursor", cursor.At).Error
		}
		return nil
	})
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
		// An old worker or a failed enqueue must not release a successor's lease.
		if subject.ExtractionState.LeaseID != leaseID {
			return nil
		}
		subject.ExtractionState.LeaseID = ""
		subject.ExtractionState.LeaseUntil = time.Time{}
		subject.ExtractScheduledAt = nil
		return saveExtractionState(tx, subject)
	})
}
