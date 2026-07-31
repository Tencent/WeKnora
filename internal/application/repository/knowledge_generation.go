package repository

import (
	"context"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

var ErrKnowledgeGenerationNotFound = errors.New("knowledge generation not found")

type knowledgeGenerationRepository struct {
	db *gorm.DB
}

// NewKnowledgeGenerationRepository creates a generation repository.
func NewKnowledgeGenerationRepository(db *gorm.DB) interfaces.KnowledgeGenerationRepository {
	return &knowledgeGenerationRepository{db: db}
}

func (r *knowledgeGenerationRepository) Create(ctx context.Context, generation *types.KnowledgeGeneration) error {
	return r.db.WithContext(ctx).Create(generation).Error
}

func (r *knowledgeGenerationRepository) Get(
	ctx context.Context, tenantID uint64, generationID string,
) (*types.KnowledgeGeneration, error) {
	var generation types.KnowledgeGeneration
	if err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND id = ?", tenantID, generationID).
		First(&generation).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrKnowledgeGenerationNotFound
		}
		return nil, err
	}
	return &generation, nil
}

func (r *knowledgeGenerationRepository) GetActive(
	ctx context.Context, tenantID uint64, knowledgeID string,
) (*types.KnowledgeGeneration, error) {
	var knowledge types.Knowledge
	if err := r.db.WithContext(ctx).
		Select("active_generation_id").
		Where("tenant_id = ? AND id = ?", tenantID, knowledgeID).
		First(&knowledge).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrKnowledgeNotFound
		}
		return nil, err
	}
	if knowledge.ActiveGenerationID == "" {
		return nil, ErrKnowledgeGenerationNotFound
	}
	return r.Get(ctx, tenantID, knowledge.ActiveGenerationID)
}

func (r *knowledgeGenerationRepository) LatestAttempt(
	ctx context.Context, tenantID uint64, knowledgeID string,
) (int, error) {
	var latestAttempt int
	err := r.db.WithContext(ctx).Model(&types.KnowledgeGeneration{}).
		Where("tenant_id = ? AND knowledge_id = ?", tenantID, knowledgeID).
		Select("COALESCE(MAX(attempt), 0)").
		Scan(&latestAttempt).Error
	return latestAttempt, err
}

func (r *knowledgeGenerationRepository) MarkReady(
	ctx context.Context, generationID, manifestDigest string,
) error {
	now := time.Now()
	res := r.db.WithContext(ctx).Model(&types.KnowledgeGeneration{}).
		Where("id = ? AND state = ?", generationID, types.KnowledgeGenerationStateBuilding).
		Updates(map[string]interface{}{
			"state":           types.KnowledgeGenerationStateReady,
			"manifest_digest": manifestDigest,
			"ready_at":        now,
			"updated_at":      now,
		})
	return res.Error
}

func (r *knowledgeGenerationRepository) SetSnapshotDescription(
	ctx context.Context, generationID, description string,
) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&types.KnowledgeGeneration{}).
		Where("id = ? AND state IN ?", generationID, []types.KnowledgeGenerationState{
			types.KnowledgeGenerationStateBuilding,
			types.KnowledgeGenerationStateReady,
		}).
		Updates(map[string]interface{}{
			"snapshot_description": description,
			"updated_at":           now,
		}).Error
}

func (r *knowledgeGenerationRepository) ActivateIfCurrent(
	ctx context.Context, generationID string, attempt int,
) (bool, error) {
	now := time.Now()
	activated := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var generation types.KnowledgeGeneration
		if err := tx.Where("id = ?", generationID).First(&generation).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrKnowledgeGenerationNotFound
			}
			return err
		}
		if generation.State != types.KnowledgeGenerationStateReady || generation.Attempt != attempt {
			return nil
		}

		var latestAttempt int
		if err := tx.Model(&types.KnowledgeGeneration{}).
			Where("tenant_id = ? AND knowledge_id = ?", generation.TenantID, generation.KnowledgeID).
			Select("COALESCE(MAX(attempt), 0)").
			Scan(&latestAttempt).Error; err != nil {
			return err
		}
		if latestAttempt != attempt {
			if err := tx.Model(&types.KnowledgeGeneration{}).
				Where("id = ? AND state IN ?", generation.ID, []types.KnowledgeGenerationState{
					types.KnowledgeGenerationStateBuilding,
					types.KnowledgeGenerationStateReady,
				}).
				Updates(map[string]interface{}{
					"state":      types.KnowledgeGenerationStateRetired,
					"retired_at": now,
					"updated_at": now,
				}).Error; err != nil {
				return err
			}
			return nil
		}

		knowledgeUpdates := map[string]interface{}{
			"active_generation_id": generation.ID,
			"updated_at":           now,
		}
		if generation.SnapshotDescription != "" {
			knowledgeUpdates["description"] = generation.SnapshotDescription
		}
		res := tx.Model(&types.Knowledge{}).
			Where("tenant_id = ? AND id = ?", generation.TenantID, generation.KnowledgeID).
			Updates(knowledgeUpdates)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return ErrKnowledgeNotFound
		}

		if err := tx.Model(&types.KnowledgeGeneration{}).
			Where("id = ? AND state = ?", generation.ID, types.KnowledgeGenerationStateReady).
			Updates(map[string]interface{}{
				"state":        types.KnowledgeGenerationStateActive,
				"activated_at": now,
				"updated_at":   now,
			}).Error; err != nil {
			return err
		}

		if err := tx.Model(&types.KnowledgeGeneration{}).
			Where("tenant_id = ? AND knowledge_id = ? AND id <> ? AND state = ?",
				generation.TenantID, generation.KnowledgeID, generation.ID, types.KnowledgeGenerationStateActive).
			Updates(map[string]interface{}{
				"state":      types.KnowledgeGenerationStateRetired,
				"retired_at": now,
				"updated_at": now,
			}).Error; err != nil {
			return err
		}
		activated = true
		return nil
	})
	return activated, err
}

func (r *knowledgeGenerationRepository) MarkRetired(ctx context.Context, generationID string) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&types.KnowledgeGeneration{}).
		Where("id = ? AND state IN ?", generationID, []types.KnowledgeGenerationState{
			types.KnowledgeGenerationStateBuilding,
			types.KnowledgeGenerationStateReady,
		}).
		Updates(map[string]interface{}{
			"state":      types.KnowledgeGenerationStateRetired,
			"retired_at": now,
			"updated_at": now,
		}).Error
}

func (r *knowledgeGenerationRepository) MarkFailed(ctx context.Context, generationID, message string) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&types.KnowledgeGeneration{}).
		Where("id = ? AND state IN ?", generationID, []types.KnowledgeGenerationState{
			types.KnowledgeGenerationStateBuilding,
			types.KnowledgeGenerationStateReady,
		}).
		Updates(map[string]interface{}{
			"state":         types.KnowledgeGenerationStateFailed,
			"error_message": message,
			"updated_at":    now,
		}).Error
}

func (r *knowledgeGenerationRepository) MarkPurged(ctx context.Context, generationID string) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&types.KnowledgeGeneration{}).
		Where("id = ? AND state IN ?", generationID, []types.KnowledgeGenerationState{
			types.KnowledgeGenerationStateBuilding,
			types.KnowledgeGenerationStateRetired,
			types.KnowledgeGenerationStateFailed,
		}).
		Updates(map[string]interface{}{
			"state":      types.KnowledgeGenerationStatePurged,
			"updated_at": now,
		}).Error
}

func (r *knowledgeGenerationRepository) ListGCEligible(
	ctx context.Context, before time.Time, limit int,
) ([]*types.KnowledgeGeneration, error) {
	if limit <= 0 {
		limit = 100
	}
	var generations []*types.KnowledgeGeneration
	err := r.db.WithContext(ctx).
		Where("state IN ?", []types.KnowledgeGenerationState{
			types.KnowledgeGenerationStateBuilding,
			types.KnowledgeGenerationStateRetired,
			types.KnowledgeGenerationStateFailed,
		}).
		Where("updated_at < ?", before).
		Order("updated_at ASC").
		Limit(limit).
		Find(&generations).Error
	return generations, err
}
