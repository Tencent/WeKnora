package repository

import (
	"errors"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// Snapshot all generation inputs, not just quoted evidence: a multi-source page
// can survive deletion and remove the source from its current references.
func learningRemovedSourceQuizzes(tx *gorm.DB) *gorm.DB {
	refs := "json_each(x.source_knowledge_ids)"
	if tx.Dialector.Name() == "postgres" {
		refs = "jsonb_array_elements_text(x.source_knowledge_ids::jsonb)"
	}
	return tx.Table("learning_quizzes AS x").Select("x.*").Where(
		`x.source_knowledge_ids = '[]' OR EXISTS (SELECT 1 FROM ` + refs + ` AS ref
		WHERE NOT EXISTS (SELECT 1 FROM knowledges k WHERE k.id = ref.value
		AND k.tenant_id = x.tenant_id AND k.knowledge_base_id = x.knowledge_base_id AND k.deleted_at IS NULL))`)
}

func learningQuizSourcesGone(tx *gorm.DB, q *types.LearningQuiz) (bool, error) {
	if len(q.SourceKnowledgeIDs) == 0 || len(q.SourceKnowledgeIDs) > 64 {
		return true, nil
	}
	var kb types.KnowledgeBase
	err := learningShare(tx).Select("id").Where("id = ? AND tenant_id = ?", q.KnowledgeBaseID, q.TenantID).First(&kb).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	var page types.WikiPage
	err = learningShare(tx).Select("id").Where("id = ? AND tenant_id = ? AND knowledge_base_id = ?",
		q.PageID, q.TenantID, q.KnowledgeBaseID).First(&page).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	var ids []string
	err = learningShare(tx.Model(&types.Knowledge{})).Where("tenant_id = ? AND knowledge_base_id = ? AND id IN ?",
		q.TenantID, q.KnowledgeBaseID, q.SourceKnowledgeIDs).Order("id").Pluck("id", &ids).Error
	return len(ids) != len(q.SourceKnowledgeIDs), err
}

// Caller holds the profile fence. SHARE locks on existing sources serialize
// export with deletion; ordinary edits keep historical answers, but stale quizzes
// remain unanswerable through learningFreshQuiz.
func learningPruneDeletedQuiz(tx *gorm.DB, scope interfaces.LearningScope, q *types.LearningQuiz) (bool, error) {
	gone, err := learningQuizSourcesGone(tx, q)
	if err != nil || !gone {
		return false, err
	}
	if err := learningScope(tx, scope).Where("quiz_id = ?", q.ID).Delete(&types.LearningAttempt{}).Error; err != nil {
		return false, err
	}
	if err := tx.Where("quiz_id = ?", q.ID).Delete(&types.LearningQuestion{}).Error; err != nil {
		return false, err
	}
	if err := learningScope(tx, scope).Where("id = ?", q.ID).Delete(&types.LearningQuiz{}).Error; err != nil {
		return false, err
	}
	// Do not remove a newer assessment made from the surviving sources.
	err = learningScope(tx, scope).Where("page_id = ? AND source_stamp = ?", q.PageID, q.SourceStamp).
		Delete(&types.LearningMastery{}).Error
	return err == nil, err
}
