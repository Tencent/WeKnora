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

type evaluationHumanRatingRepository struct{ db *gorm.DB }

// NewEvaluationHumanRatingRepository creates the append-only human-rating store.
func NewEvaluationHumanRatingRepository(db *gorm.DB) interfaces.EvaluationHumanRatingRepository {
	return &evaluationHumanRatingRepository{db: db}
}

func (r *evaluationHumanRatingRepository) AppendHumanRating(
	ctx context.Context,
	tenantID uint64,
	taskID string,
	sampleIndex int,
	raterID string,
	input types.EvaluationHumanRatingInput,
) (*types.EvaluationHumanRatingRevision, error) {
	if err := validateHumanRating(tenantID, taskID, sampleIndex, raterID, input); err != nil {
		return nil, err
	}
	if err := (&evaluationQuestionResultRepository{db: r.db}).authorizeQuestionResultRead(
		ctx, tenantID, taskID,
	); err != nil {
		return nil, err
	}
	var created types.EvaluationHumanRatingRevision
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var question types.EvaluationQuestionResultEntity
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Select("tenant_id", "task_id", "sample_index").
			Where("tenant_id = ? AND task_id = ? AND sample_index = ?", tenantID, taskID, sampleIndex).
			First(&question).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return interfaces.ErrEvaluationTaskNotFound
			}
			return fmt.Errorf("load rated evaluation question: %w", err)
		}
		var latest int
		if err := tx.Model(&types.EvaluationHumanRatingRevision{}).
			Where("tenant_id = ? AND task_id = ? AND sample_index = ?", tenantID, taskID, sampleIndex).
			Select("COALESCE(MAX(revision), 0)").Scan(&latest).Error; err != nil {
			return fmt.Errorf("resolve human rating revision: %w", err)
		}
		var superseded types.EvaluationHumanRatingRevision
		var supersedesID *string
		if err := tx.Select("id").
			Where(
				"tenant_id = ? AND task_id = ? AND sample_index = ? AND rubric_key = ? AND rubric_version = ?",
				tenantID, taskID, sampleIndex,
				strings.TrimSpace(input.RubricKey), strings.TrimSpace(input.RubricVersion),
			).
			Order("revision DESC").First(&superseded).Error; err == nil {
			supersedesID = &superseded.ID
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("resolve superseded human rating: %w", err)
		}
		created = types.EvaluationHumanRatingRevision{
			ID: uuid.NewString(), TenantID: tenantID, TaskID: taskID, SampleIndex: sampleIndex,
			Revision: latest + 1, RaterID: strings.TrimSpace(raterID),
			RubricKey: strings.TrimSpace(input.RubricKey), RubricVersion: strings.TrimSpace(input.RubricVersion),
			RubricSnapshot: append(types.JSON(nil), input.RubricSnapshot...), Score: input.Score,
			Comment: strings.TrimSpace(input.Comment), SupersedesID: supersedesID, CreatedAt: time.Now().UTC(),
		}
		if err := tx.Create(&created).Error; err != nil {
			return fmt.Errorf("append human rating revision: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &created, nil
}

func (r *evaluationHumanRatingRepository) ListHumanRatings(
	ctx context.Context,
	tenantID uint64,
	taskID string,
	sampleIndex int,
) ([]*types.EvaluationHumanRatingRevision, error) {
	if tenantID == 0 || strings.TrimSpace(taskID) == "" || sampleIndex < 0 {
		return nil, errors.New("list human ratings: tenant, task, and non-negative sample index are required")
	}
	if err := (&evaluationQuestionResultRepository{db: r.db}).authorizeQuestionResultRead(
		ctx, tenantID, taskID,
	); err != nil {
		return nil, err
	}
	var question types.EvaluationQuestionResultEntity
	if err := r.db.WithContext(ctx).
		Select("tenant_id", "task_id", "sample_index").
		Where("tenant_id = ? AND task_id = ? AND sample_index = ?", tenantID, taskID, sampleIndex).
		First(&question).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, interfaces.ErrEvaluationTaskNotFound
		}
		return nil, fmt.Errorf("load rated evaluation question: %w", err)
	}
	var ratings []*types.EvaluationHumanRatingRevision
	if err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND task_id = ? AND sample_index = ?", tenantID, taskID, sampleIndex).
		Order("revision DESC").Find(&ratings).Error; err != nil {
		return nil, fmt.Errorf("list human rating revisions: %w", err)
	}
	if len(ratings) == 0 {
		ratings = []*types.EvaluationHumanRatingRevision{}
	}
	for _, rating := range ratings {
		rating.CreatedAt = rating.CreatedAt.UTC()
	}
	return ratings, nil
}

func validateHumanRating(
	tenantID uint64,
	taskID string,
	sampleIndex int,
	raterID string,
	input types.EvaluationHumanRatingInput,
) error {
	if tenantID == 0 || strings.TrimSpace(taskID) == "" || sampleIndex < 0 || strings.TrimSpace(raterID) == "" {
		return errors.New("append human rating: tenant, task, sample index, and rater are required")
	}
	if input.Score < 1 || input.Score > 5 || strings.TrimSpace(input.RubricKey) == "" ||
		strings.TrimSpace(input.RubricVersion) == "" {
		return errors.New("append human rating: score 1 to 5 and rubric identity are required")
	}
	if len([]byte(input.RubricKey)) > 64 || len([]byte(input.RubricVersion)) > 32 ||
		len(input.RubricSnapshot) > 16*1024 || len([]byte(strings.TrimSpace(input.Comment))) > 4000 {
		return errors.New("append human rating: rubric or comment exceeds size limit")
	}
	var rubric map[string]json.RawMessage
	if len(input.RubricSnapshot) == 0 || json.Unmarshal(input.RubricSnapshot, &rubric) != nil || rubric == nil {
		return errors.New("append human rating: rubric_snapshot must be a JSON object")
	}
	return nil
}

var _ interfaces.EvaluationHumanRatingRepository = (*evaluationHumanRatingRepository)(nil)
