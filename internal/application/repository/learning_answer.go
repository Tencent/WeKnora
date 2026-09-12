package repository

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

func (r *learningRepository) SubmitAnswer(
	ctx context.Context,
	scope interfaces.LearningScope,
	input types.LearningAnswer,
) (*types.LearningAnswerResult, error) {
	if input.QuestionID == "" || len(input.QuestionID) > 36 || input.OptionID == "" || len(input.OptionID) > 36 ||
		strings.TrimSpace(input.AttemptID) != input.AttemptID || input.AttemptID == "" || len(input.AttemptID) > 64 {
		return nil, types.ErrLearningInvalid
	}
	var result *types.LearningAnswerResult
	var rejection error
	err := r.profileTx(ctx, scope, false, true, func(tx *gorm.DB, p *types.LearningProfile) error {
		result, rejection = nil, nil
		var replay types.LearningAttempt
		err := learningScope(tx, scope).Where("attempt_id = ?", input.AttemptID).First(&replay).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		hasReplay := err == nil
		if hasReplay && (replay.QuestionID != input.QuestionID || replay.Result.SelectedOption != input.OptionID) {
			return types.ErrLearningConflict
		}
		var question types.LearningQuestion
		owned := learningScope(tx, scope).Model(&types.LearningQuiz{}).Select("id")
		if err := tx.Where("id = ? AND quiz_id IN (?)", input.QuestionID, owned).First(&question).Error; err != nil {
			return err
		}
		var q types.LearningQuiz
		if err := learningScope(tx, scope).Where("id = ?", question.QuizID).First(&q).Error; err != nil {
			return err
		}
		page, err := learningFreshQuiz(tx, scope, p, &q)
		if err != nil {
			return err
		}
		if q.Status == "stale" {
			rejection = types.ErrLearningStale
			return nil
		}
		if q.Status != "ready" {
			return types.ErrLearningNotReady
		}
		valid := false
		for _, o := range question.Options {
			if o.ID == input.OptionID {
				valid = true
			}
		}
		if !valid {
			return types.ErrLearningInvalid
		}
		if hasReplay {
			result = &replay.Result
			return nil
		}
		var answered int64
		if err := learningScope(tx, scope).Model(&types.LearningAttempt{}).
			Where("question_id = ?", question.ID).
			Count(&answered).
			Error; err != nil {
			return err
		}
		if answered > 0 {
			return types.ErrLearningConflict
		}
		var credits int64
		if err := learningScope(tx, scope).Model(&types.LearningAttempt{}).
			Where("page_id = ? AND fingerprint = ? AND source_stamp = ? AND credited = ?",
				q.PageID, question.Fingerprint, q.SourceStamp, true).
			Count(&credits).Error; err != nil {
			return err
		}
		m, err := learningMastery(tx, scope, page)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		before := types.LearningMasteryState(m, q.SourceStamp, now).PMastery
		after := before
		correct := input.OptionID == question.CorrectOption
		if credits == 0 {
			before, after = types.LearningAssess(m, q.SourceStamp, correct, now)
			if err := learningSaveMastery(tx, m); err != nil {
				return err
			}
		}
		result = &types.LearningAnswerResult{
			AttemptID:      input.AttemptID,
			QuestionID:     question.ID,
			SelectedOption: input.OptionID,
			Correct:        correct,
			CorrectOption:  question.CorrectOption,
			Explanation:    question.Explanation,
			Evidence:       question.Evidence,
			Mastery:        types.LearningMasteryState(m, q.SourceStamp, now),
		}
		return tx.Create(
			&types.LearningAttempt{
				TenantID:         scope.TenantID,
				SubjectID:        scope.SubjectID,
				AttemptID:        input.AttemptID,
				QuestionID:       question.ID,
				QuizID:           q.ID,
				PageID:           q.PageID,
				KnowledgeBaseID:  q.KnowledgeBaseID,
				Fingerprint:      question.Fingerprint,
				SourceStamp:      q.SourceStamp,
				Before:           before,
				After:            after,
				Credited:         credits == 0,
				AlgorithmVersion: types.LearningAlgorithmVersion,
				Result:           *result,
				CreatedAt:        now,
			},
		).Error
	})
	if err != nil {
		return nil, err
	}
	if rejection != nil {
		return nil, rejection
	}
	return result, nil
}
