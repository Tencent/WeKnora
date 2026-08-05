package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// SetKnowledgeTags replaces all tags for a single knowledge entry.
// It deletes existing relations and inserts new ones in a transaction.
func (r *knowledgeRepository) SetKnowledgeTags(
	ctx context.Context,
	knowledgeID string,
	tagIDs []string,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Delete all existing tag relations for this knowledge
		if err := tx.Where("knowledge_id = ?", knowledgeID).
			Delete(&types.KnowledgeTagRelation{}).Error; err != nil {
			return err
		}
		// Insert new relations (skip empty and duplicate IDs)
		if len(tagIDs) == 0 {
			return nil
		}
		seen := make(map[string]struct{}, len(tagIDs))
		now := time.Now()
		relations := make([]types.KnowledgeTagRelation, 0, len(tagIDs))
		for _, tagID := range tagIDs {
			if tagID == "" {
				continue
			}
			if _, dup := seen[tagID]; dup {
				continue
			}
			seen[tagID] = struct{}{}
			relations = append(relations, types.KnowledgeTagRelation{
				KnowledgeID: knowledgeID,
				TagID:       tagID,
				CreatedAt:   now,
			})
		}
		if len(relations) == 0 {
			return nil
		}
		return tx.Create(&relations).Error
	})
}

// AddKnowledgeTagRelations incrementally adds validated relations and never
// removes existing manual tags. The composite primary key plus DO NOTHING
// makes retries and duplicate deliveries idempotent.
func (r *knowledgeRepository) AddKnowledgeTagRelations(
	ctx context.Context,
	tenantID uint64,
	kbID, knowledgeID string,
	tagIDs []string,
) error {
	seen := make(map[string]struct{}, len(tagIDs))
	unique := make([]string, 0, len(tagIDs))
	for _, id := range tagIDs {
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	if len(unique) == 0 {
		return nil
	}

	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var knowledgeCount int64
		if err := tx.Model(&types.Knowledge{}).
			Where("id = ? AND tenant_id = ? AND knowledge_base_id = ? AND deleted_at IS NULL", knowledgeID, tenantID, kbID).
			Where("parse_status NOT IN ?", []string{types.ParseStatusCancelled, types.ParseStatusDeleting, types.ParseStatusFailed}).
			Count(&knowledgeCount).Error; err != nil {
			return err
		}
		if knowledgeCount != 1 {
			return fmt.Errorf("knowledge %s does not belong to tenant %d and knowledge base %s", knowledgeID, tenantID, kbID)
		}

		var tagCount int64
		if err := tx.Model(&types.KnowledgeTag{}).
			Where("tenant_id = ? AND knowledge_base_id = ? AND id IN ?", tenantID, kbID, unique).
			Count(&tagCount).Error; err != nil {
			return err
		}
		if tagCount != int64(len(unique)) {
			return fmt.Errorf("one or more tags do not belong to tenant %d and knowledge base %s", tenantID, kbID)
		}

		now := time.Now()
		relations := make([]types.KnowledgeTagRelation, 0, len(unique))
		for _, tagID := range unique {
			relations = append(relations, types.KnowledgeTagRelation{
				KnowledgeID: knowledgeID,
				TagID:       tagID,
				CreatedAt:   now,
			})
		}
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&relations).Error
	})
}

// GetKnowledgeTags returns tags for multiple knowledge IDs.
// The result is a map from knowledge ID to its tag list.
func (r *knowledgeRepository) GetKnowledgeTags(
	ctx context.Context,
	knowledgeIDs []string,
) (map[string][]*types.KnowledgeTag, error) {
	result := make(map[string][]*types.KnowledgeTag)
	if len(knowledgeIDs) == 0 {
		return result, nil
	}

	// Query relations and join with knowledge_tags to get full tag info
	type relationWithTag struct {
		KnowledgeID string `gorm:"column:knowledge_id"`
		types.KnowledgeTag
	}
	var rows []relationWithTag
	if err := r.db.WithContext(ctx).
		Table("knowledge_tag_relations AS ktr").
		Select("ktr.knowledge_id, kt.id, kt.seq_id, kt.tenant_id, kt.knowledge_base_id, kt.name, kt.color, kt.sort_order, kt.created_at, kt.updated_at").
		Joins("JOIN knowledge_tags AS kt ON ktr.tag_id = kt.id").
		Where("ktr.knowledge_id IN (?)", knowledgeIDs).
		Find(&rows).Error; err != nil {
		return nil, err
	}

	for _, row := range rows {
		tag := row.KnowledgeTag
		result[row.KnowledgeID] = append(result[row.KnowledgeID], &tag)
	}
	return result, nil
}

// DeleteKnowledgeTagRelations deletes all tag relations for a knowledge entry.
func (r *knowledgeRepository) DeleteKnowledgeTagRelations(
	ctx context.Context,
	knowledgeID string,
) error {
	return r.db.WithContext(ctx).
		Where("knowledge_id = ?", knowledgeID).
		Delete(&types.KnowledgeTagRelation{}).Error
}
