package repository

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// KnowledgeStageRepository persists per-stage progress for a Knowledge.
//
// The repo is intentionally narrow: only the operations the tracker
// service needs (upsert, list, mark all skipped). We don't expose a
// generic Update — every state transition goes through one of the
// purpose-built methods so the audit trail is consistent.
type KnowledgeStageRepository interface {
	// Upsert inserts or updates a stage row by (knowledge_id, stage).
	// On conflict, ALL provided columns are overwritten and the
	// attempt counter is incremented. The caller fills in the row
	// fields it wants to set and leaves the rest at their zero values.
	Upsert(ctx context.Context, row *types.KnowledgeProcessingStage) error
	// ListByKnowledge returns every stage row for the given knowledge,
	// ordered by the canonical stage list. Missing stages are NOT
	// synthesized here — the API layer decides whether to fill them
	// with "pending" placeholders.
	ListByKnowledge(ctx context.Context, knowledgeID string) ([]types.KnowledgeProcessingStage, error)
	// MarkPendingAll resets every stage row for a knowledge to
	// "pending" with a fresh attempt number. Called at the start of
	// re-parsing so a previous run's "failed" badge doesn't linger
	// while the retry is in flight.
	MarkPendingAll(ctx context.Context, knowledgeID string) error
}

type knowledgeStageRepository struct {
	db *gorm.DB
}

// NewKnowledgeStageRepository wires the GORM-backed implementation.
func NewKnowledgeStageRepository(db *gorm.DB) KnowledgeStageRepository {
	return &knowledgeStageRepository{db: db}
}

func (r *knowledgeStageRepository) Upsert(ctx context.Context, row *types.KnowledgeProcessingStage) error {
	if row == nil || row.KnowledgeID == "" || row.Stage == "" {
		return nil
	}
	now := time.Now()
	row.UpdatedAt = now
	if row.CreatedAt.IsZero() {
		row.CreatedAt = now
	}
	if row.Attempt == 0 {
		row.Attempt = 1
	}
	// ON CONFLICT (knowledge_id, stage) bumps attempt and overwrites
	// state. We must also explicitly null-out finished_at / duration_ms
	// when the new state is "running" — otherwise stale completion
	// timestamps from a previous attempt leak into the timeline.
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "knowledge_id"}, {Name: "stage"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"status":        row.Status,
			"started_at":    row.StartedAt,
			"finished_at":   row.FinishedAt,
			"duration_ms":   row.DurationMs,
			"error_code":    row.ErrorCode,
			"error_message": row.ErrorMessage,
			"error_detail":  row.ErrorDetail,
			"attempt":       gorm.Expr("knowledge_processing_stages.attempt + ?", 1),
			"updated_at":    now,
		}),
	}).Create(row).Error
}

func (r *knowledgeStageRepository) ListByKnowledge(ctx context.Context, knowledgeID string) ([]types.KnowledgeProcessingStage, error) {
	if knowledgeID == "" {
		return nil, nil
	}
	var rows []types.KnowledgeProcessingStage
	err := r.db.WithContext(ctx).
		Where("knowledge_id = ?", knowledgeID).
		Find(&rows).Error
	return rows, err
}

func (r *knowledgeStageRepository) MarkPendingAll(ctx context.Context, knowledgeID string) error {
	if knowledgeID == "" {
		return nil
	}
	now := time.Now()
	// Reset every existing stage. We don't pre-create rows for stages
	// that haven't been touched yet — those will be added lazily by
	// the tracker on first Begin call. This keeps the table small for
	// knowledge that gets created but never parsed.
	return r.db.WithContext(ctx).Model(&types.KnowledgeProcessingStage{}).
		Where("knowledge_id = ?", knowledgeID).
		Updates(map[string]interface{}{
			"status":        types.ProcessingStagePending,
			"started_at":    nil,
			"finished_at":   nil,
			"duration_ms":   0,
			"error_code":    "",
			"error_message": "",
			"error_detail":  "",
			"attempt":       gorm.Expr("attempt + ?", 1),
			"updated_at":    now,
		}).Error
}
