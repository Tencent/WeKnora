package repository

import (
	"context"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func learningQuizPublic(
	tx *gorm.DB,
	scope interfaces.LearningScope,
	q *types.LearningQuiz,
	page *types.WikiPage,
) (*types.LearningQuizView, error) {
	v := &types.LearningQuizView{
		ID:               q.ID,
		PageID:           q.PageID,
		KnowledgeBaseID:  q.KnowledgeBaseID,
		Status:           q.Status,
		ErrorCode:        q.ErrorCode,
		Questions:        []types.LearningQuestionView{},
		AlgorithmVersion: types.LearningAlgorithmVersion,
	}
	if page != nil {
		v.Slug, v.Title = page.Slug, page.Title
	}
	if q.Status != "ready" {
		return v, nil
	}
	var questions []types.LearningQuestion
	if err := tx.Where("quiz_id = ?", q.ID).Order("position").Find(&questions).Error; err != nil {
		return nil, err
	}
	var attempts []types.LearningAttempt
	if err := learningScope(tx, scope).Where("quiz_id = ?", q.ID).Find(&attempts).Error; err != nil {
		return nil, err
	}
	answers := map[string]types.LearningAnswerResult{}
	for _, a := range attempts {
		answers[a.QuestionID] = a.Result
	}
	for _, question := range questions {
		view := types.LearningQuestionView{ID: question.ID, Prompt: question.Prompt, Options: question.Options}
		if answer, ok := answers[question.ID]; ok {
			view.Answered, view.Result = true, &answer
		}
		v.Questions = append(v.Questions, view)
	}
	return v, nil
}

func learningStaleQuiz(tx *gorm.DB, q *types.LearningQuiz) error {
	q.Status, q.ErrorCode, q.LeaseToken, q.LeaseUntil = "stale", "source_changed", "", nil
	return tx.Model(q).
		Updates(map[string]any{"status": q.Status, "error_code": q.ErrorCode, "lease_token": "", "lease_until": nil}).
		Error
}

func learningEvidenceGone(err error) bool {
	return errors.Is(err, types.ErrLearningEvidence) || errors.Is(err, types.ErrLearningNotFound) ||
		errors.Is(err, gorm.ErrRecordNotFound)
}

func learningFreshQuiz(
	tx *gorm.DB,
	scope interfaces.LearningScope,
	p *types.LearningProfile,
	q *types.LearningQuiz,
) (*types.WikiPage, error) {
	source, err := learningSource(tx, scope.TenantID, q.PageID)
	if err != nil && !learningEvidenceGone(err) {
		return nil, err
	}
	var page *types.WikiPage
	if source != nil {
		page = source.Page
	}
	if q.Status != "stale" &&
		(err != nil || source.Stamp != q.SourceStamp || source.Page.KnowledgeBaseID != q.KnowledgeBaseID ||
			q.Epoch != p.Epoch) {
		if err := learningStaleQuiz(tx, q); err != nil {
			return nil, err
		}
	}
	return page, nil
}

func (r *learningRepository) PrepareQuiz(
	ctx context.Context,
	scope interfaces.LearningScope,
	pageID string,
) (*types.LearningQuizView, *types.LearningGeneratePayload, error) {
	var result *types.LearningQuizView
	var wake *types.LearningGeneratePayload
	err := r.profileTx(ctx, scope, false, true, func(tx *gorm.DB, p *types.LearningProfile) error {
		result, wake = nil, nil
		source, err := learningSource(tx, scope.TenantID, pageID)
		if err != nil {
			return err
		}
		if source.ModelID == "" {
			return types.ErrLearningEvidence
		}
		var existing types.LearningQuiz
		err = learningScope(tx, scope).
			Where("page_id = ? AND status IN ?", pageID, []string{"pending", "running", "ready"}).
			Order("created_at DESC, id DESC").
			First(&existing).
			Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil {
			if existing.Epoch != p.Epoch || existing.SourceStamp != source.Stamp ||
				existing.KnowledgeBaseID != source.Page.KnowledgeBaseID {
				if err := learningStaleQuiz(tx, &existing); err != nil {
					return err
				}
			} else {
				var answered int64
				if err := learningScope(tx, scope).Model(&types.LearningAttempt{}).
					Where("quiz_id = ?", existing.ID).
					Count(&answered).
					Error; err != nil {
					return err
				}
				if existing.Status != "ready" || answered < 3 {
					result, err = learningQuizPublic(tx, scope, &existing, source.Page)
					if existing.Status == "pending" {
						wake = &types.LearningGeneratePayload{QuizID: existing.ID, Epoch: existing.Epoch}
					}
					return err
				}
			}
		}
		var active, total int64
		if err := learningScope(tx, scope).Model(&types.LearningQuiz{}).
			Where("status IN ?", []string{"pending", "running"}).
			Count(&active).
			Error; err != nil {
			return err
		}
		if err := learningScope(tx, scope).Model(&types.LearningQuiz{}).Count(&total).Error; err != nil {
			return err
		}
		if active >= 4 || total >= 1000 {
			return types.ErrLearningBusy
		}
		q := &types.LearningQuiz{
			ID: uuid.NewString(), TenantID: scope.TenantID, SubjectID: scope.SubjectID,
			PageID: pageID, KnowledgeBaseID: source.Page.KnowledgeBaseID, Epoch: p.Epoch, SourceStamp: source.Stamp,
			SourceKnowledgeIDs: source.Page.SourceKnowledgeIDs(),
			Status:             "pending", ModelID: source.ModelID, PromptVersion: types.LearningPromptVersion,
		}
		if err := tx.Create(q).Error; err != nil {
			return err
		}
		result, err = learningQuizPublic(tx, scope, q, source.Page)
		wake = &types.LearningGeneratePayload{QuizID: q.ID, Epoch: q.Epoch}
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	return result, wake, nil
}

func (r *learningRepository) GetQuiz(
	ctx context.Context,
	scope interfaces.LearningScope,
	id string,
) (*types.LearningQuizView, error) {
	var result *types.LearningQuizView
	err := r.profileTx(ctx, scope, false, true, func(tx *gorm.DB, p *types.LearningProfile) error {
		var q types.LearningQuiz
		if err := learningScope(tx, scope).Where("id = ?", id).First(&q).Error; err != nil {
			return err
		}
		page, err := learningFreshQuiz(tx, scope, p, &q)
		if err != nil {
			return err
		}
		result, err = learningQuizPublic(tx, scope, &q, page)
		return err
	})
	return result, err
}

func (r *learningRepository) Claim(
	ctx context.Context,
	payload types.LearningGeneratePayload,
) (*types.LearningClaim, error) {
	var initial types.LearningQuiz
	err := r.db.WithContext(ctx).Where("id = ? AND epoch = ?", payload.QuizID, payload.Epoch).First(&initial).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, learningDBError(ctx, err)
	}
	scope := interfaces.LearningScope{TenantID: initial.TenantID, SubjectID: initial.SubjectID}
	var result *types.LearningClaim
	err = r.profileTx(ctx, scope, false, false, func(tx *gorm.DB, p *types.LearningProfile) error {
		result = nil
		var q types.LearningQuiz
		err := learningScope(tx, scope).Where("id = ? AND epoch = ?", payload.QuizID, payload.Epoch).First(&q).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if !p.Enabled || p.Epoch != q.Epoch {
			return nil
		}
		now := time.Now().UTC()
		if q.Status != "pending" && (q.Status != "running" || (q.LeaseUntil != nil && q.LeaseUntil.After(now))) {
			return nil
		}
		if q.Claims >= 3 {
			return tx.Model(&q).
				Updates(map[string]any{
					"status": "failed", "error_code": "generation_exhausted", "lease_until": nil, "lease_token": "",
				}).
				Error
		}
		source, err := learningSource(tx, scope.TenantID, q.PageID)
		if err != nil && !learningEvidenceGone(err) {
			return err
		}
		if err != nil || source.Stamp != q.SourceStamp || source.Page.KnowledgeBaseID != q.KnowledgeBaseID {
			return learningStaleQuiz(tx, &q)
		}
		until, token := now.Add(types.LearningLeaseDuration), uuid.NewString()
		res := tx.Model(&types.LearningQuiz{}).
			Where("id = ? AND epoch = ? AND lease_token = ? AND status = ?", q.ID, q.Epoch, q.LeaseToken, q.Status).
			Updates(map[string]any{
				"status": "running", "lease_token": token, "lease_until": until, "claims": q.Claims + 1,
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return nil
		}
		q.Status, q.LeaseToken, q.LeaseUntil, q.Claims = "running", token, &until, q.Claims+1
		source.Variation = q.ID
		result = &types.LearningClaim{Quiz: q, Source: *source}
		return nil
	})
	return result, err
}

func (r *learningRepository) Publish(
	ctx context.Context,
	claim *types.LearningClaim,
	questions []types.LearningQuestion,
) error {
	if claim == nil {
		return types.ErrLearningInvalid
	}
	scope := interfaces.LearningScope{TenantID: claim.Quiz.TenantID, SubjectID: claim.Quiz.SubjectID}
	stale := false
	err := r.profileTx(ctx, scope, false, false, func(tx *gorm.DB, p *types.LearningProfile) error {
		stale = false
		if !p.Enabled || p.Epoch != claim.Quiz.Epoch {
			return types.ErrLearningStale
		}
		var q types.LearningQuiz
		err := learningScope(tx, scope).
			Where("id = ? AND status = ? AND epoch = ? AND lease_token = ? AND lease_until > ?",
				claim.Quiz.ID, "running", p.Epoch, claim.Quiz.LeaseToken, time.Now().UTC()).
			First(&q).
			Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return types.ErrLearningStale
		}
		if err != nil {
			return err
		}
		source, err := learningSource(tx, scope.TenantID, q.PageID)
		if err != nil && !learningEvidenceGone(err) {
			return err
		}
		if err != nil || source.Stamp != q.SourceStamp || source.Page.KnowledgeBaseID != q.KnowledgeBaseID {
			stale = true
			return learningStaleQuiz(tx, &q)
		}
		if err := types.LearningValidateQuestions(source, questions); err != nil {
			return err
		}
		for i, question := range questions {
			question.ID, question.QuizID, question.Position = uuid.NewString(), q.ID, i
			question.Fingerprint = types.LearningFingerprint(question.Prompt)
			if err := tx.Create(&question).Error; err != nil {
				return err
			}
		}
		res := tx.Model(&q).Where("lease_token = ? AND lease_until > ?", claim.Quiz.LeaseToken, time.Now().UTC()).
			Updates(map[string]any{"status": "ready", "error_code": "", "lease_token": "", "lease_until": nil})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return types.ErrLearningStale
		}
		return nil
	})
	if err == nil && stale {
		return types.ErrLearningStale
	}
	return err
}

func (r *learningRepository) Fail(ctx context.Context, claim *types.LearningClaim, code string) error {
	if claim == nil {
		return types.ErrLearningInvalid
	}
	switch code {
	case "model_unavailable", "generation_failed", "invalid_evidence", "verification_failed":
	default:
		code = "generation_failed"
	}
	scope := interfaces.LearningScope{TenantID: claim.Quiz.TenantID, SubjectID: claim.Quiz.SubjectID}
	return r.profileTx(ctx, scope, false, false, func(tx *gorm.DB, p *types.LearningProfile) error {
		if !p.Enabled || p.Epoch != claim.Quiz.Epoch {
			return nil
		}
		return learningScope(tx, scope).Model(&types.LearningQuiz{}).
			Where(
				"id = ? AND epoch = ? AND status = ? AND lease_token = ? AND lease_until > ?",
				claim.Quiz.ID, p.Epoch, "running", claim.Quiz.LeaseToken, time.Now().UTC(),
			).
			Updates(map[string]any{"status": "failed", "error_code": code, "lease_token": "", "lease_until": nil}).Error
	})
}
