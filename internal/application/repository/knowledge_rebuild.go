package repository

import (
	"context"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var activeRebuildRunStatuses = []string{
	types.RebuildRunStatusPending,
	types.RebuildRunStatusParsing,
	types.RebuildRunStatusParsed,
	types.RebuildRunStatusChunksClassified,
	types.RebuildRunStatusMultimodal,
	types.RebuildRunStatusArtifactsReady,
	types.RebuildRunStatusFinalizing,
	types.RebuildRunStatusCommitting,
}

type knowledgeRebuildRunRepository struct {
	db *gorm.DB
}

func NewKnowledgeRebuildRunRepository(db *gorm.DB) interfaces.KnowledgeRebuildRunRepository {
	return &knowledgeRebuildRunRepository{db: db}
}

func (r *knowledgeRebuildRunRepository) Start(ctx context.Context, run *types.KnowledgeRebuildRun) error {
	if run == nil {
		return errors.New("knowledge rebuild run: nil run")
	}
	if run.ID == "" {
		run.ID = uuid.New().String()
	}
	now := time.Now()
	if run.Status == "" {
		run.Status = types.RebuildRunStatusPending
	}
	if run.StartedAt.IsZero() {
		run.StartedAt = now
	}
	run.UpdatedAt = now

	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Serialize rebuild creation with other rebuilds for the same knowledge.
		var knowledgeID string
		result := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Table("knowledges").
			Select("id").
			Where("tenant_id = ? AND id = ?", run.TenantID, run.KnowledgeID).
			Scan(&knowledgeID)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("knowledge rebuild run: knowledge not found")
		}

		if err := tx.Model(&types.KnowledgeRebuildRun{}).
			Where("tenant_id = ? AND knowledge_id = ? AND status IN ?", run.TenantID, run.KnowledgeID, activeRebuildRunStatuses).
			Updates(map[string]interface{}{
				"status":        types.RebuildRunStatusSuperseded,
				"error_message": "superseded by a newer rebuild run",
				"completed_at":  now,
				"updated_at":    now,
			}).Error; err != nil {
			return err
		}
		return tx.Create(run).Error
	})
}

func (r *knowledgeRebuildRunRepository) Get(ctx context.Context, tenantID uint64, runID string) (*types.KnowledgeRebuildRun, error) {
	var run types.KnowledgeRebuildRun
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND id = ?", tenantID, runID).
		First(&run).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &run, nil
}

func (r *knowledgeRebuildRunRepository) IsCurrent(
	ctx context.Context,
	tenantID uint64,
	knowledgeID, runID string,
	attempt int,
) (bool, error) {
	if runID == "" {
		return true, nil
	}
	var count int64
	query := r.db.WithContext(ctx).Model(&types.KnowledgeRebuildRun{}).
		Where("tenant_id = ? AND knowledge_id = ? AND id = ? AND status IN ?", tenantID, knowledgeID, runID, activeRebuildRunStatuses)
	if attempt > 0 {
		query = query.Where("(attempt = ? OR attempt = 0)", attempt)
	}
	if err := query.Count(&count).Error; err != nil {
		return false, err
	}
	return count == 1, nil
}

func (r *knowledgeRebuildRunRepository) BindAttempt(
	ctx context.Context, tenantID uint64, runID string, attempt int,
) error {
	if runID == "" || attempt <= 0 {
		return nil
	}
	return r.db.WithContext(ctx).Model(&types.KnowledgeRebuildRun{}).
		Where("tenant_id = ? AND id = ? AND attempt = 0 AND status IN ?", tenantID, runID, activeRebuildRunStatuses).
		Update("attempt", attempt).Error
}

func (r *knowledgeRebuildRunRepository) SetStatus(
	ctx context.Context,
	tenantID uint64,
	runID, status, errorMessage string,
) error {
	if runID == "" {
		return nil
	}
	now := time.Now()
	updates := map[string]interface{}{
		"status":        status,
		"error_message": errorMessage,
		"updated_at":    now,
	}
	switch status {
	case types.RebuildRunStatusCompleted, types.RebuildRunStatusFailed,
		types.RebuildRunStatusCancelled, types.RebuildRunStatusSuperseded:
		updates["completed_at"] = now
	}
	return r.db.WithContext(ctx).Model(&types.KnowledgeRebuildRun{}).
		Where("tenant_id = ? AND id = ? AND status IN ?", tenantID, runID, activeRebuildRunStatuses).
		Updates(updates).Error
}

func (r *knowledgeRebuildRunRepository) RecordParseResult(
	ctx context.Context,
	tenantID uint64,
	runID, cacheKey string,
	cacheHit, success, terminal bool,
	errorMessage string,
) error {
	if runID == "" {
		return nil
	}
	status := types.RebuildRunStatusParsing
	if success {
		status = types.RebuildRunStatusParsed
		errorMessage = ""
	} else if terminal {
		status = types.RebuildRunStatusFailed
	}
	return r.setStatusAndFields(ctx, tenantID, runID, status, errorMessage, map[string]interface{}{
		"parse_cache_key": cacheKey,
		"parse_cache_hit": cacheHit,
	})
}

func (r *knowledgeRebuildRunRepository) ReplaceChunkResults(
	ctx context.Context,
	tenantID uint64,
	runID string,
	results []*types.KnowledgeRebuildChunkResult,
) error {
	if runID == "" {
		return nil
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := ensureActiveRebuildRun(tx, tenantID, runID); err != nil {
			return err
		}
		if err := tx.Where("run_id = ?", runID).Delete(&types.KnowledgeRebuildChunkResult{}).Error; err != nil {
			return err
		}
		prepareRebuildChunkResults(runID, results)
		if len(results) > 0 {
			if err := tx.CreateInBatches(results, 200).Error; err != nil {
				return err
			}
		}
		return updateRebuildChunkCounts(tx, tenantID, runID, true)
	})
}

func (r *knowledgeRebuildRunRepository) UpsertChunkResults(
	ctx context.Context,
	tenantID uint64,
	runID string,
	results []*types.KnowledgeRebuildChunkResult,
) error {
	if runID == "" || len(results) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := ensureActiveRebuildRun(tx, tenantID, runID); err != nil {
			return err
		}
		prepareRebuildChunkResults(runID, results)
		if err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "run_id"}, {Name: "chunk_id"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"chunk_type", "classification", "content_fingerprint", "metadata_fingerprint", "updated_at",
			}),
		}).CreateInBatches(results, 200).Error; err != nil {
			return err
		}
		return updateRebuildChunkCounts(tx, tenantID, runID, false)
	})
}

func (r *knowledgeRebuildRunRepository) ListChunkResults(
	ctx context.Context,
	tenantID uint64,
	runID string,
	classifications []string,
	chunkTypes []types.ChunkType,
) ([]*types.KnowledgeRebuildChunkResult, error) {
	if runID == "" {
		return nil, nil
	}
	run, err := r.Get(ctx, tenantID, runID)
	if err != nil {
		return nil, err
	}
	if run == nil {
		return nil, errors.New("knowledge rebuild run: run not found")
	}
	var results []*types.KnowledgeRebuildChunkResult
	query := r.db.WithContext(ctx).Where("run_id = ?", runID)
	if len(classifications) > 0 {
		query = query.Where("classification IN ?", classifications)
	}
	if len(chunkTypes) > 0 {
		query = query.Where("chunk_type IN ?", chunkTypes)
	}
	if err := query.Order("created_at ASC").Find(&results).Error; err != nil {
		return nil, err
	}
	return results, nil
}

func ensureActiveRebuildRun(tx *gorm.DB, tenantID uint64, runID string) error {
	var count int64
	if err := tx.Model(&types.KnowledgeRebuildRun{}).
		Where("tenant_id = ? AND id = ? AND status IN ?", tenantID, runID, activeRebuildRunStatuses).
		Count(&count).Error; err != nil {
		return err
	}
	if count != 1 {
		return errors.New("knowledge rebuild run: run is not active")
	}
	return nil
}

func prepareRebuildChunkResults(runID string, results []*types.KnowledgeRebuildChunkResult) {
	now := time.Now()
	for _, result := range results {
		if result == nil {
			continue
		}
		if result.ID == "" {
			result.ID = uuid.New().String()
		}
		result.RunID = runID
		if result.CreatedAt.IsZero() {
			result.CreatedAt = now
		}
		result.UpdatedAt = now
	}
}

func updateRebuildChunkCounts(tx *gorm.DB, tenantID uint64, runID string, classified bool) error {
	var counts struct {
		Candidate    int
		Unchanged    int
		MetadataOnly int
		ChangedNew   int
		Stale        int
	}
	if err := tx.Model(&types.KnowledgeRebuildChunkResult{}).
		Select(`COALESCE(SUM(CASE WHEN classification <> 'stale' THEN 1 ELSE 0 END), 0) AS candidate,
			COALESCE(SUM(CASE WHEN classification = 'unchanged' THEN 1 ELSE 0 END), 0) AS unchanged,
			COALESCE(SUM(CASE WHEN classification = 'metadata_only' THEN 1 ELSE 0 END), 0) AS metadata_only,
			COALESCE(SUM(CASE WHEN classification = 'changed_new' THEN 1 ELSE 0 END), 0) AS changed_new,
			COALESCE(SUM(CASE WHEN classification = 'stale' THEN 1 ELSE 0 END), 0) AS stale`).
		Where("run_id = ?", runID).
		Scan(&counts).Error; err != nil {
		return err
	}
	updates := map[string]interface{}{
		"candidate_chunks":     counts.Candidate,
		"unchanged_chunks":     counts.Unchanged,
		"metadata_only_chunks": counts.MetadataOnly,
		"changed_new_chunks":   counts.ChangedNew,
		"stale_chunks":         counts.Stale,
		"updated_at":           time.Now(),
	}
	if classified {
		now := time.Now()
		updates["status"] = types.RebuildRunStatusChunksClassified
		updates["chunk_diff_ready_at"] = now
	}
	return tx.Model(&types.KnowledgeRebuildRun{}).
		Where("tenant_id = ? AND id = ? AND status IN ?", tenantID, runID, activeRebuildRunStatuses).
		Updates(updates).Error
}

func (r *knowledgeRebuildRunRepository) BeginImages(ctx context.Context, tenantID uint64, runID string, total int) error {
	if runID == "" {
		return nil
	}
	if total < 0 {
		total = 0
	}
	status := types.RebuildRunStatusMultimodal
	updates := map[string]interface{}{
		"images_total": total,
		"updated_at":   time.Now(),
	}
	if total == 0 {
		now := time.Now()
		status = types.RebuildRunStatusArtifactsReady
		updates["artifacts_ready_at"] = now
	}
	updates["status"] = status
	return r.db.WithContext(ctx).Model(&types.KnowledgeRebuildRun{}).
		Where("tenant_id = ? AND id = ? AND status IN ?", tenantID, runID, activeRebuildRunStatuses).
		Updates(updates).Error
}

func (r *knowledgeRebuildRunRepository) RecordImageResult(
	ctx context.Context,
	tenantID uint64,
	runID string,
	imageIndex int,
	ocrCacheKey, captionCacheKey string,
	ocrCacheHit, captionCacheHit, success bool,
	errorMessage string,
) (bool, error) {
	if runID == "" {
		return false, nil
	}
	ready := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var run types.KnowledgeRebuildRun
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("tenant_id = ? AND id = ?", tenantID, runID).
			First(&run).Error; err != nil {
			return err
		}
		if !containsRebuildStatus(activeRebuildRunStatuses, run.Status) {
			return nil
		}

		resultStatus := "completed"
		if !success {
			resultStatus = "failed"
		}
		result := &types.KnowledgeRebuildImageResult{
			ID:              uuid.New().String(),
			RunID:           runID,
			ImageIndex:      imageIndex,
			Status:          resultStatus,
			OCRCacheKey:     ocrCacheKey,
			CaptionCacheKey: captionCacheKey,
			OCRCacheHit:     ocrCacheHit,
			CaptionCacheHit: captionCacheHit,
			ErrorMessage:    errorMessage,
		}
		if err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "run_id"}, {Name: "image_index"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"status", "ocr_cache_key", "caption_cache_key",
				"ocr_cache_hit", "caption_cache_hit", "error_message", "updated_at",
			}),
		}).Create(result).Error; err != nil {
			return err
		}

		var aggregate struct {
			Completed        int
			Failed           int
			OCRCacheHits     int
			CaptionCacheHits int
		}
		if err := tx.Model(&types.KnowledgeRebuildImageResult{}).
			Select(`COUNT(*) AS completed,
				SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END) AS failed,
				SUM(CASE WHEN ocr_cache_hit THEN 1 ELSE 0 END) AS ocr_cache_hits,
				SUM(CASE WHEN caption_cache_hit THEN 1 ELSE 0 END) AS caption_cache_hits`).
			Where("run_id = ?", runID).
			Scan(&aggregate).Error; err != nil {
			return err
		}

		updates := map[string]interface{}{
			"images_completed":   aggregate.Completed,
			"images_failed":      aggregate.Failed,
			"ocr_cache_hits":     aggregate.OCRCacheHits,
			"caption_cache_hits": aggregate.CaptionCacheHits,
			"updated_at":         time.Now(),
		}
		if run.ImagesTotal > 0 && aggregate.Completed >= run.ImagesTotal {
			now := time.Now()
			updates["status"] = types.RebuildRunStatusArtifactsReady
			updates["artifacts_ready_at"] = now
			ready = true
		}
		return tx.Model(&types.KnowledgeRebuildRun{}).
			Where("tenant_id = ? AND id = ?", tenantID, runID).
			Updates(updates).Error
	})
	return ready, err
}

func (r *knowledgeRebuildRunRepository) BeginArtifacts(
	ctx context.Context,
	tenantID uint64,
	runID string,
	total int,
	summaryRequired, wikiReduceRequired bool,
) error {
	if runID == "" {
		return nil
	}
	if total < 0 {
		total = 0
	}
	return r.db.WithContext(ctx).Model(&types.KnowledgeRebuildRun{}).
		Where("tenant_id = ? AND id = ? AND status IN ?", tenantID, runID, activeRebuildRunStatuses).
		Updates(map[string]interface{}{
			"status":               types.RebuildRunStatusFinalizing,
			"artifacts_total":      total,
			"summary_required":     summaryRequired,
			"wiki_reduce_required": wikiReduceRequired,
			"updated_at":           time.Now(),
		}).Error
}

func (r *knowledgeRebuildRunRepository) FinalizeArtifact(
	ctx context.Context,
	tenantID uint64,
	runID, knowledgeID, stage, artifactKey string,
	success bool,
	errorMessage string,
) (bool, error) {
	if runID == "" || stage == "" || artifactKey == "" {
		return false, nil
	}
	inserted := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := ensureActiveRebuildRun(tx, tenantID, runID); err != nil {
			return err
		}
		status := "completed"
		if !success {
			status = "failed"
		}
		result := &types.KnowledgeRebuildArtifactResult{
			ID:           uuid.New().String(),
			RunID:        runID,
			Stage:        stage,
			ArtifactKey:  artifactKey,
			Status:       status,
			ErrorMessage: errorMessage,
		}
		create := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "run_id"}, {Name: "stage"}, {Name: "artifact_key"}},
			DoNothing: true,
		}).Create(result)
		if create.Error != nil {
			return create.Error
		}
		if create.RowsAffected == 0 {
			return nil
		}
		inserted = true

		if err := tx.Model(&types.Knowledge{}).
			Where("id = ? AND parse_status = ? AND pending_subtasks_count > 0", knowledgeID, types.ParseStatusFinalizing).
			Updates(map[string]interface{}{
				"pending_subtasks_count": gorm.Expr("pending_subtasks_count - 1"),
				"updated_at":             time.Now(),
			}).Error; err != nil {
			return err
		}

		var counts struct {
			Completed int
			Failed    int
		}
		if err := tx.Model(&types.KnowledgeRebuildArtifactResult{}).
			Select(`COUNT(*) AS completed,
				COALESCE(SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END), 0) AS failed`).
			Where("run_id = ?", runID).
			Scan(&counts).Error; err != nil {
			return err
		}
		return tx.Model(&types.KnowledgeRebuildRun{}).
			Where("tenant_id = ? AND id = ?", tenantID, runID).
			Updates(map[string]interface{}{
				"artifacts_completed": counts.Completed,
				"artifacts_failed":    counts.Failed,
				"updated_at":          time.Now(),
			}).Error
	})
	return inserted, err
}

func (r *knowledgeRebuildRunRepository) MarkStaleCleanupComplete(
	ctx context.Context, tenantID uint64, runID string,
) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&types.KnowledgeRebuildRun{}).
		Where("tenant_id = ? AND id = ? AND status IN ?", tenantID, runID, activeRebuildRunStatuses).
		Updates(map[string]interface{}{
			"status":           types.RebuildRunStatusCommitting,
			"stale_cleanup_at": now,
			"updated_at":       now,
		}).Error
}

func (r *knowledgeRebuildRunRepository) MarkWikiReduceEnqueued(
	ctx context.Context, tenantID uint64, runID string,
) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&types.KnowledgeRebuildRun{}).
		Where("tenant_id = ? AND id = ? AND status IN ?", tenantID, runID, activeRebuildRunStatuses).
		Updates(map[string]interface{}{
			"wiki_reduce_enqueued_at": now,
			"updated_at":              now,
		}).Error
}

func (r *knowledgeRebuildRunRepository) FinalizeCommit(
	ctx context.Context, tenantID uint64, runID, knowledgeID string,
) (bool, error) {
	promoted := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var run types.KnowledgeRebuildRun
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("tenant_id = ? AND id = ?", tenantID, runID).
			First(&run).Error; err != nil {
			return err
		}
		if run.CommitCompletedAt != nil {
			return nil
		}
		if !containsRebuildStatus(activeRebuildRunStatuses, run.Status) {
			return nil
		}
		now := time.Now()
		if err := tx.Model(&types.Knowledge{}).
			Where("id = ? AND parse_status = ? AND pending_subtasks_count > 0", knowledgeID, types.ParseStatusFinalizing).
			Updates(map[string]interface{}{
				"pending_subtasks_count": gorm.Expr("pending_subtasks_count - 1"),
				"updated_at":             now,
			}).Error; err != nil {
			return err
		}
		updates := map[string]interface{}{
			"status":              types.RebuildRunStatusCommitting,
			"commit_completed_at": now,
			"updated_at":          now,
		}
		allReservedSlotsComplete := !run.WikiReduceRequired || run.WikiCompletedAt != nil
		if allReservedSlotsComplete {
			promote := tx.Model(&types.Knowledge{}).
				Where("id = ? AND parse_status = ? AND pending_subtasks_count = 0", knowledgeID, types.ParseStatusFinalizing).
				Updates(map[string]interface{}{
					"parse_status": types.ParseStatusCompleted,
					"processed_at": now,
					"updated_at":   now,
				})
			if promote.Error != nil {
				return promote.Error
			}
			promoted = promote.RowsAffected > 0
			if promoted {
				updates["status"] = types.RebuildRunStatusCompleted
				updates["completed_at"] = now
			}
		}
		return tx.Model(&types.KnowledgeRebuildRun{}).
			Where("tenant_id = ? AND id = ?", tenantID, runID).
			Updates(updates).Error
	})
	return promoted, err
}

func (r *knowledgeRebuildRunRepository) FinalizeWiki(
	ctx context.Context,
	tenantID uint64,
	runID, knowledgeID string,
	success bool,
	errorMessage string,
) (bool, error) {
	promoted := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var run types.KnowledgeRebuildRun
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("tenant_id = ? AND id = ?", tenantID, runID).
			First(&run).Error; err != nil {
			return err
		}
		if run.WikiCompletedAt != nil {
			return nil
		}
		if !containsRebuildStatus(activeRebuildRunStatuses, run.Status) {
			return nil
		}
		now := time.Now()
		if !success {
			if err := tx.Model(&types.Knowledge{}).
				Where("id = ?", knowledgeID).
				Updates(map[string]interface{}{
					"parse_status":           types.ParseStatusFailed,
					"pending_subtasks_count": 0,
					"error_message":          errorMessage,
					"updated_at":             now,
				}).Error; err != nil {
				return err
			}
			return tx.Model(&types.KnowledgeRebuildRun{}).
				Where("tenant_id = ? AND id = ?", tenantID, runID).
				Updates(map[string]interface{}{
					"status":            types.RebuildRunStatusFailed,
					"error_message":     errorMessage,
					"wiki_completed_at": now,
					"completed_at":      now,
					"updated_at":        now,
				}).Error
		}
		if err := tx.Model(&types.Knowledge{}).
			Where("id = ? AND parse_status = ? AND pending_subtasks_count > 0", knowledgeID, types.ParseStatusFinalizing).
			Updates(map[string]interface{}{
				"pending_subtasks_count": gorm.Expr("pending_subtasks_count - 1"),
				"updated_at":             now,
			}).Error; err != nil {
			return err
		}
		updates := map[string]interface{}{
			"status":            types.RebuildRunStatusCommitting,
			"error_message":     "",
			"wiki_completed_at": now,
			"updated_at":        now,
		}
		if run.CommitCompletedAt != nil {
			promote := tx.Model(&types.Knowledge{}).
				Where("id = ? AND parse_status = ? AND pending_subtasks_count = 0", knowledgeID, types.ParseStatusFinalizing).
				Updates(map[string]interface{}{
					"parse_status": types.ParseStatusCompleted,
					"processed_at": now,
					"updated_at":   now,
				})
			if promote.Error != nil {
				return promote.Error
			}
			promoted = promote.RowsAffected > 0
			if promoted {
				updates["status"] = types.RebuildRunStatusCompleted
				updates["completed_at"] = now
			}
		}
		return tx.Model(&types.KnowledgeRebuildRun{}).
			Where("tenant_id = ? AND id = ?", tenantID, runID).
			Updates(updates).Error
	})
	return promoted, err
}

func (r *knowledgeRebuildRunRepository) FailRun(
	ctx context.Context, tenantID uint64, runID, knowledgeID, errorMessage string,
) error {
	now := time.Now()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&types.Knowledge{}).
			Where("id = ?", knowledgeID).
			Updates(map[string]interface{}{
				"parse_status":           types.ParseStatusFailed,
				"pending_subtasks_count": 0,
				"error_message":          errorMessage,
				"updated_at":             now,
			}).Error; err != nil {
			return err
		}
		return tx.Model(&types.KnowledgeRebuildRun{}).
			Where("tenant_id = ? AND id = ?", tenantID, runID).
			Updates(map[string]interface{}{
				"status":        types.RebuildRunStatusFailed,
				"error_message": errorMessage,
				"completed_at":  now,
				"updated_at":    now,
			}).Error
	})
}

func (r *knowledgeRebuildRunRepository) setStatusAndFields(
	ctx context.Context,
	tenantID uint64,
	runID, status, errorMessage string,
	fields map[string]interface{},
) error {
	if fields == nil {
		fields = map[string]interface{}{}
	}
	fields["status"] = status
	fields["error_message"] = errorMessage
	fields["updated_at"] = time.Now()
	if status == types.RebuildRunStatusFailed {
		fields["completed_at"] = time.Now()
	}
	return r.db.WithContext(ctx).Model(&types.KnowledgeRebuildRun{}).
		Where("tenant_id = ? AND id = ? AND status IN ?", tenantID, runID, activeRebuildRunStatuses).
		Updates(fields).Error
}

func containsRebuildStatus(statuses []string, status string) bool {
	for _, candidate := range statuses {
		if candidate == status {
			return true
		}
	}
	return false
}
