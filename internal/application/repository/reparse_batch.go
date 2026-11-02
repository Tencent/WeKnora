package repository

import (
	"context"
	"fmt"
	"sort"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
)

// ReparseBatchStore is deliberately optional: callers that use lightweight
// KnowledgeRepository fakes do not need to implement durable batch support.
type ReparseBatchStore interface {
	CreateReparseBatch(context.Context, *types.ReparseBatch, []string) error
	DiscardUnsubmittedReparseBatch(context.Context, uint64, string) error
	GetReparseBatch(context.Context, uint64, string) (*types.ReparseBatch, error)
	FindTrackingReparseBatch(context.Context, uint64, string, string) (*types.ReparseBatch, error)
	ListReparseBatches(context.Context, uint64, string, int, int) ([]*types.ReparseBatch, int64, error)
	ListReparseBatchMembers(context.Context, string, int, int) ([]*types.ReparseBatchMember, int64, error)
	CreateReparseSubmission(context.Context, *types.ReparseBatchSubmission) error
	GetReparseSubmissionByKey(context.Context, uint64, string) (*types.ReparseBatchSubmission, error)
	SetReparseSubmissionState(context.Context, string, string, string) error
	SetReparseMemberState(context.Context, string, string, string, string) error
	GetReparseBatchView(context.Context, uint64, string) (*types.ReparseBatchView, error)
	ListReparseBatchMemberViews(
		context.Context, uint64, string, string, int, int,
	) ([]*types.ReparseBatchMemberView, int64, error)
	ListReparseBatchViews(
		context.Context, uint64, string, int, int,
	) ([]*types.ReparseBatchView, int64, error)
	ListReparseBatchViewsByIDs(
		context.Context, uint64, []string,
	) ([]*types.ReparseBatchView, error)
}

// ReparseBatchStore exposes the database owned by the normal knowledge
// repository without widening interfaces used by existing test doubles.
func (r *knowledgeRepository) ReparseBatchStore() ReparseBatchStore {
	return (*reparseBatchStore)(r)
}

type reparseBatchStore knowledgeRepository

// Only an automatically created batch whose receipt failed may be discarded.
// Registered batches and batches with any durable receipt are retained.
func (r *reparseBatchStore) DiscardUnsubmittedReparseBatch(
	ctx context.Context, tenant uint64, id string,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var receipts int64
		if err := tx.Model(&types.ReparseBatchSubmission{}).
			Where("tenant_id = ? AND batch_id = ?", tenant, id).
			Count(&receipts).Error; err != nil {
			return err
		}
		if receipts != 0 {
			return nil
		}
		var batch types.ReparseBatch
		if err := tx.Where("tenant_id = ? AND id = ? AND mode = ?", tenant, id, "managed").
			First(&batch).Error; err != nil {
			return err
		}
		if err := tx.Where("batch_id = ?", id).Delete(&types.ReparseBatchMember{}).Error; err != nil {
			return err
		}
		return tx.Where("tenant_id = ? AND id = ?", tenant, id).
			Delete(&types.ReparseBatch{}).Error
	})
}

func (r *reparseBatchStore) CreateReparseBatch(ctx context.Context, batch *types.ReparseBatch, ids []string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(batch).Error; err != nil {
			return err
		}
		sort.Strings(ids)
		members := make([]types.ReparseBatchMember, 0, len(ids))
		for _, id := range ids {
			members = append(members, types.ReparseBatchMember{
				BatchID: batch.ID, KnowledgeID: id, SubmissionState: "waiting_start",
			})
		}
		if len(members) == 0 {
			return fmt.Errorf("reparse batch has no members")
		}
		// SQLite accepts at most 999 bound variables and PostgreSQL has its own
		// parameter ceiling. Keep large frozen manifests portable.
		return tx.CreateInBatches(&members, 100).Error
	})
}

func (r *reparseBatchStore) GetReparseBatch(
	ctx context.Context, tenant uint64, id string,
) (*types.ReparseBatch, error) {
	var batch types.ReparseBatch
	if err := r.db.WithContext(ctx).Where("id = ? AND tenant_id = ?", id, tenant).First(&batch).Error; err != nil {
		return nil, err
	}
	return &batch, nil
}

func (r *reparseBatchStore) FindTrackingReparseBatch(
	ctx context.Context, tenant uint64, kbID, manifest string,
) (*types.ReparseBatch, error) {
	var batch types.ReparseBatch
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND knowledge_base_id = ? AND mode = ? AND manifest_sha256 = ?",
			tenant, kbID, "tracking_only", manifest).
		First(&batch).Error
	if err != nil {
		return nil, err
	}
	return &batch, nil
}

func (r *reparseBatchStore) ListReparseBatches(
	ctx context.Context, tenant uint64, kbID string, offset, limit int,
) ([]*types.ReparseBatch, int64, error) {
	q := r.db.WithContext(ctx).Model(&types.ReparseBatch{}).Where("tenant_id = ?", tenant)
	if kbID != "" {
		q = q.Where("knowledge_base_id = ?", kbID)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []*types.ReparseBatch
	err := q.Order("created_at DESC").Offset(offset).Limit(limit).Find(&rows).Error
	return rows, total, err
}

func (r *reparseBatchStore) ListReparseBatchMembers(
	ctx context.Context, batchID string, offset, limit int,
) ([]*types.ReparseBatchMember, int64, error) {
	q := r.db.WithContext(ctx).Model(&types.ReparseBatchMember{}).Where("batch_id = ?", batchID)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []*types.ReparseBatchMember
	err := q.Order("knowledge_id").Offset(offset).Limit(limit).Find(&rows).Error
	return rows, total, err
}

func (r *reparseBatchStore) CreateReparseSubmission(
	ctx context.Context, s *types.ReparseBatchSubmission,
) error {
	return r.db.WithContext(ctx).Create(s).Error
}

func (r *reparseBatchStore) GetReparseSubmissionByKey(
	ctx context.Context, tenant uint64, key string,
) (*types.ReparseBatchSubmission, error) {
	var s types.ReparseBatchSubmission
	if err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND idempotency_key = ?", tenant, key).
		First(&s).Error; err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *reparseBatchStore) SetReparseSubmissionState(
	ctx context.Context, id, state, taskID string,
) error {
	return r.db.WithContext(ctx).Model(&types.ReparseBatchSubmission{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{"state": state, "task_id": taskID}).Error
}

func (r *reparseBatchStore) SetReparseMemberState(
	ctx context.Context, batchID, knowledgeID, state, errText string,
) error {
	return r.db.WithContext(ctx).Model(&types.ReparseBatchMember{}).
		Where("batch_id = ? AND knowledge_id = ?", batchID, knowledgeID).
		Updates(map[string]interface{}{"submission_state": state, "submission_error": errText}).Error
}
