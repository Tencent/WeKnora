package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var _ interfaces.DocumentSchedulerRepository = (*taskPendingOpsRepository)(nil)

// documentTransaction takes the database-owned cursor lock before reading any
// admission state. SQLite's first statement must WRITE to avoid deferred-lock
// upgrade races. No transaction survives into document/model processing.
func (r *taskPendingOpsRepository) documentTransaction(ctx context.Context, fn func(*gorm.DB) error) error {
	for attempt := 0; ; attempt++ {
		err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			res := tx.Exec(`UPDATE document_scheduler_state SET last_tenant_id=last_tenant_id WHERE id=1`)
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected != 1 {
				return errors.New("document scheduler cursor missing")
			}
			return fn(tx)
		})
		if err == nil || r.db.Name() != "sqlite" || attempt >= 7 ||
			(!strings.Contains(err.Error(), "locked") && !strings.Contains(err.Error(), "busy")) {
			return err
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// EnqueueDocument coalesces the same parse attempt without resetting its durable
// retry budget. New attempts replace pending work but never remove live leases.
func (r *taskPendingOpsRepository) EnqueueDocument(ctx context.Context, op *types.TaskPendingOp) (bool, error) {
	return r.EnqueueDocumentGeneration(ctx, op, "")
}

// EnqueueDocumentGeneration admits only the current explicit producer token, or
// an original legacy generation that has never advanced.
func (r *taskPendingOpsRepository) EnqueueDocumentGeneration(
	ctx context.Context, op *types.TaskPendingOp, token string,
) (bool, error) {
	if err := preparePendingOp(op); err != nil {
		return false, err
	}
	if op.TaskType != types.TypeDocumentProcess || op.Scope != types.TaskScopeKnowledgeBase || op.DedupKey == "" {
		return false, errors.New("document scheduler: invalid document scope")
	}
	if op.FailCount < 0 || op.FailCount > 3 {
		return false, errors.New("document scheduler: retry count outside execution budget")
	}
	var payload types.DocumentProcessPayload
	if err := json.Unmarshal(op.Payload, &payload); err != nil {
		return false, err
	}
	if payload.TenantID != op.TenantID || payload.KnowledgeID != op.DedupKey ||
		payload.KnowledgeBaseID != op.ScopeID {
		return false, errors.New("document scheduler: payload ownership mismatch")
	}
	accepted := false
	err := r.documentTransaction(ctx, func(tx *gorm.DB) error {
		accepted = false
		query := tx.Model(&types.KnowledgeBase{}).Select("id").Where("id=? AND tenant_id=?", op.ScopeID, op.TenantID)
		if tx.Name() == "postgres" {
			query = query.Clauses(clause.Locking{Strength: "SHARE"})
		}
		var kb types.KnowledgeBase
		if err := query.Take(&kb).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		tenantQuery := tx.Model(&types.Tenant{}).Select("id").Where("id=?", op.TenantID)
		if tx.Name() == "postgres" {
			tenantQuery = tenantQuery.Clauses(clause.Locking{Strength: "SHARE"})
		}
		var tenant types.Tenant
		if err := tenantQuery.Take(&tenant).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		// A move changes the scope of the same knowledge ID. Validate its current
		// scope before considering old pending work, including higher attempts.
		knowledgeQuery := tx.Model(&types.Knowledge{}).Select("id", "parse_status").
			Where("id=? AND tenant_id=? AND knowledge_base_id=?", op.DedupKey, op.TenantID, op.ScopeID)
		if tx.Name() == "postgres" {
			knowledgeQuery = knowledgeQuery.Clauses(clause.Locking{Strength: "SHARE"})
		}
		var knowledge types.Knowledge
		if err := knowledgeQuery.Take(&knowledge).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		var existing []types.TaskPendingOp
		generationToken := token
		if generationToken == "" {
			var err error
			generationToken, err = initialDocumentGeneration(tx, op.TenantID, op.DedupKey, op.ScopeID)
			if err != nil || generationToken == "" {
				return err
			}
		} else {
			generation, err := matchDocumentGeneration(tx, op.TenantID, op.DedupKey, generationToken)
			if err != nil || generation == nil {
				return err
			}
			if generation.ScopeID != op.ScopeID {
				return nil
			}
		}
		if err := documentRows(tx).Where("tenant_id=? AND dedup_key=?", op.TenantID, op.DedupKey).
			Where("generation=?", generationToken).
			Order("id DESC").Find(&existing).Error; err != nil {
			return err
		}
		for _, row := range existing {
			var old types.DocumentProcessPayload
			if err := json.Unmarshal(row.Payload, &old); err != nil {
				return err
			}
			if row.ScopeID == op.ScopeID && old.Attempt >= payload.Attempt {
				*op = row
				accepted = true
				return nil
			}
		}
		// A legacy queue redelivery must not buy another budget after terminal
		// completion, including during the status transition into a new reparse.
		var terminal []types.TaskDeadLetter
		if err := tx.Where("tenant_id=? AND task_type=? AND related_id=? AND scope_id=?",
			op.TenantID, types.TypeDocumentProcess, op.DedupKey, op.ScopeID).
			Where("generation=?", generationToken).Find(&terminal).Error; err != nil {
			return err
		}
		for _, letter := range terminal {
			var old types.DocumentProcessPayload
			if err := json.Unmarshal(letter.Payload, &old); err != nil {
				return err
			}
			if old.Attempt >= payload.Attempt {
				return nil
			}
		}
		if knowledge.ParseStatus != types.ParseStatusPending && knowledge.ParseStatus != types.ParseStatusProcessing &&
			(knowledge.ParseStatus != types.ParseStatusFailed || op.FailCount <= 0) {
			return nil
		}
		if err := documentRows(tx).Where("tenant_id=? AND dedup_key=?", op.TenantID, op.DedupKey).
			Delete(&types.TaskPendingOp{}).Error; err != nil {
			return err
		}
		op.ID = 0
		op.ClaimedAt = nil
		if err := tx.Create(op).Error; err != nil {
			return err
		}
		if err := tx.Model(&types.TaskPendingOp{}).Where("id=?", op.ID).
			Update("generation", generationToken).Error; err != nil {
			return err
		}
		accepted = true
		return nil
	})
	return accepted, err
}

func documentRows(tx *gorm.DB) *gorm.DB {
	return tx.Model(&types.TaskPendingOp{}).
		Where("task_type=? AND scope=?", types.TypeDocumentProcess, types.TaskScopeKnowledgeBase)
}

// ClaimDocument rotates over eligible TENANTS, then chooses their oldest ready
// document across every KB. Independent leases, not pending rows, own capacity.
func (r *taskPendingOpsRepository) ClaimDocument(
	ctx context.Context, limit int, duration time.Duration,
) (*types.DocumentClaim, error) {
	if limit <= 0 || duration <= 0 {
		return nil, errors.New("document scheduler: positive cap and lease duration required")
	}
	var result *types.DocumentClaim
	err := r.documentTransaction(ctx, func(tx *gorm.DB) error {
		result = nil
		now := time.Now()
		var last uint64
		if err := tx.Raw(`SELECT last_tenant_id FROM document_scheduler_state WHERE id=1`).
			Scan(&last).Error; err != nil {
			return err
		}
		const eligible = `task_type=? AND scope=? AND (available_at IS NULL OR available_at<=?)
 AND EXISTS (SELECT 1 FROM document_generations g WHERE g.tenant_id=task_pending_ops.tenant_id
  AND g.knowledge_id=task_pending_ops.dedup_key AND g.scope_id=task_pending_ops.scope_id
  AND g.token=task_pending_ops.generation)
 AND NOT EXISTS (SELECT 1 FROM document_execution_leases l
   WHERE l.tenant_id=task_pending_ops.tenant_id AND l.knowledge_id=task_pending_ops.dedup_key)
 AND (SELECT COUNT(*) FROM document_execution_leases l WHERE l.tenant_id=task_pending_ops.tenant_id) < ?
 AND EXISTS (SELECT 1 FROM tenants t WHERE t.id=task_pending_ops.tenant_id AND t.deleted_at IS NULL)
 AND EXISTS (SELECT 1 FROM knowledge_bases b WHERE b.id=task_pending_ops.scope_id
   AND b.tenant_id=task_pending_ops.tenant_id AND b.deleted_at IS NULL)
 AND EXISTS (SELECT 1 FROM knowledges k WHERE k.id=task_pending_ops.dedup_key
   AND k.tenant_id=task_pending_ops.tenant_id AND k.knowledge_base_id=task_pending_ops.scope_id
   AND k.deleted_at IS NULL AND k.parse_status IN ('pending','processing','failed'))`
		var tenants []uint64
		if err := tx.Model(&types.TaskPendingOp{}).
			Where(eligible, types.TypeDocumentProcess, types.TaskScopeKnowledgeBase, now, limit).
			Distinct("tenant_id").Order("tenant_id").Pluck("tenant_id", &tenants).Error; err != nil {
			return err
		}
		if len(tenants) == 0 {
			return nil
		}
		selected := tenants[0]
		for _, tenant := range tenants {
			if tenant > last {
				selected = tenant
				break
			}
		}
		var op types.TaskPendingOp
		if err := tx.Where(eligible, types.TypeDocumentProcess, types.TaskScopeKnowledgeBase, now, limit).
			Where("tenant_id=?", selected).Order("fail_count,id").First(&op).Error; err != nil {
			return err
		}
		owner := uuid.NewString()
		lease := types.DocumentExecutionLease{
			TenantID: selected, KnowledgeID: op.DedupKey, PendingID: op.ID, Owner: owner, ExpiresAt: now.Add(duration),
		}
		if err := tx.Create(&lease).Error; err != nil {
			return err
		}
		if err := tx.Model(&types.TaskPendingOp{}).Where("id=?", op.ID).Update("claimed_at", now).Error; err != nil {
			return err
		}
		if err := tx.Exec(`UPDATE document_scheduler_state SET last_tenant_id=? WHERE id=1`,
			selected).Error; err != nil {
			return err
		}
		op.ClaimedAt = &now
		generation, err := pendingDocumentGeneration(tx, op.ID)
		if err != nil {
			return err
		}
		result = &types.DocumentClaim{Op: &op, Owner: owner, Generation: generation}
		return nil
	})
	return result, err
}

// RenewDocument loses ownership on expiry or pending-row removal, but does not
// release that lease: the worker must exit first, or stale recovery must run.
func (r *taskPendingOpsRepository) RenewDocument(
	ctx context.Context, c *types.DocumentClaim, duration time.Duration,
) (bool, error) {
	if c == nil || c.Op == nil || duration <= 0 {
		return false, errors.New("document scheduler: invalid renewal")
	}
	renewed := false
	err := r.documentTransaction(ctx, func(tx *gorm.DB) error {
		renewed = false
		now := time.Now()
		res := tx.Model(&types.DocumentExecutionLease{}).
			Where("tenant_id=? AND knowledge_id=? AND pending_id=? AND owner=? AND expires_at>?",
				c.Op.TenantID, c.Op.DedupKey, c.Op.ID, c.Owner, now).
			Where("EXISTS (SELECT 1 FROM task_pending_ops p WHERE p.id=document_execution_leases.pending_id)").
			Update("expires_at", now.Add(duration))
		renewed = res.RowsAffected == 1
		return res.Error
	})
	return renewed, err
}

// FinishDocument atomically fences the owner, updates the pending retry state,
// archives terminal failures with their original payload, and releases capacity.
func (r *taskPendingOpsRepository) FinishDocument(
	ctx context.Context, c *types.DocumentClaim, outcome string, next time.Time, lastError string,
) (bool, error) {
	if c == nil || c.Op == nil {
		return false, errors.New("document scheduler: nil claim")
	}
	switch outcome {
	case "success", "retry", "terminal", "release":
	default:
		return false, fmt.Errorf("document scheduler: invalid outcome %q", outcome)
	}
	settled := false
	err := r.documentTransaction(ctx, func(tx *gorm.DB) error {
		settled = false
		var lease types.DocumentExecutionLease
		err := tx.Where("tenant_id=? AND knowledge_id=? AND pending_id=? AND owner=?",
			c.Op.TenantID, c.Op.DedupKey, c.Op.ID, c.Owner).Take(&lease).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		// Lock original-row settlement against concurrent removal. The persisted
		// attempt guard below protects the pre-enqueue window of a newer reparse.
		query := documentRows(tx).Select("id").Where("id=?", lease.PendingID)
		if tx.Name() == "postgres" {
			query = query.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		var op types.TaskPendingOp
		err = query.Take(&op).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		exists := err == nil
		if exists {
			token, err := pendingDocumentGeneration(tx, lease.PendingID)
			if err != nil {
				return err
			}
			generation, err := matchDocumentGeneration(tx, lease.TenantID, lease.KnowledgeID, token)
			if err != nil {
				return err
			}
			exists = generation != nil && (c.Generation == "" || c.Generation == token)
		}
		if err := finishDocumentWithinTx(tx, &lease, outcome, next, lastError); err != nil {
			return err
		}
		settled = exists
		return nil
	})
	return settled && err == nil, err
}

func finishDocumentWithinTx(
	tx *gorm.DB, lease *types.DocumentExecutionLease, outcome string, next time.Time, lastError string,
) error {
	var op types.TaskPendingOp
	query := documentRows(tx).Where("id=? AND tenant_id=? AND dedup_key=?",
		lease.PendingID, lease.TenantID, lease.KnowledgeID)
	if tx.Name() == "postgres" {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	err := query.Take(&op).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if err == nil {
		generationToken, err := pendingDocumentGeneration(tx, op.ID)
		if err != nil {
			return err
		}
		generation, err := matchDocumentGeneration(tx, op.TenantID, op.DedupKey, generationToken)
		if err != nil {
			return err
		}
		if generation == nil || generation.ScopeID != op.ScopeID {
			outcome = "success"
		}
		switch outcome {
		case "retry", "terminal":
			op.FailCount++
			if outcome == "terminal" || op.FailCount >= 4 {
				letter := types.TaskDeadLetter{
					TenantID: op.TenantID, TaskType: op.TaskType, Scope: op.Scope, ScopeID: op.ScopeID,
					RelatedID: op.DedupKey, Payload: op.Payload, LastError: lastError,
					FailCount: op.FailCount, FailedAt: time.Now(),
				}
				if err := tx.Create(&letter).Error; err != nil {
					return err
				}
				if err := tx.Model(&types.TaskDeadLetter{}).Where("id=?", letter.ID).
					Update("generation", generationToken).Error; err != nil {
					return err
				}
				if err := failDocumentAttempt(tx, &op, lastError); err != nil {
					return err
				}
				if err := tx.Delete(&types.TaskPendingOp{}, op.ID).Error; err != nil {
					return err
				}
			} else {
				if err := tx.Model(&types.TaskPendingOp{}).Where("id=?", op.ID).
					Updates(map[string]interface{}{
						"fail_count": op.FailCount, "claimed_at": nil, "available_at": next,
					}).Error; err != nil {
					return err
				}
			}
		case "success":
			if err := tx.Delete(&types.TaskPendingOp{}, op.ID).Error; err != nil {
				return err
			}
		case "release":
			if err := tx.Model(&types.TaskPendingOp{}).Where("id=?", op.ID).
				Updates(map[string]interface{}{"claimed_at": nil, "available_at": next}).Error; err != nil {
				return err
			}
		}
	}
	return tx.Where("tenant_id=? AND knowledge_id=? AND owner=?", lease.TenantID, lease.KnowledgeID, lease.Owner).
		Delete(&types.DocumentExecutionLease{}).Error
}

// failDocumentAttempt fences status mutation with the persisted parse attempt.
// Pending-row ownership alone is insufficient during a reparse's pre-enqueue
// window. The caller has already checked the durable producer generation, which
// also fences legacy payloads whose optional trace attempt is zero.
func failDocumentAttempt(tx *gorm.DB, op *types.TaskPendingOp, lastError string) error {
	var payload types.DocumentProcessPayload
	if err := json.Unmarshal(op.Payload, &payload); err != nil {
		return nil
	}
	query := tx.Model(&types.Knowledge{}).Select("id").
		Where("id=? AND tenant_id=? AND knowledge_base_id=?", op.DedupKey, op.TenantID, op.ScopeID)
	if tx.Name() == "postgres" {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var knowledge types.Knowledge
	if err := query.Take(&knowledge).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	// The separate statement after the row lock sees a reparse that already
	// reset status; a later reset must wait for our transaction to finish.
	update := tx.Model(&types.Knowledge{}).
		Where("id=? AND tenant_id=? AND knowledge_base_id=?", op.DedupKey, op.TenantID, op.ScopeID).
		Where("parse_status IN ?", []string{types.ParseStatusPending, types.ParseStatusProcessing})
	if payload.Attempt > 0 {
		update = update.Where(`NOT EXISTS (SELECT 1 FROM knowledge_processing_spans s WHERE s.knowledge_id=knowledges.id
 AND s.attempt>?)`, payload.Attempt)
	}
	return update.Updates(map[string]interface{}{
		"parse_status": types.ParseStatusFailed, "error_message": lastError, "updated_at": time.Now(),
	}).Error
}

// SnapshotDocumentPending captures immutable row IDs before cancellation starts.
func (r *taskPendingOpsRepository) SnapshotDocumentPending(
	ctx context.Context, tenantID uint64, knowledgeID string,
) ([]int64, error) {
	if tenantID == 0 || knowledgeID == "" {
		return nil, errors.New("document scheduler: tenant and knowledge are required for cleanup")
	}
	var ids []int64
	err := r.documentTransaction(ctx, func(tx *gorm.DB) error {
		ids = nil
		return documentRows(tx).Where("tenant_id=? AND dedup_key=?", tenantID, knowledgeID).
			Order("id").Pluck("id", &ids).Error
	})
	return ids, err
}

// DeleteDocumentPending removes only captured rows; independent live leases and
// any rows created by a concurrent reparse remain untouched.
func (r *taskPendingOpsRepository) DeleteDocumentPending(
	ctx context.Context, tenantID uint64, knowledgeID string, ids []int64,
) error {
	if tenantID == 0 || knowledgeID == "" {
		return errors.New("document scheduler: tenant and knowledge are required for cleanup")
	}
	if len(ids) == 0 {
		return nil
	}
	return r.documentTransaction(ctx, func(tx *gorm.DB) error {
		return documentRows(tx).Where("tenant_id=? AND dedup_key=? AND id IN ?", tenantID, knowledgeID, ids).
			Delete(&types.TaskPendingOp{}).Error
	})
}

// RecoverDocuments removes unreachable work and charges abandoned executions.
// A lease survives deletion of its pending row until the owner exits or expires.
func (r *taskPendingOpsRepository) RecoverDocuments(ctx context.Context) error {
	_, err := r.RecoverDocumentFailures(ctx)
	return err
}

// RecoverDocumentFailures reports only failures newly committed by this recovery
// pass. Historic archives are never replayed into a newer parse attempt's trace.
func (r *taskPendingOpsRepository) RecoverDocumentFailures(ctx context.Context) ([]*types.TaskPendingOp, error) {
	var failures []*types.TaskPendingOp
	err := r.documentTransaction(ctx, func(tx *gorm.DB) error {
		failures = nil
		now := time.Now()
		const obsolete = `NOT EXISTS (SELECT 1 FROM document_generations g WHERE g.tenant_id=task_pending_ops.tenant_id
 AND g.knowledge_id=task_pending_ops.dedup_key AND g.scope_id=task_pending_ops.scope_id
 AND g.token=task_pending_ops.generation)
 OR NOT EXISTS (SELECT 1 FROM tenants t WHERE t.id=task_pending_ops.tenant_id AND t.deleted_at IS NULL)
 OR NOT EXISTS (SELECT 1 FROM knowledge_bases b WHERE b.id=task_pending_ops.scope_id
  AND b.tenant_id=task_pending_ops.tenant_id AND b.deleted_at IS NULL)
 OR NOT EXISTS (SELECT 1 FROM knowledges k WHERE k.id=task_pending_ops.dedup_key
  AND k.tenant_id=task_pending_ops.tenant_id AND k.knowledge_base_id=task_pending_ops.scope_id
  AND k.deleted_at IS NULL AND (k.parse_status IN ('pending','processing','failed')
   OR (k.parse_status IN ('finalizing','completed') AND EXISTS (
    SELECT 1 FROM document_execution_leases l WHERE l.pending_id=task_pending_ops.id AND l.expires_at>?))))`
		// A live worker may already have promoted the document while still
		// committing its handoff. Do not cancel that worker through row removal.
		if err := documentRows(tx).Where("("+obsolete+")", now).Delete(&types.TaskPendingOp{}).Error; err != nil {
			return err
		}
		var leases []types.DocumentExecutionLease
		if err := tx.Where("expires_at<=?", now).Find(&leases).Error; err != nil {
			return err
		}
		for i := range leases {
			var op types.TaskPendingOp
			query := documentRows(tx).Where("id=? AND tenant_id=? AND dedup_key=? AND fail_count>=3",
				leases[i].PendingID, leases[i].TenantID, leases[i].KnowledgeID)
			if tx.Name() == "postgres" {
				query = query.Clauses(clause.Locking{Strength: "UPDATE"})
			}
			lookupErr := query.Take(&op).Error
			if lookupErr != nil && !errors.Is(lookupErr, gorm.ErrRecordNotFound) {
				return lookupErr
			}
			err := finishDocumentWithinTx(tx, &leases[i], "retry", now.Add(time.Second),
				"document execution lease expired")
			if err != nil {
				return err
			}
			if lookupErr == nil {
				op.FailCount++
				failures = append(failures, &op)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return failures, nil
}

// DocumentTenants lists origins requiring a bounded dispatcher wakeup.
func (r *taskPendingOpsRepository) DocumentTenants(ctx context.Context) ([]uint64, error) {
	var tenants []uint64
	err := r.db.WithContext(ctx).Raw(`SELECT tenant_id FROM task_pending_ops WHERE task_type=? AND scope=?
 UNION SELECT tenant_id FROM document_execution_leases ORDER BY tenant_id`,
		types.TypeDocumentProcess, types.TaskScopeKnowledgeBase).Scan(&tenants).Error
	return tenants, err
}

func (r *taskPendingOpsRepository) HasDocumentWork(ctx context.Context, tenant uint64) (bool, error) {
	var n int64
	err := r.db.WithContext(ctx).Raw(`SELECT COUNT(*) FROM (SELECT tenant_id FROM task_pending_ops
 WHERE task_type=? AND scope=? AND tenant_id=?
 UNION SELECT tenant_id FROM document_execution_leases WHERE tenant_id=?) work`,
		types.TypeDocumentProcess, types.TaskScopeKnowledgeBase, tenant, tenant).Scan(&n).Error
	return n > 0, err
}

// DocumentKnowledgeIDs includes pending and running documents for orphan checks.
func (r *taskPendingOpsRepository) DocumentKnowledgeIDs(ctx context.Context) (map[string]struct{}, error) {
	var ids []string
	err := r.db.WithContext(ctx).Raw(`SELECT dedup_key FROM task_pending_ops WHERE task_type=? AND scope=?
 UNION SELECT knowledge_id FROM document_execution_leases`,
		types.TypeDocumentProcess, types.TaskScopeKnowledgeBase).Scan(&ids).Error
	if err != nil {
		return nil, err
	}
	result := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		result[id] = struct{}{}
	}
	return result, nil
}
