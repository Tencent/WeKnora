package repository

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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
// Scope/ScopeID/Op (Payload optional). ID and FailCount default to zero;
// EnqueuedAt is filled with the current UTC time if left zero.
func (r *taskPendingOpsRepository) Enqueue(ctx context.Context, op *types.TaskPendingOp) error {
	if err := preparePendingOp(op); err != nil {
		return err
	}
	return r.db.WithContext(ctx).Create(op).Error
}

func preparePendingOp(op *types.TaskPendingOp) error {
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
	if op.EnqueuedAt.IsZero() {
		// GORM includes zero time.Time fields in INSERT statements instead of
		// letting the database default run. MySQL strict mode rejects year
		// zero, so populate the application-wide UTC convention explicitly.
		op.EnqueuedAt = time.Now().UTC()
	}
	return nil
}

// EnqueueIfKnowledgeBaseActive prevents detached wiki cleanup from writing new
// durable work after a KB was soft-deleted. On databases with row locking
// (PostgreSQL and MySQL), the share lock
// serializes this check+insert transaction against the row update performed by
// soft deletion: whichever operation acquires the row first determines the
// order, and the deletion path's subsequent scope scrub removes any insert
// that committed before it.
func (r *taskPendingOpsRepository) EnqueueIfKnowledgeBaseActive(
	ctx context.Context,
	op *types.TaskPendingOp,
) (bool, error) {
	if err := preparePendingOp(op); err != nil {
		return false, err
	}
	if op.Scope != types.TaskScopeKnowledgeBase {
		return false, errors.New("task pending ops: guarded enqueue requires knowledge_base scope")
	}
	accepted := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		query := tx.Model(&types.KnowledgeBase{}).
			Select("id").
			Where("id = ? AND tenant_id = ?", op.ScopeID, op.TenantID)
		if supportsRowLevelLocking(tx) {
			query = query.Clauses(clause.Locking{Strength: "SHARE"})
		}
		var kb types.KnowledgeBase
		if err := query.Take(&kb).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		if err := tx.Create(op).Error; err != nil {
			return err
		}
		accepted = true
		return nil
	})
	return accepted, err
}

// SeedKnowledgeFinalizingWithPendingOp commits the finalizing counter and the
// durable operation that owns one slot in the same transaction. This closes
// the crash window where a knowledge row could enter finalizing before its
// Wiki operation existed.
func (r *taskPendingOpsRepository) SeedKnowledgeFinalizingWithPendingOp(
	ctx context.Context,
	knowledgeID string,
	expectedSubtasks int,
	op *types.TaskPendingOp,
) (bool, error) {
	if knowledgeID == "" {
		return false, errors.New("task pending ops: knowledge_id is required")
	}
	if expectedSubtasks <= 0 {
		return false, errors.New("task pending ops: expected_subtasks must be positive")
	}
	if err := preparePendingOp(op); err != nil {
		return false, err
	}
	if op.Scope != types.TaskScopeKnowledgeBase {
		return false, errors.New("task pending ops: finalizing seed requires knowledge_base scope")
	}

	promoted := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		query := tx.Model(&types.KnowledgeBase{}).
			Select("id").
			Where("id = ? AND tenant_id = ?", op.ScopeID, op.TenantID)
		if supportsRowLevelLocking(tx) {
			query = query.Clauses(clause.Locking{Strength: "SHARE"})
		}
		var kb types.KnowledgeBase
		if err := query.Take(&kb).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}

		res := tx.Model(&types.Knowledge{}).
			Where(
				"id = ? AND tenant_id = ? AND knowledge_base_id = ? AND parse_status = ?",
				knowledgeID, op.TenantID, op.ScopeID, types.ParseStatusProcessing,
			).
			Updates(map[string]interface{}{
				"parse_status":           types.ParseStatusFinalizing,
				"pending_subtasks_count": expectedSubtasks,
				"updated_at":             time.Now(),
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return nil
		}
		if err := tx.Create(op).Error; err != nil {
			return err
		}
		promoted = true
		return nil
	})
	return promoted, err
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
//   - MySQL: one conditional UPDATE stamps the selected key groups with a
//     per-claim token. InnoDB re-checks claimed_at after lock acquisition, so
//     concurrent transactions cannot both own the same rows; the token selects
//     only the rows actually won by this transaction.
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
		if isMySQL(tx) {
			// MySQL does not reliably carry FOR UPDATE SKIP LOCKED through
			// the window-function derived table used to choose one anchor per
			// dedup key. Under contention two transactions can therefore read
			// the same anchors. Claim with one conditional UPDATE instead:
			// InnoDB re-checks claimed_at after acquiring each update lock, and
			// the unique token identifies exactly the rows this transaction won.
			claimToken := uuid.NewString()
			const claimSQL = `
UPDATE task_pending_ops AS target
JOIN (
	SELECT dedup_key
	FROM (
		SELECT eligible.dedup_key, MIN(eligible.id) AS anchor_id
		FROM task_pending_ops AS eligible
		WHERE eligible.task_type = ? AND eligible.scope = ? AND eligible.scope_id = ?
			AND (eligible.claimed_at IS NULL OR eligible.claimed_at < ?)
			AND NOT EXISTS (
				SELECT 1 FROM task_pending_ops AS fresh
				WHERE fresh.task_type = eligible.task_type
					AND fresh.scope = eligible.scope
					AND fresh.scope_id = eligible.scope_id
					AND fresh.dedup_key = eligible.dedup_key
					AND fresh.claimed_at IS NOT NULL
					AND fresh.claimed_at >= ?
			)
		GROUP BY eligible.dedup_key
		ORDER BY anchor_id
		LIMIT ?
	) AS candidates
) AS selected ON selected.dedup_key = target.dedup_key
SET target.claimed_at = ?, target.claim_token = ?
WHERE target.task_type = ? AND target.scope = ? AND target.scope_id = ?
	AND (target.claimed_at IS NULL OR target.claimed_at < ?)`
			if err := tx.Exec(
				claimSQL,
				taskType, scope, scopeID, staleBefore, staleBefore, limit,
				now, claimToken,
				taskType, scope, scopeID, staleBefore,
			).Error; err != nil {
				return err
			}
			return tx.Raw(`
SELECT id, tenant_id, task_type, scope, scope_id, op, dedup_key, payload,
	fail_count, enqueued_at, claimed_at
FROM task_pending_ops
WHERE claim_token = ?
ORDER BY id ASC`, claimToken).Scan(&claimed).Error
		}

		// 1. Pick up to `limit` distinct dedup_keys to claim, oldest first.
		//    Keys with a fresh claim are excluded WHOLESALE so a late sibling
		//    of an in-flight document never gets claimed on its own.
		var keys []string
		if isPostgres(tx) {
			// Lock the anchor (earliest eligible) row of each key with SKIP
			// LOCKED so concurrent claimers get disjoint KEY sets, then map
			// the locked anchors back to their dedup_keys.
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
	query := r.db.WithContext(ctx).
		Model(&types.TaskPendingOp{}).
		Where("id IN ?", ids)
	if isMySQL(r.db) {
		return query.Updates(map[string]interface{}{
			"claimed_at":  nil,
			"claim_token": nil,
		}).Error
	}
	return query.Update("claimed_at", nil).Error
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

// DeleteByScope removes every pending operation owned by a scope, regardless
// of task type. Both scope fields are required so a malformed lifecycle call
// can never turn into an unbounded queue deletion.
func (r *taskPendingOpsRepository) DeleteByScope(ctx context.Context, scope, scopeID string) error {
	if scope == "" || scopeID == "" {
		return errors.New("task pending ops: scope and scope_id are required")
	}
	return r.db.WithContext(ctx).
		Where("scope = ? AND scope_id = ?", scope, scopeID).
		Delete(&types.TaskPendingOp{}).Error
}

// IncrFailCount atomically bumps fail_count for one row and returns the
// new value. PostgreSQL/SQLite use UPDATE ... RETURNING; MySQL uses a
// SELECT ... FOR UPDATE transaction because it does not support UPDATE
// RETURNING.
//
// A missing row returns (0, nil): the caller's ID may have been removed
// by a concurrent DeleteByIDs (e.g. dead-letter path), which is benign.
func (r *taskPendingOpsRepository) IncrFailCount(ctx context.Context, id int64) (int, error) {
	var newCount int
	if isMySQL(r.db) {
		err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var op types.TaskPendingOp
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Select("id", "fail_count").
				Where("id = ?", id).
				First(&op).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return nil
				}
				return err
			}
			newCount = op.FailCount + 1
			return tx.Model(&types.TaskPendingOp{}).
				Where("id = ?", id).
				Update("fail_count", newCount).Error
		})
		if err != nil {
			return 0, err
		}
		return newCount, nil
	}
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
	if dl.FailedAt.IsZero() {
		// See preparePendingOp: an explicit zero time bypasses the SQL default
		// and is invalid under the required MySQL strict mode.
		dl.FailedAt = time.Now().UTC()
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
