package repository

import (
	"context"
	"errors"
	"sort"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrReparseSubmissionMembersUnavailable rejects overlapping or unavailable member claims.
var ErrReparseSubmissionMembersUnavailable = errors.New("reparse submission members unavailable")

// ReparseSubmissionStore owns the durable request receipt and the per-member
// claim that must precede destructive reparsing.
type ReparseSubmissionStore interface {
	CreateReparseSubmission(context.Context, *types.ReparseBatchSubmission) error
	CreateAndAssignReparseSubmission(context.Context, *types.ReparseBatchSubmission, []string) error
	GetReparseSubmissionByKey(context.Context, uint64, string) (*types.ReparseBatchSubmission, error)
	FindReparseSubmission(context.Context, uint64, string, string) (*types.ReparseBatchSubmission, error)
	HasActiveReparseSubmissionOverlap(context.Context, uint64, []string, string) (bool, error)
	SetReparseSubmissionState(context.Context, string, string, string) error
	MarkReparseSubmissionQueued(context.Context, string, string) error
	MarkReparseSubmissionEnqueueUnknown(context.Context, string, string) error
	MarkReparseSubmissionProcessing(context.Context, string) error
	FailReparseSubmissionScope(context.Context, string) error
	ClaimReparseMember(context.Context, string, string, string) (string, error)
}

func (r *knowledgeRepository) ReparseSubmissionStore() ReparseSubmissionStore {
	return (*reparseSubmissionStore)(r)
}

type reparseSubmissionStore knowledgeRepository

func (r *reparseSubmissionStore) CreateReparseSubmission(ctx context.Context, s *types.ReparseBatchSubmission) error {
	return r.db.WithContext(ctx).Create(s).Error
}

func (r *reparseSubmissionStore) CreateAndAssignReparseSubmission(
	ctx context.Context,
	s *types.ReparseBatchSubmission,
	ids []string,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Lock current knowledge rows, not the per-batch membership rows. Two
		// automatic requests create different batches, so membership locks alone
		// cannot serialize an overlapping selection across those batches.
		lockedIDs := append([]string(nil), ids...)
		sort.Strings(lockedIDs)
		var locked []types.Knowledge
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("tenant_id = ? AND id IN ?", s.TenantID, lockedIDs).
			Order("id").
			Find(&locked).
			Error; err != nil {
			return err
		}
		if len(locked) != len(ids) {
			return ErrReparseSubmissionMembersUnavailable
		}
		var active int64
		if err := tx.Table("reparse_batch_members AS m").
			Joins("JOIN reparse_batch_submissions AS prior ON prior.id = m.submission_id").
			Where(
				`prior.tenant_id = ? AND m.knowledge_id IN ?
                 AND (prior.state IN ? OR m.submission_state IN ('submitting', 'handoff_unknown'))`,
				s.TenantID, lockedIDs, []string{"created", "queued", "processing", "enqueue_unknown"},
			).
			Count(&active).
			Error; err != nil {
			return err
		}
		if active > 0 {
			return ErrReparseSubmissionMembersUnavailable
		}
		if err := tx.Create(s).Error; err != nil {
			return err
		}
		result := tx.Model(&types.ReparseBatchMember{}).
			Where("batch_id = ? AND knowledge_id IN ? AND submission_id = ''", s.BatchID, ids).
			Update("submission_id", s.ID)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != int64(len(ids)) {
			return ErrReparseSubmissionMembersUnavailable
		}
		return nil
	})
}

func (r *reparseSubmissionStore) GetReparseSubmissionByKey(
	ctx context.Context,
	tenant uint64,
	key string,
) (*types.ReparseBatchSubmission, error) {
	var row types.ReparseBatchSubmission
	if err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND idempotency_key = ?", tenant, key).
		First(&row).
		Error; err != nil {
		return nil, err
	}
	return &row, nil
}

func (r *reparseSubmissionStore) FindReparseSubmission(
	ctx context.Context,
	tenant uint64,
	batchID, requestSHA string,
) (*types.ReparseBatchSubmission, error) {
	var row types.ReparseBatchSubmission
	if err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND batch_id = ? AND request_sha256 = ?", tenant, batchID, requestSHA).
		Order("created_at DESC").
		First(&row).
		Error; err != nil {
		return nil, err
	}
	return &row, nil
}

func (r *reparseSubmissionStore) HasActiveReparseSubmissionOverlap(
	ctx context.Context,
	tenant uint64,
	ids []string,
	exceptID string,
) (bool, error) {
	if len(ids) == 0 {
		return false, nil
	}
	var count int64
	q := r.db.WithContext(ctx).
		Table("reparse_batch_members AS m").
		Joins("JOIN reparse_batch_submissions AS s ON s.id = m.submission_id").
		Where(
			`s.tenant_id = ? AND m.knowledge_id IN ?
             AND (s.state IN ? OR m.submission_state IN ('submitting', 'handoff_unknown'))`,
			tenant, ids, []string{"created", "queued", "processing", "enqueue_unknown"},
		)
	if exceptID != "" {
		q = q.Where("s.id <> ?", exceptID)
	}
	if err := q.Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

func (r *reparseSubmissionStore) SetReparseSubmissionState(ctx context.Context, id, state, taskID string) error {
	updates := map[string]interface{}{"state": state}
	if taskID != "" {
		updates["task_id"] = taskID
	}
	return r.db.WithContext(ctx).Model(&types.ReparseBatchSubmission{}).Where("id = ?", id).Updates(updates).Error
}

func (r *reparseSubmissionStore) MarkReparseSubmissionQueued(ctx context.Context, id, taskID string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&types.ReparseBatchSubmission{}).
			Where("id = ? AND state = ?", id, "created").
			Updates(map[string]interface{}{"state": "queued", "task_id": taskID})
		if result.Error != nil || result.RowsAffected == 0 {
			return result.Error
		}
		return tx.Model(&types.ReparseBatchMember{}).
			Where("submission_id = ? AND submission_state = ?", id, "waiting_start").
			Update("submission_state", "queued").
			Error
	})
}

func (r *reparseSubmissionStore) MarkReparseSubmissionEnqueueUnknown(ctx context.Context, id, taskID string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&types.ReparseBatchSubmission{}).
			Where("id = ? AND state = ?", id, "created").
			Updates(map[string]interface{}{"state": "enqueue_unknown", "task_id": taskID})
		if result.Error != nil || result.RowsAffected == 0 {
			return result.Error
		}
		return tx.Model(&types.ReparseBatchMember{}).
			Where("submission_id = ? AND submission_state = ?", id, "waiting_start").
			Update("submission_state", "handoff_unknown").
			Error
	})
}

func (r *reparseSubmissionStore) MarkReparseSubmissionProcessing(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var receipt types.ReparseBatchSubmission
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", id).First(&receipt).Error; err != nil {
			return err
		}
		if receipt.State != "created" && receipt.State != "queued" && receipt.State != "enqueue_unknown" {
			return nil
		}
		if receipt.State == "enqueue_unknown" {
			if err := tx.Model(&types.ReparseBatchMember{}).
				Where("submission_id = ? AND submission_state = ?", id, "handoff_unknown").
				Update("submission_state", "queued").
				Error; err != nil {
				return err
			}
		}
		return tx.Model(&types.ReparseBatchSubmission{}).
			Where("id = ? AND state IN ?", id, []string{"created", "queued", "enqueue_unknown"}).
			Update("state", "processing").
			Error
	})
}

func (r *reparseSubmissionStore) FailReparseSubmissionScope(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&types.ReparseBatchSubmission{}).
			Where("id = ? AND state IN ?", id, []string{"created", "queued", "processing", "enqueue_unknown"}).
			Update("state", "failed").
			Error; err != nil {
			return err
		}
		return tx.Model(&types.ReparseBatchMember{}).
			Where(
				"submission_id = ? AND submission_state IN ?", id,
				[]string{"waiting_start", "queued", "handoff_unknown"},
			).
			Updates(map[string]interface{}{
				"submission_state": "failed", "submission_error": "scope_validation_failed",
			}).
			Error
	})
}

func (r *reparseSubmissionStore) ClaimReparseMember(
	ctx context.Context,
	batchID, knowledgeID, submissionID string,
) (string, error) {
	result := r.db.WithContext(ctx).
		Model(&types.ReparseBatchMember{}).
		Where(
			"batch_id = ? AND knowledge_id = ? AND submission_id = ? AND submission_state IN ?",
			batchID, knowledgeID, submissionID, []string{"waiting_start", "queued"},
		).
		Updates(map[string]interface{}{"submission_state": "submitting", "submission_error": ""})
	if result.Error != nil {
		return "", result.Error
	}
	if result.RowsAffected > 0 {
		return "claimed", nil
	}
	var row types.ReparseBatchMember
	if err := r.db.WithContext(ctx).
		Where("batch_id = ? AND knowledge_id = ? AND submission_id = ?", batchID, knowledgeID, submissionID).
		First(&row).
		Error; err != nil {
		return "", err
	}
	return row.SubmissionState, nil
}
