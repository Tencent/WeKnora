package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// modelUsageRepository persists call-level model usage rows. It is distinct
// from the model-reference scope helpers in model_usage.go (which answer
// "which KBs/agents reference a model"): this is the append-only usage ledger
// for Model Usage v1.
type modelUsageRepository struct{ db *gorm.DB }

// NewModelUsageRepository creates a database-backed model usage repository.
func NewModelUsageRepository(db *gorm.DB) interfaces.ModelUsageRepository {
	return &modelUsageRepository{db: db}
}

func (r *modelUsageRepository) Create(ctx context.Context, usage *types.ModelUsage) error {
	if err := usage.Validate(); err != nil {
		return err
	}
	// A non-NULL evaluation_run_id must reference an evaluation run that
	// belongs to the same tenant. The FK only guarantees existence; this
	// guards against tenant A usage rows cross-linking tenant B runs.
	if usage.EvaluationRunID != nil && *usage.EvaluationRunID != "" {
		var count int64
		if err := r.db.WithContext(ctx).Model(&types.EvaluationRun{}).
			Where("id = ? AND tenant_id = ?", *usage.EvaluationRunID, usage.TenantID).
			Count(&count).Error; err != nil {
			return fmt.Errorf("verify evaluation run tenant: %w", err)
		}
		if count == 0 {
			return fmt.Errorf(
				"model_usage: evaluation run %q not found for tenant %d",
				*usage.EvaluationRunID, usage.TenantID,
			)
		}
	}
	return r.db.WithContext(ctx).Create(usage).Error
}

func (r *modelUsageRepository) GetByID(ctx context.Context, tenantID uint64, id string) (*types.ModelUsage, error) {
	var usage types.ModelUsage
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND id = ?", tenantID, id).
		First(&usage).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &usage, nil
}
