package repository

import (
	"context"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

func learningKBFilter(db *gorm.DB, kbID string) *gorm.DB {
	if kbID != "" {
		return db.Where("knowledge_base_id = ?", kbID)
	}
	return db
}

func learningDelete(
	tx *gorm.DB,
	scope interfaces.LearningScope,
	kbID, pageID string,
) (*types.LearningClearResult, error) {
	filter := func() *gorm.DB {
		q := learningKBFilter(learningScope(tx, scope), kbID)
		if pageID != "" {
			q = q.Where("page_id = ?", pageID)
		}
		return q
	}
	result := &types.LearningClearResult{}
	res := filter().Delete(&types.LearningAttempt{})
	if res.Error != nil {
		return nil, res.Error
	}
	result.DeletedAttempts = res.RowsAffected
	res = filter().Delete(&types.LearningMastery{})
	if res.Error != nil {
		return nil, res.Error
	}
	result.DeletedMastery = res.RowsAffected
	quizzes := filter().Model(&types.LearningQuiz{}).Select("id")
	if err := tx.Where("quiz_id IN (?)", quizzes).Delete(&types.LearningQuestion{}).Error; err != nil {
		return nil, err
	}
	res = filter().Delete(&types.LearningQuiz{})
	if res.Error != nil {
		return nil, res.Error
	}
	result.DeletedQuizzes = res.RowsAffected
	return result, nil
}

func (r *learningRepository) Clear(
	ctx context.Context,
	scope interfaces.LearningScope,
	kbID string,
) (*types.LearningClearResult, error) {
	var result *types.LearningClearResult
	err := r.profileTx(ctx, scope, true, false, func(tx *gorm.DB, p *types.LearningProfile) error {
		var err error
		result, err = learningDelete(tx, scope, kbID, "")
		if err != nil {
			return err
		}
		var remaining int64
		if err := learningScope(tx, scope).Model(&types.LearningMastery{}).Count(&remaining).Error; err != nil {
			return err
		}
		var quizzes int64
		if err := learningScope(tx, scope).Model(&types.LearningQuiz{}).Count(&quizzes).Error; err != nil {
			return err
		}
		enabled := p.Enabled && kbID != "" && remaining+quizzes > 0
		if err := learningScope(tx, scope).Model(&types.LearningProfile{}).
			Updates(map[string]any{"epoch": p.Epoch + 1, "enabled": enabled}).
			Error; err != nil {
			return err
		}
		// A profile-wide fence also cancels other queued work. Existing history
		// in other KBs stays intact; their next quiz is prepared on the new epoch.
		return learningScope(tx, scope).Model(&types.LearningQuiz{}).
			Where("status IN ?", []string{"pending", "running", "ready"}).
			Updates(map[string]any{"status": "stale", "lease_token": "", "lease_until": nil}).
			Error
	})
	return result, err
}

func (r *learningRepository) Export(
	ctx context.Context,
	scope interfaces.LearningScope,
	kbID string,
) (*types.LearningExport, error) {
	var result *types.LearningExport
	err := r.profileTx(ctx, scope, false, false, func(tx *gorm.DB, p *types.LearningProfile) error {
		result = &types.LearningExport{
			Settings:   types.LearningSettings{Enabled: p.Enabled, AlgorithmVersion: types.LearningAlgorithmVersion},
			Nodes:      []*types.LearningNodeView{},
			Quizzes:    []*types.LearningQuizView{},
			Attempts:   []types.LearningAttemptExport{},
			ExportedAt: time.Now().UTC(),
		}
		var quizzes []types.LearningQuiz
		if err := learningKBFilter(
			learningScope(tx, scope),
			kbID,
		).Order("created_at, id").
			Find(&quizzes).
			Error; err != nil {
			return err
		}
		retained := quizzes[:0]
		for _, q := range quizzes {
			removed, err := learningPruneDeletedQuiz(tx, scope, &q)
			if err != nil {
				return err
			}
			if !removed {
				retained = append(retained, q)
			}
		}
		quizzes = retained
		// Read mastery only after pruning so counts and exported answers agree.
		var masters []types.LearningMastery
		if err := learningKBFilter(learningScope(tx, scope), kbID).Order("page_id").Find(&masters).Error; err != nil {
			return err
		}
		for _, m := range masters {
			page, kb, err := learningPage(tx, scope.TenantID, m.PageID)
			if learningEvidenceGone(err) {
				continue
			}
			if err != nil {
				return err
			}
			if page.KnowledgeBaseID != m.KnowledgeBaseID {
				continue
			}
			n, err := learningNode(tx, scope, page, kb)
			if err != nil {
				return err
			}
			result.Nodes = append(result.Nodes, types.LearningNodePublic(n, result.ExportedAt))
		}
		for _, q := range quizzes {
			page, err := learningFreshQuiz(tx, scope, p, &q)
			if err != nil {
				return err
			}
			v, err := learningQuizPublic(tx, scope, &q, page)
			if err != nil {
				return err
			}
			result.Quizzes = append(result.Quizzes, v)
		}
		var attempts []types.LearningAttempt
		if err := learningKBFilter(
			learningScope(tx, scope),
			kbID,
		).Order("created_at, attempt_id").
			Find(&attempts).
			Error; err != nil {
			return err
		}
		for _, a := range attempts {
			result.Attempts = append(
				result.Attempts,
				types.LearningAttemptExport{
					PageID:           a.PageID,
					KnowledgeBaseID:  a.KnowledgeBaseID,
					Fingerprint:      a.Fingerprint,
					SourceStamp:      a.SourceStamp,
					Before:           a.Before,
					After:            a.After,
					Credited:         a.Credited,
					AlgorithmVersion: a.AlgorithmVersion,
					CreatedAt:        a.CreatedAt,
					Result:           a.Result,
				},
			)
		}
		return nil
	})
	return result, err
}

func (r *learningRepository) Recover(ctx context.Context, limit int) ([]types.LearningGeneratePayload, error) {
	limit = max(1, min(limit, 100))
	// Find only orphans, rather than repeatedly scanning the first healthy
	// profiles. Each deletion rechecks bindings under the publication lock.
	type orphan struct {
		TenantID                           uint64
		SubjectID, PageID, KnowledgeBaseID string
	}
	var orphans []orphan
	const missing = `NOT EXISTS (SELECT 1 FROM wiki_pages p
		WHERE p.id = x.page_id AND p.tenant_id = x.tenant_id
		AND p.knowledge_base_id = x.knowledge_base_id AND p.deleted_at IS NULL)
		OR NOT EXISTS (SELECT 1 FROM knowledge_bases k
		WHERE k.id = x.knowledge_base_id AND k.tenant_id = x.tenant_id
		AND k.deleted_at IS NULL)`
	err := r.db.WithContext(ctx).
		Raw(`SELECT tenant_id, subject_id, page_id, knowledge_base_id FROM learning_quizzes x WHERE `+missing+
			` UNION SELECT tenant_id, subject_id, page_id, knowledge_base_id FROM learning_mastery x WHERE `+missing+
			` ORDER BY tenant_id, subject_id, page_id LIMIT ?`, limit).
		Scan(&orphans).
		Error
	if err != nil {
		return nil, learningDBError(ctx, err)
	}
	for _, o := range orphans {
		scope := interfaces.LearningScope{TenantID: o.TenantID, SubjectID: o.SubjectID}
		err := r.profileTx(ctx, scope, false, false, func(tx *gorm.DB, _ *types.LearningProfile) error {
			page, _, err := learningPage(tx, scope.TenantID, o.PageID)
			if err == nil && page.KnowledgeBaseID == o.KnowledgeBaseID {
				return nil
			}
			if err != nil && !learningEvidenceGone(err) {
				return err
			}
			_, err = learningDelete(tx, scope, o.KnowledgeBaseID, o.PageID)
			return err
		})
		if err != nil {
			return nil, err
		}
	}
	var removedSources []types.LearningQuiz
	err = learningRemovedSourceQuizzes(
		r.db.WithContext(ctx),
	).Order("x.updated_at, x.id").
		Limit(limit).
		Find(&removedSources).
		Error
	if err != nil {
		return nil, learningDBError(ctx, err)
	}
	for _, q := range removedSources {
		scope := interfaces.LearningScope{TenantID: q.TenantID, SubjectID: q.SubjectID}
		err := r.profileTx(ctx, scope, false, false, func(tx *gorm.DB, _ *types.LearningProfile) error {
			var current types.LearningQuiz
			err := learningScope(tx, scope).Where("id = ?", q.ID).First(&current).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			if err != nil {
				return err
			}
			_, err = learningPruneDeletedQuiz(tx, scope, &current)
			return err
		})
		if err != nil {
			return nil, err
		}
	}
	var pending []types.LearningQuiz
	err = r.db.WithContext(ctx).Table("learning_quizzes AS q").Select("q.*").
		Joins("JOIN learning_profiles p ON p.tenant_id = q.tenant_id "+
			"AND p.subject_id = q.subject_id AND p.epoch = q.epoch AND p.enabled = ?", true).
		Where(
			"q.status = ? OR (q.status = ? AND (q.lease_until IS NULL OR q.lease_until <= ?))",
			"pending", "running", time.Now().UTC(),
		).
		Order("q.updated_at, q.id").Limit(limit).Find(&pending).Error
	if err != nil {
		return nil, learningDBError(ctx, err)
	}
	wakes := []types.LearningGeneratePayload{}
	for _, q := range pending {
		scope := interfaces.LearningScope{TenantID: q.TenantID, SubjectID: q.SubjectID}
		var wake *types.LearningGeneratePayload
		err := r.profileTx(ctx, scope, false, false, func(tx *gorm.DB, p *types.LearningProfile) error {
			wake = nil
			var current types.LearningQuiz
			err := learningScope(tx, scope).Where("id = ?", q.ID).First(&current).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			if err != nil {
				return err
			}
			if !p.Enabled || p.Epoch != current.Epoch {
				return nil
			}
			if current.Status != "pending" &&
				(current.Status != "running" || (current.LeaseUntil != nil && current.LeaseUntil.After(time.Now()))) {
				return nil
			}
			if _, err := learningFreshQuiz(tx, scope, p, &current); err != nil {
				return err
			}
			if current.Status == "stale" {
				return nil
			}
			wake = &types.LearningGeneratePayload{QuizID: current.ID, Epoch: current.Epoch}
			return tx.Model(&current).Update("updated_at", time.Now().UTC()).Error
		})
		if err != nil {
			return nil, err
		}
		if wake != nil {
			wakes = append(wakes, *wake)
		}
	}
	return wakes, nil
}
