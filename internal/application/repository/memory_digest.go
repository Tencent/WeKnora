package repository

import (
	"context"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// GetDigest returns the consolidated profile, or (nil, nil) before the first
// consolidation has run.
func (r *memoryRepository) GetDigest(
	ctx context.Context, scope interfaces.MemoryScope,
) (*types.MemoryDigest, error) {
	var digest types.MemoryDigest
	err := r.scoped(ctx, scope).First(&digest).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &digest, nil
}

// AcquireDigestLease claims the right to rewrite the profile.
//
// Consolidation reads every selected episode and replaces one shared document,
// so two rewrites running at once do not merge — the later one silently
// discards the earlier one's work and bills for both calls. The lease is the
// same mechanism the extraction pipeline uses, for the same reason.
//
// Returns an empty lease id when someone else holds it.
func (r *memoryRepository) AcquireDigestLease(
	ctx context.Context, scope interfaces.MemoryScope, ttl time.Duration,
) (string, error) {
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	lease := uuid.New().String()
	now := time.Now()
	until := now.Add(ttl)

	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var digest types.MemoryDigest
		err := tx.Where("tenant_id = ? AND subject_id = ?", scope.TenantID, scope.SubjectID).
			Clauses(forUpdateClause()).First(&digest).Error
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			// First consolidation for this person: the row that will hold the
			// profile is created by the act of claiming it.
			return tx.Create(&types.MemoryDigest{
				TenantID: scope.TenantID, SubjectID: scope.SubjectID,
				LeaseID: lease, LeasedUntil: &until,
				CreatedAt: now, UpdatedAt: now,
			}).Error
		case err != nil:
			return err
		}
		if digest.LeaseID != "" && digest.LeasedUntil != nil && digest.LeasedUntil.After(now) {
			lease = ""
			return nil
		}
		return tx.Model(&digest).Updates(map[string]interface{}{
			"lease_id": lease, "leased_until": until, "updated_at": now,
		}).Error
	})
	if err != nil {
		return "", err
	}
	return lease, nil
}

// ReleaseDigestLease drops the claim without changing the profile, so a
// consolidation that failed or found nothing to do does not make the next one
// wait out the lease.
func (r *memoryRepository) ReleaseDigestLease(
	ctx context.Context, scope interfaces.MemoryScope, lease string,
) error {
	if lease == "" {
		return nil
	}
	return r.scoped(ctx, scope).Model(&types.MemoryDigest{}).
		Where("lease_id = ?", lease).
		Updates(map[string]interface{}{
			"lease_id": "", "leased_until": nil, "updated_at": time.Now(),
		}).Error
}

// SaveDigest installs a rewritten profile and returns its revision.
//
// The write is conditional on the lease, which is what makes a stale worker
// harmless: one whose lease expired and was taken over by another run finds its
// update matches no rows, and reports that rather than overwriting the profile
// that replaced it.
func (r *memoryRepository) SaveDigest(
	ctx context.Context, scope interfaces.MemoryScope, lease, body string,
) (int64, error) {
	body = types.SanitizeMemoryDocument(body, types.MemoryDigestMaxRunes)
	now := time.Now()
	var revision int64

	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var digest types.MemoryDigest
		err := tx.Where("tenant_id = ? AND subject_id = ?", scope.TenantID, scope.SubjectID).
			Clauses(forUpdateClause()).First(&digest).Error
		if err != nil {
			return err
		}
		if lease != "" && digest.LeaseID != lease {
			return types.ErrMemoryDigestLeaseLost
		}
		revision = digest.Revision + 1
		return tx.Model(&digest).Updates(map[string]interface{}{
			"body":          body,
			"previous_body": digest.Body,
			"revision":      revision,
			"generated_at":  now,
			// A rewrite has, by construction, taken the person's edits into
			// account: it was shown them and told not to undo them. Clearing
			// the marker stops every later rewrite from being warned about an
			// edit that has already been absorbed.
			"user_edited_at": nil,
			"lease_id":       "",
			"leased_until":   nil,
			"updated_at":     now,
		}).Error
	})
	if err != nil {
		return 0, err
	}
	return revision, nil
}

// SetDigestEpisodeCount records how many accounts the live profile was built
// from, for the manager to show.
func (r *memoryRepository) SetDigestEpisodeCount(
	ctx context.Context, scope interfaces.MemoryScope, count int,
) error {
	return r.scoped(ctx, scope).Model(&types.MemoryDigest{}).
		Updates(map[string]interface{}{"episode_count": count, "updated_at": time.Now()}).Error
}

// SaveUserDigest installs a profile the person wrote themselves.
//
// It bumps the revision like a rewrite does, so the episodes already folded in
// are not re-proposed, and it sets the edit marker so the next rewrite is told
// to respect what they changed instead of restoring it. An edit that a
// consolidation quietly reverted an hour later would be worse than no editor
// at all.
func (r *memoryRepository) SaveUserDigest(
	ctx context.Context, scope interfaces.MemoryScope, body string,
) (int64, error) {
	body = types.SanitizeMemoryDocument(body, types.MemoryDigestMaxRunes)
	now := time.Now()
	var revision int64

	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var digest types.MemoryDigest
		err := tx.Where("tenant_id = ? AND subject_id = ?", scope.TenantID, scope.SubjectID).
			Clauses(forUpdateClause()).First(&digest).Error
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			revision = 1
			return tx.Create(&types.MemoryDigest{
				TenantID: scope.TenantID, SubjectID: scope.SubjectID,
				Body: body, Revision: revision, UserEditedAt: &now,
				CreatedAt: now, UpdatedAt: now,
			}).Error
		case err != nil:
			return err
		}
		revision = digest.Revision + 1
		return tx.Model(&digest).Updates(map[string]interface{}{
			"body": body, "previous_body": digest.Body, "revision": revision,
			"user_edited_at": now, "updated_at": now,
		}).Error
	})
	if err != nil {
		return 0, err
	}
	return revision, nil
}

// DeleteDigest clears the profile. The episodes stay: this asks for the
// injected summary to be rebuilt, not for the person's history to be erased.
func (r *memoryRepository) DeleteDigest(
	ctx context.Context, scope interfaces.MemoryScope,
) error {
	return r.scoped(ctx, scope).Delete(&types.MemoryDigest{}).Error
}

// ---------------------------------------------------------------------------
// Verbatim notes
// ---------------------------------------------------------------------------

// AddNote records something the user asked to remember, in their words.
//
// Deduplicated on the exact text so that asking twice does not put the same
// line in the prompt twice, and so an explicit request is idempotent under the
// retries the extraction queue can produce. Saying it again refreshes the
// timestamp, which is how it stays inside the injection cap.
func (r *memoryRepository) AddNote(
	ctx context.Context, scope interfaces.MemoryScope, note *types.MemoryNote,
) error {
	if note == nil {
		return nil
	}
	note.Content = types.SanitizeMemoryNote(note.Content)
	if note.Content == "" {
		return nil
	}
	note.TenantID, note.SubjectID = scope.TenantID, scope.SubjectID
	now := time.Now()

	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing types.MemoryNote
		err := tx.Where("tenant_id = ? AND subject_id = ? AND content = ?",
			scope.TenantID, scope.SubjectID, note.Content).
			Clauses(forUpdateClause()).First(&existing).Error
		switch {
		case err == nil:
			note.ID = existing.ID
			return tx.Model(&existing).Updates(map[string]interface{}{
				"source_session_id": note.SourceSessionID,
				"source_message_id": note.SourceMessageID,
				"updated_at":        now,
			}).Error
		case errors.Is(err, gorm.ErrRecordNotFound):
			if note.ID == "" {
				note.ID = uuid.New().String()
			}
			note.CreatedAt, note.UpdatedAt = now, now
			return tx.Create(note).Error
		default:
			return err
		}
	})
}

// ListNotes returns the user's own words, newest first, bounded.
func (r *memoryRepository) ListNotes(
	ctx context.Context, scope interfaces.MemoryScope, limit int,
) ([]*types.MemoryNote, error) {
	if limit <= 0 {
		limit = types.MemoryNotesMaxItems
	}
	var notes []*types.MemoryNote
	err := r.scoped(ctx, scope).Order("created_at DESC, id DESC").Limit(limit).Find(&notes).Error
	if err != nil {
		return nil, err
	}
	return notes, nil
}

// GetNote returns one note, or (nil, nil) when the id belongs to another
// subject or to nobody.
func (r *memoryRepository) GetNote(
	ctx context.Context, scope interfaces.MemoryScope, id string,
) (*types.MemoryNote, error) {
	var note types.MemoryNote
	err := r.scoped(ctx, scope).Where("id = ?", id).First(&note).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &note, nil
}

// CountNotes reports how many notes the subject holds, which is what the
// per-subject cap is enforced against.
func (r *memoryRepository) CountNotes(
	ctx context.Context, scope interfaces.MemoryScope,
) (int64, error) {
	var count int64
	err := r.scoped(ctx, scope).Model(&types.MemoryNote{}).Count(&count).Error
	return count, err
}

// DeleteNote forgets one explicit instruction.
func (r *memoryRepository) DeleteNote(
	ctx context.Context, scope interfaces.MemoryScope, id string,
) error {
	return r.scoped(ctx, scope).Where("id = ?", id).Delete(&types.MemoryNote{}).Error
}

// DeleteAllNotes clears the scope and reports how many notes went.
func (r *memoryRepository) DeleteAllNotes(
	ctx context.Context, scope interfaces.MemoryScope,
) (int64, error) {
	result := r.scoped(ctx, scope).Delete(&types.MemoryNote{})
	return result.RowsAffected, result.Error
}
