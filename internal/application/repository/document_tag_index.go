package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Readiness is a persisted bootstrap marker, not a cross-store consistency
// guarantee. Queries never migrate data or invalidate it after a write failure.
type documentTagIndexRepository struct {
	interfaces.RetrieveEngineRepository
	db   *gorm.DB
	tags interfaces.DocumentTagIndexer
	task interfaces.TaskEnqueuer
}

func NewDocumentTagIndexRepository(repo interfaces.RetrieveEngineRepository, db *gorm.DB, task interfaces.TaskEnqueuer) interfaces.RetrieveEngineRepository {
	if _, wrapped := repo.(*documentTagIndexRepository); wrapped {
		return repo
	}
	tags, _ := repo.(interfaces.DocumentTagIndexer)
	return &documentTagIndexRepository{RetrieveEngineRepository: repo, db: db, tags: tags, task: task}
}

func (r *documentTagIndexRepository) SupportsDocumentTags() bool { return r.tags != nil }

func (r *documentTagIndexRepository) Retrieve(ctx context.Context, params types.RetrieveParams) ([]*types.RetrieveResult, error) {
	if len(params.TagIDs) == 0 || params.KnowledgeType == types.KnowledgeTypeFAQ {
		return r.RetrieveEngineRepository.Retrieve(ctx, params)
	}
	if len(params.KnowledgeBaseIDs) == 0 {
		return nil, fmt.Errorf("document tag filtering requires a knowledge base scope")
	}
	if r.tags != nil {
		var kbs []types.KnowledgeBase
		if err := r.db.WithContext(ctx).Select("id", "document_tag_ready").Where("id IN ?", params.KnowledgeBaseIDs).Find(&kbs).Error; err != nil {
			return nil, err
		}
		ready := len(kbs) == len(uniqueTagIndexIDs(params.KnowledgeBaseIDs))
		for _, kb := range kbs {
			ready = ready && kb.DocumentTagReady
		}
		if ready {
			// The backend checks schema availability without creating an index.
			results, err := r.RetrieveEngineRepository.Retrieve(ctx, params)
			if err == nil {
				return results, nil
			}
			logger.Warnf(ctx, "Document tag query failed; using relational fallback: %v", err)
		}
	}
	var ids []string
	q := r.db.WithContext(ctx).Model(&types.Knowledge{}).
		Joins("JOIN knowledge_tag_relations ktr ON knowledges.id = ktr.knowledge_id").
		Where("knowledges.knowledge_base_id IN ? AND ktr.tag_id IN ?", params.KnowledgeBaseIDs, params.TagIDs)
	if len(params.KnowledgeIDs) > 0 {
		q = q.Where("knowledges.id IN ?", params.KnowledgeIDs)
	}
	if err := q.Distinct("knowledges.id").Pluck("knowledges.id", &ids).Error; err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}
	params.KnowledgeIDs, params.TagIDs = ids, nil
	return r.RetrieveEngineRepository.Retrieve(ctx, params)
}

// Each metadata page and index write locks the same KB row across processes.
// Relational tag mutations commit first; synchronization reloads their current
// values under this short-lived lock instead of replaying a stale payload.
func lockTagKB(tx *gorm.DB, kbID string) error {
	if tx.Dialector.Name() == "sqlite" {
		if err := tx.Model(&types.KnowledgeBase{}).Where("id = ?", kbID).
			UpdateColumn("document_tag_ready", gorm.Expr("document_tag_ready")).Error; err != nil {
			return err
		}
	}
	return tx.Select("id").Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", kbID).First(&types.KnowledgeBase{}).Error
}

func documentTags(tx *gorm.DB, kbID string, ids []string) (map[string][]string, error) {
	var docs []types.Knowledge
	if err := tx.Unscoped().Select("id").Where("knowledge_base_id = ? AND id IN ?", kbID, ids).Find(&docs).Error; err != nil {
		return nil, err
	}
	tags := make(map[string][]string, len(docs))
	for _, doc := range docs {
		tags[doc.ID] = []string{}
	}
	var relations []types.KnowledgeTagRelation
	if err := tx.Table("knowledge_tag_relations AS ktr").Select("ktr.*").
		Joins("JOIN knowledges k ON k.id = ktr.knowledge_id AND k.deleted_at IS NULL").
		Where("k.knowledge_base_id = ? AND ktr.knowledge_id IN ?", kbID, ids).
		Order("ktr.tag_id").Find(&relations).Error; err != nil {
		return nil, err
	}
	for _, relation := range relations {
		tags[relation.KnowledgeID] = append(tags[relation.KnowledgeID], relation.TagID)
	}
	return tags, nil
}

func (r *documentTagIndexRepository) SyncDocumentTags(ctx context.Context, kbID string, ids []string) error {
	if r.tags == nil {
		return nil
	}
	if err := r.tags.PrepareDocumentTagIndex(ctx); err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil // Still prepare the schema for an empty KB.
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockTagKB(tx, kbID); err != nil {
			return err
		}
		tags, err := documentTags(tx, kbID, ids)
		if err != nil || len(tags) == 0 {
			return err
		}
		return r.tags.UpdateDocumentTags(ctx, kbID, tags)
	})
}

func uniqueTagIndexIDs(ids []string) []string {
	result := slices.Clone(ids)
	slices.Sort(result)
	return slices.Compact(result)
}

func (r *documentTagIndexRepository) Save(ctx context.Context, info *types.IndexInfo, params map[string]any) error {
	return r.BatchSave(ctx, []*types.IndexInfo{info}, params)
}

func (r *documentTagIndexRepository) BatchSave(ctx context.Context, infos []*types.IndexInfo, params map[string]any) error {
	byKB := make(map[string][]string)
	for _, info := range infos {
		if info.KnowledgeType != types.KnowledgeTypeFAQ {
			byKB[info.KnowledgeBaseID] = append(byKB[info.KnowledgeBaseID], info.KnowledgeID)
		}
	}
	if r.tags == nil || len(byKB) == 0 {
		return r.RetrieveEngineRepository.BatchSave(ctx, infos, params)
	}
	if err := r.tags.PrepareDocumentTagIndex(ctx); err != nil {
		return err
	}
	kbIDs := make([]string, 0, len(byKB))
	for id := range byKB {
		kbIDs = append(kbIDs, id)
	}
	slices.Sort(kbIDs)
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		allTags := make(map[string][]string)
		for _, kbID := range kbIDs {
			if err := lockTagKB(tx, kbID); err != nil {
				return err
			}
			tags, err := documentTags(tx, kbID, byKB[kbID])
			if err != nil {
				return err
			}
			for id, values := range tags {
				allTags[id] = values
			}
		}
		enriched := make([]*types.IndexInfo, len(infos))
		for i, info := range infos {
			copy := *info
			if info.KnowledgeType != types.KnowledgeTypeFAQ {
				copy.TagIDs = append([]string{}, allTags[info.KnowledgeID]...)
			}
			enriched[i] = &copy
		}
		return r.RetrieveEngineRepository.BatchSave(ctx, enriched, params)
	})
	if err == nil {
		r.enqueueBackfill(ctx, kbIDs)
	}
	return err
}

func (r *documentTagIndexRepository) enqueueBackfill(ctx context.Context, kbIDs []string) {
	if r.task == nil {
		return
	}
	var kbs []types.KnowledgeBase
	if err := r.db.WithContext(ctx).Select("id", "tenant_id").Where("id IN ? AND document_tag_ready = ?", kbIDs, false).Find(&kbs).Error; err != nil {
		logger.Warnf(ctx, "Cannot schedule document tag backfill: %v", err)
		return
	}
	for _, kb := range kbs {
		if err := EnqueueDocumentTagSync(r.task, types.DocumentTagSyncPayload{TenantID: kb.TenantID, KnowledgeBaseID: kb.ID}); err != nil {
			logger.Warnf(ctx, "Cannot enqueue document tag backfill for %s: %v", kb.ID, err)
		}
	}
}

// EnqueueDocumentTagSync shares routing and retry policy with the worker and
// recovery sweep. Only full bootstrap triggers are deduplicated: a newer
// incremental repair must never be lost behind an already-running repair.
func EnqueueDocumentTagSync(task interfaces.TaskEnqueuer, payload types.DocumentTagSyncPayload) error {
	if task == nil {
		return fmt.Errorf("document tag task queue is unavailable")
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	opts := []asynq.Option{asynq.Queue(types.QueueMaintenance), asynq.MaxRetry(10), asynq.Timeout(30 * time.Minute)}
	if len(payload.KnowledgeIDs) == 0 && !payload.Force {
		opts = append(opts, asynq.Unique(10*time.Minute))
	}
	_, err = task.Enqueue(asynq.NewTask(types.TypeDocumentTagSync, data), opts...)
	if errors.Is(err, asynq.ErrDuplicateTask) {
		return nil
	}
	return err
}

func (r *documentTagIndexRepository) CopyIndices(ctx context.Context, sourceKB string, knowledgeIDs, chunkIDs map[string]string, targetKB string, dimension int, knowledgeType string) error {
	if r.tags == nil || knowledgeType == types.KnowledgeTypeFAQ {
		return r.RetrieveEngineRepository.CopyIndices(ctx, sourceKB, knowledgeIDs, chunkIDs, targetKB, dimension, knowledgeType)
	}
	// Copying historical indices is an explicit bootstrap operation, unlike
	// ordinary writes. Never inherit the source KB's readiness or tag IDs.
	if err := r.db.WithContext(ctx).Model(&types.KnowledgeBase{}).Where("id = ?", targetKB).UpdateColumn("document_tag_ready", false).Error; err != nil {
		return err
	}
	defer r.enqueueBackfill(ctx, []string{targetKB})
	if err := r.tags.PrepareDocumentTagIndex(ctx); err != nil {
		return err
	}
	err := r.RetrieveEngineRepository.CopyIndices(ctx, sourceKB, knowledgeIDs, chunkIDs, targetKB, dimension, knowledgeType)
	ids := make([]string, 0, len(knowledgeIDs))
	for _, id := range knowledgeIDs {
		ids = append(ids, id)
	}
	for start := 0; start < len(ids); start += 100 {
		if syncErr := r.SyncDocumentTags(ctx, targetKB, ids[start:min(start+100, len(ids))]); syncErr != nil {
			return errors.Join(err, syncErr)
		}
	}
	return err
}

func (r *documentTagIndexRepository) ValidateKnowledgeIndexMove(ctx context.Context) error {
	if _, ok := r.RetrieveEngineRepository.(interfaces.KnowledgeIndexMover); !ok {
		return fmt.Errorf("retriever %s does not support moving indices", r.EngineType())
	}
	if validator, ok := r.RetrieveEngineRepository.(interface{ ValidateKnowledgeIndexMove(context.Context) error }); ok {
		return validator.ValidateKnowledgeIndexMove(ctx)
	}
	return nil
}

func (r *documentTagIndexRepository) MoveKnowledgeIndices(ctx context.Context, sourceKB, targetKB, knowledgeID string, chunkIDs []string, dimension int, knowledgeType string) error {
	if err := r.ValidateKnowledgeIndexMove(ctx); err != nil {
		return err
	}
	return r.RetrieveEngineRepository.(interfaces.KnowledgeIndexMover).MoveKnowledgeIndices(ctx, sourceKB, targetKB, knowledgeID, chunkIDs, dimension, knowledgeType)
}
