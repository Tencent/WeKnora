package repository

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

func (r *learningRepository) Node(
	ctx context.Context,
	scope interfaces.LearningScope,
	id string,
) (*types.LearningNode, error) {
	var result *types.LearningNode
	err := r.profileTx(ctx, scope, false, true, func(tx *gorm.DB, _ *types.LearningProfile) error {
		p, kb, err := learningPage(tx, scope.TenantID, id)
		if err != nil {
			return err
		}
		result, err = learningNode(tx, scope, p, kb)
		return err
	})
	return result, err
}

func (r *learningRepository) RecordView(ctx context.Context, scope interfaces.LearningScope, id string) error {
	return r.profileTx(ctx, scope, false, true, func(tx *gorm.DB, _ *types.LearningProfile) error {
		p, _, err := learningPage(tx, scope.TenantID, id)
		if err != nil {
			return err
		}
		m, err := learningMastery(tx, scope, p)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		m.ViewedAt = &now
		return learningSaveMastery(tx, m)
	})
}

func (r *learningRepository) Overlay(
	ctx context.Context,
	scope interfaces.LearningScope,
	kbID string,
	slugs []string,
) ([]*types.LearningNode, error) {
	if len(slugs) > types.LearningMaxNodes {
		return nil, types.ErrLearningInvalid
	}
	result := []*types.LearningNode{}
	err := r.profileTx(ctx, scope, false, true, func(tx *gorm.DB, _ *types.LearningProfile) error {
		result = []*types.LearningNode{}
		kb, err := learningKB(tx, scope.TenantID, kbID)
		if err != nil {
			return err
		}
		if len(slugs) == 0 {
			return nil
		}
		var pages []*types.WikiPage
		err = learningPages(
			tx,
			scope.TenantID,
			kbID,
		).Where("slug IN ?", slugs).
			Order("id").
			Limit(types.LearningMaxNodes).
			Find(&pages).
			Error
		if err != nil {
			return err
		}
		result, err = learningNodes(tx, scope, pages, kb)
		return err
	})
	return result, err
}

func (r *learningRepository) Overview(
	ctx context.Context,
	scope interfaces.LearningScope,
	kbID string,
) (*types.LearningOverview, error) {
	result := &types.LearningOverview{
		Enabled:          true,
		AlgorithmVersion: types.LearningAlgorithmVersion,
		KnowledgeBaseID:  kbID,
	}
	err := r.profileTx(ctx, scope, false, true, func(tx *gorm.DB, _ *types.LearningProfile) error {
		kb, err := learningKB(tx, scope.TenantID, kbID)
		if err != nil {
			return err
		}
		var total int64
		if err := learningPages(tx, scope.TenantID, kbID).Count(&total).Error; err != nil {
			return err
		}
		result.TotalNodes, result.Counts = int(total), types.LearningCounts{Unseen: int(total)}
		ids := learningScope(tx, scope).Model(&types.LearningMastery{}).Select("page_id").Where("attempts > 0")
		var pages []*types.WikiPage
		if err := learningPages(
			tx,
			scope.TenantID,
			kbID,
		).Where("id IN (?)", ids).
			Order("id").
			Limit(types.LearningMaxNodes).
			Find(&pages).
			Error; err != nil {
			return err
		}
		nodes, err := learningNodes(tx, scope, pages, kb)
		if err != nil {
			return err
		}
		for _, n := range nodes {
			switch types.LearningMasteryState(n.Mastery, n.SourceStamp, time.Now()).State {
			case "learning":
				result.Counts.Learning++
				result.Counts.Unseen--
			case "mastered":
				result.Counts.Mastered++
				result.Counts.Unseen--
			case "review_due":
				result.Counts.ReviewDue++
				result.Counts.Unseen--
			}
		}
		return nil
	})
	return result, err
}

func (r *learningRepository) Candidates(
	ctx context.Context,
	scope interfaces.LearningScope,
	kbID string,
	interests []string,
) ([]*types.LearningNode, error) {
	result := []*types.LearningNode{}
	err := r.profileTx(ctx, scope, false, true, func(tx *gorm.DB, _ *types.LearningProfile) error {
		result = []*types.LearningNode{}
		kb, err := learningKB(tx, scope.TenantID, kbID)
		if err != nil {
			return err
		}
		pool := map[string]*types.WikiPage{}
		add := func(q *gorm.DB) error {
			var pages []*types.WikiPage
			if err := q.Limit(64).Find(&pages).Error; err != nil {
				return err
			}
			for _, p := range pages {
				pool[p.ID] = p
			}
			return nil
		}
		base := func() *gorm.DB { return learningPages(tx, scope.TenantID, kbID) }
		var due []string
		if err := learningScope(tx, scope).Model(&types.LearningMastery{}).
			Where("knowledge_base_id = ? AND attempts > 0", kbID).
			Order("next_review_at, page_id").
			Limit(64).
			Pluck("page_id", &due).
			Error; err != nil {
			return err
		}
		if err := add(base().Where("id IN ?", due).Order("id")); err != nil {
			return err
		}
		var seeds []*types.WikiPage
		seedIDs := learningScope(tx, scope).Model(&types.LearningMastery{}).
			Select("page_id").
			Where("knowledge_base_id = ?", kbID).
			Order("page_id").
			Limit(32)
		if err := base().Where("id IN (?)", seedIDs).Order("id").Find(&seeds).Error; err != nil {
			return err
		}
		related := map[string]bool{}
		for _, p := range seeds {
			for _, slug := range append(append([]string(nil), p.InLinks...), p.OutLinks...) {
				if len(related) < 256 {
					related[slug] = true
				}
			}
		}
		slugs := make([]string, 0, len(related))
		for slug := range related {
			slugs = append(slugs, slug)
		}
		sort.Strings(slugs)
		if err := add(base().Where("slug IN ?", slugs).Order("id")); err != nil {
			return err
		}
		if len(interests) > 0 {
			q := tx.Session(&gorm.Session{NewDB: true})
			for i, word := range interests[:min(8, len(interests))] {
				word = strings.ToLower(word)
				if len(word) > 128 {
					word = word[:128]
				}
				word = "%" + strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(word) + "%"
				condition := "(LOWER(title) LIKE ? ESCAPE '!' OR LOWER(CAST(aliases AS TEXT)) LIKE ? ESCAPE '!')"
				if i == 0 {
					q = q.Where(condition, word, word)
				} else {
					q = q.Or(condition, word, word)
				}
			}
			if err := add(base().Where(q).Order("id")); err != nil {
				return err
			}
		}
		assessed := learningScope(tx, scope).Model(&types.LearningMastery{}).Select("page_id").Where("attempts > 0")
		if err := add(base().Where("id NOT IN (?)", assessed).Order("id")); err != nil {
			return err
		}
		ids := make([]string, 0, len(pool))
		for id := range pool {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		pages := make([]*types.WikiPage, 0, len(ids))
		for _, id := range ids {
			pages = append(pages, pool[id])
		}
		result, err = learningNodes(tx, scope, pages, kb)
		if err != nil {
			return err
		}
		for _, n := range result {
			n.Related = related[n.Page.Slug]
		}
		return nil
	})
	return result, err
}
