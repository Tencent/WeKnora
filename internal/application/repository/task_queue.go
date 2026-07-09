package repository

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// taskPendingOpsRepository implements interfaces.TaskPendingOpsRepository.
type taskPendingOpsRepository struct {
	db *gorm.DB
}

// NewTaskPendingOpsRepository constructs a GORM-backed implementation.
func NewTaskPendingOpsRepository(db *gorm.DB) interfaces.TaskPendingOpsRepository {
	return &taskPendingOpsRepository{db: db}
}

// Enqueue inserts a single op. Callers must populate TenantID/TaskType/
// Scope/ScopeID/Op (Payload optional). ID, FailCount default to zero;
// EnqueuedAt is filled with the DB-side default if left zero.
func (r *taskPendingOpsRepository) Enqueue(ctx context.Context, op *types.TaskPendingOp) error {
	if op == nil {
		return errors.New("task pending ops: nil op")
	}
	if op.TaskType == "" || op.Scope == "" || op.ScopeID == "" {
		return errors.New("task pending ops: task_type, scope, scope_id are required")
	}
	if op.Op == "" {
		return errors.New("task pending ops: op is required")
	}
	if len(op.Payload) == 0 {
		// Make sure the JSONB column never sees NULL — the schema sets a
		// default but explicit "{}" keeps the row uniform regardless of
		// driver-level default handling.
		op.Payload = []byte("{}")
	}
	return r.db.WithContext(ctx).Create(op).Error
}

// PeekBatch returns up to `limit` rows for the (task_type, scope, scope_id)
// tuple ordered by id ASC. Rows are not removed; callers must
// DeleteByIDs once they have been consumed (or IncrFailCount and leave
// them for the next pass). `limit` <= 0 falls back to 1; we clamp the
// upper bound generously so callers can pull large windows when they
// know the consumer can handle them.
func (r *taskPendingOpsRepository) PeekBatch(
	ctx context.Context,
	taskType, scope, scopeID string,
	limit int,
) ([]*types.TaskPendingOp, error) {
	if limit <= 0 {
		limit = 1
	}
	if limit > 1000 {
		limit = 1000
	}
	var ops []*types.TaskPendingOp
	if err := r.db.WithContext(ctx).
		Where("task_type = ? AND scope = ? AND scope_id = ?", taskType, scope, scopeID).
		Order("id ASC").
		Limit(limit).
		Find(&ops).Error; err != nil {
		return nil, err
	}
	return ops, nil
}

// ClaimBatch atomically claims eligible rows for the tuple, grouped by
// dedup_key. `limit` counts DISTINCT dedup_keys (i.e. documents), NOT rows:
// ALL eligible rows sharing a chosen dedup_key are claimed together and
// returned in the same batch. This is the invariant the concurrent wiki
// consumers rely on — a document with multiple queued ops (e.g. an ingest
// followed by a retract) must never be split across two concurrent batches,
// otherwise each batch's per-batch last-write-wins dedup can't collapse the
// pair and the two ops race (a stale ingest could resurrect a retracted doc).
//
// To uphold that invariant even for a sibling enqueued AFTER a batch already
// claimed the key (e.g. a retract arriving while the ingest is still in
// flight), a dedup_key that has ANY fresh claim (claimed_at >= staleBefore) is
// skipped ENTIRELY — not just its already-claimed rows. The late sibling waits
// for the holder to finish (which deletes the claimed rows, freeing the key) or
// for the claim to go stale. This serializes same-document ops across
// concurrent batches instead of letting them race on wall-clock completion.
//
// Eligibility = unclaimed (claimed_at IS NULL) OR stale claim
// (claimed_at < staleBefore), AND the key has no fresh claim. The whole thing
// runs in one transaction:
//
//   - Postgres: we lock the ANCHOR row (earliest eligible id) of each
//     candidate dedup_key with FOR UPDATE SKIP LOCKED. Because the anchor
//     uniquely represents its key, SKIP LOCKED hands concurrent claimers
//     DISJOINT key sets — a key whose anchor is already locked by another
//     in-flight claim is skipped entirely rather than half-claimed. We then
//     stamp every eligible row of the chosen keys and read them back.
//   - Other dialects (SQLite, used by unit tests / Lite mode): writes are
//     serialized by the single-writer engine, so a plain grouped SELECT +
//     UPDATE is already race-free.
//
// Rows are claimed by explicit id (only the eligible ones), so a freshly
// enqueued or still-in-flight sibling row of a chosen key is never handed
// out twice.
func (r *taskPendingOpsRepository) ClaimBatch(
	ctx context.Context,
	taskType, scope, scopeID string,
	limit int,
	staleBefore time.Time,
) ([]*types.TaskPendingOp, error) {
	if limit <= 0 {
		limit = 1
	}
	if limit > 1000 {
		limit = 1000
	}
	now := time.Now()
	var claimed []*types.TaskPendingOp
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 1. Pick up to `limit` distinct dedup_keys to claim, oldest first.
		//    Keys with a fresh claim are excluded WHOLESALE so a late sibling
		//    of an in-flight document never gets claimed on its own.
		var keys []string
		if tx.Dialector.Name() == "postgres" {
			// Lock the anchor (earliest eligible) row of each key with SKIP
			// LOCKED so concurrent claimers get disjoint KEY sets, then map
			// the locked anchors back to their dedup_keys. The NOT IN subquery
			// drops any key that still has a fresh (non-stale) claim.
			const anchorSQL = `
SELECT dedup_key FROM task_pending_ops
WHERE id IN (
	SELECT id FROM (
		SELECT id, ROW_NUMBER() OVER (PARTITION BY dedup_key ORDER BY id) AS rn
		FROM task_pending_ops
		WHERE task_type = ? AND scope = ? AND scope_id = ?
			AND (claimed_at IS NULL OR claimed_at < ?)
			AND dedup_key NOT IN (
				SELECT dedup_key FROM task_pending_ops
				WHERE task_type = ? AND scope = ? AND scope_id = ?
					AND claimed_at IS NOT NULL AND claimed_at >= ?
			)
	) anchors WHERE anchors.rn = 1
)
ORDER BY id ASC
LIMIT ?
FOR UPDATE SKIP LOCKED`
			if err := tx.Raw(anchorSQL,
				taskType, scope, scopeID, staleBefore,
				taskType, scope, scopeID, staleBefore,
				limit).
				Scan(&keys).Error; err != nil {
				return err
			}
		} else {
			freshKeys := tx.Model(&types.TaskPendingOp{}).
				Select("dedup_key").
				Where("task_type = ? AND scope = ? AND scope_id = ?", taskType, scope, scopeID).
				Where("claimed_at IS NOT NULL AND claimed_at >= ?", staleBefore)
			if err := tx.Model(&types.TaskPendingOp{}).
				Where("task_type = ? AND scope = ? AND scope_id = ?", taskType, scope, scopeID).
				Where("(claimed_at IS NULL OR claimed_at < ?)", staleBefore).
				Where("dedup_key NOT IN (?)", freshKeys).
				Group("dedup_key").
				Order("MIN(id) ASC").
				Limit(limit).
				Pluck("dedup_key", &keys).Error; err != nil {
				return err
			}
		}
		if len(keys) == 0 {
			return nil
		}

		// 2. Resolve the exact eligible rows for the chosen keys and claim
		//    them by id. Claiming by id (not by "dedup_key IN keys") means a
		//    sibling row that is still in flight elsewhere (claimed & fresh)
		//    is left untouched and never returned to this batch.
		var ids []int64
		if err := tx.Model(&types.TaskPendingOp{}).
			Where("task_type = ? AND scope = ? AND scope_id = ?", taskType, scope, scopeID).
			Where("dedup_key IN ?", keys).
			Where("(claimed_at IS NULL OR claimed_at < ?)", staleBefore).
			Order("id ASC").
			Pluck("id", &ids).Error; err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		if err := tx.Model(&types.TaskPendingOp{}).
			Where("id IN ?", ids).
			Update("claimed_at", now).Error; err != nil {
			return err
		}
		return tx.Where("id IN ?", ids).Order("id ASC").Find(&claimed).Error
	})
	if err != nil {
		return nil, err
	}
	return claimed, nil
}

// ReleaseByIDs clears claimed_at for the given rows, returning them to the
// unclaimed pool. Empty input is a no-op. Setting claimed_at back to NULL
// on a row that was never claimed is harmless.
func (r *taskPendingOpsRepository) ReleaseByIDs(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).
		Model(&types.TaskPendingOp{}).
		Where("id IN ?", ids).
		Update("claimed_at", nil).Error
}

// DeleteByIDs removes the given rows in one statement. Empty input is a
// no-op so the caller can invoke unconditionally at the end of a batch.
func (r *taskPendingOpsRepository) DeleteByIDs(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).
		Where("id IN ?", ids).
		Delete(&types.TaskPendingOp{}).Error
}

// IncrFailCount atomically bumps fail_count for one row and returns the
// new value. We use UPDATE ... RETURNING so the read+write happens in
// one round trip and races between concurrent IncrFailCount callers
// resolve to monotonic counts.
//
// A missing row returns (0, nil): the caller's ID may have been removed
// by a concurrent DeleteByIDs (e.g. dead-letter path), which is benign.
func (r *taskPendingOpsRepository) IncrFailCount(ctx context.Context, id int64) (int, error) {
	var newCount int
	err := r.db.WithContext(ctx).Raw(
		`UPDATE task_pending_ops SET fail_count = fail_count + 1 WHERE id = ? RETURNING fail_count`,
		id,
	).Scan(&newCount).Error
	if err != nil {
		return 0, err
	}
	return newCount, nil
}

// PendingCount returns how many rows are currently queued for the
// tuple. Covered by idx_task_pending_ops_scope.
func (r *taskPendingOpsRepository) PendingCount(
	ctx context.Context,
	taskType, scope, scopeID string,
) (int64, error) {
	var n int64
	if err := r.db.WithContext(ctx).
		Model(&types.TaskPendingOp{}).
		Where("task_type = ? AND scope = ? AND scope_id = ?", taskType, scope, scopeID).
		Count(&n).Error; err != nil {
		return 0, err
	}
	return n, nil
}

// DeleteByDedupKey drops rows in the tuple whose dedup_key matches.
// If `op` is non-empty, only rows with the matching op are dropped;
// otherwise every matching row is removed. Empty dedup_key is rejected
// to prevent accidentally wiping the entire queue for a KB.
//
// Used by:
//   - Wiki delete path: scrub queued WikiOpIngest entries for a
//     knowledge that is being deleted, while preserving WikiOpRetract
//     so the cleanup can still unlink pages.
//   - Wiki reparse path: same scrub of pending ingests so the new
//     parse can repopulate cleanly.
func (r *taskPendingOpsRepository) DeleteByDedupKey(
	ctx context.Context,
	taskType, scope, scopeID, dedupKey, op string,
) error {
	if dedupKey == "" {
		return fmt.Errorf("task pending ops: empty dedup_key in DeleteByDedupKey")
	}
	q := r.db.WithContext(ctx).
		Where("task_type = ? AND scope = ? AND scope_id = ? AND dedup_key = ?",
			taskType, scope, scopeID, dedupKey)
	if op != "" {
		q = q.Where("op = ?", op)
	}
	return q.Delete(&types.TaskPendingOp{}).Error
}

// taskDeadLetterRepository implements interfaces.TaskDeadLetterRepository.
type taskDeadLetterRepository struct {
	db *gorm.DB
}

// NewTaskDeadLetterRepository constructs a GORM-backed implementation.
func NewTaskDeadLetterRepository(db *gorm.DB) interfaces.TaskDeadLetterRepository {
	return &taskDeadLetterRepository{db: db}
}

// Insert records one dead letter. Best-effort caller: the asynq
// middleware swallows the error so a failed insert never masks the
// underlying task error.
func (r *taskDeadLetterRepository) Insert(ctx context.Context, dl *types.TaskDeadLetter) error {
	if dl == nil {
		return errors.New("task dead letters: nil entry")
	}
	if dl.TaskType == "" {
		return errors.New("task dead letters: task_type is required")
	}
	if dl.Scope == "" {
		dl.Scope = types.TaskScopeUnknown
	}
	if len(dl.Payload) == 0 {
		dl.Payload = []byte("{}")
	}
	return r.db.WithContext(ctx).Create(dl).Error
}

// ListByScope returns dead letters for (scope, scope_id) newest-first
// with a stringified id cursor. `limit` is clamped to [1, 200]. Empty
// nextCursor signals the tail.
func (r *taskDeadLetterRepository) ListByScope(
	ctx context.Context,
	scope, scopeID, cursor string,
	limit int,
) ([]*types.TaskDeadLetter, string, error) {
	if scope == "" || scopeID == "" {
		return nil, "", errors.New("task dead letters: scope and scope_id are required")
	}
	return r.list(ctx, cursor, limit, func(q *gorm.DB) *gorm.DB {
		return q.Where("scope = ? AND scope_id = ?", scope, scopeID)
	})
}

// ListByTaskType returns dead letters for the given task_type
// newest-first with a stringified id cursor. Same clamping rules.
func (r *taskDeadLetterRepository) ListByTaskType(
	ctx context.Context,
	taskType, cursor string,
	limit int,
) ([]*types.TaskDeadLetter, string, error) {
	if taskType == "" {
		return nil, "", errors.New("task dead letters: task_type is required")
	}
	return r.list(ctx, cursor, limit, func(q *gorm.DB) *gorm.DB {
		return q.Where("task_type = ?", taskType)
	})
}

// list is the shared cursor pagination implementation, parametrized by
// the caller-supplied filter. Mirrors wikiLogEntryRepository.List.
func (r *taskDeadLetterRepository) list(
	ctx context.Context,
	cursor string,
	limit int,
	filter func(*gorm.DB) *gorm.DB,
) ([]*types.TaskDeadLetter, string, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	q := r.db.WithContext(ctx).Order("id DESC").Limit(limit)
	q = filter(q)

	if cursor != "" {
		cursorID, err := strconv.ParseInt(cursor, 10, 64)
		if err != nil {
			return nil, "", fmt.Errorf("invalid cursor %q: %w", cursor, err)
		}
		q = q.Where("id < ?", cursorID)
	}

	var rows []*types.TaskDeadLetter
	if err := q.Find(&rows).Error; err != nil {
		return nil, "", err
	}

	nextCursor := ""
	if len(rows) == limit {
		nextCursor = strconv.FormatInt(rows[len(rows)-1].ID, 10)
	}
	return rows, nextCursor, nil
}

// DeleteByID drops a single dead letter row. Returns nil even if the
// row is already gone — operators issuing concurrent deletes shouldn't
// see spurious errors.
func (r *taskDeadLetterRepository) DeleteByID(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).
		Where("id = ?", id).
		Delete(&types.TaskDeadLetter{}).Error
}
