package repository

import (
	"context"
	"errors"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const weightEpsilon = 1e-9

// messageFeedbackRepository implements interfaces.MessageFeedbackRepository
type messageFeedbackRepository struct {
	db *gorm.DB
}

// NewMessageFeedbackRepository creates a new message feedback repository
func NewMessageFeedbackRepository(db *gorm.DB) interfaces.MessageFeedbackRepository {
	return &messageFeedbackRepository{db: db}
}

func (r *messageFeedbackRepository) isPostgres() bool {
	return r.db.Dialector.Name() == "postgres"
}

// SyncMessageChunkRefs idempotently inserts answer->chunk reference rows.
func (r *messageFeedbackRepository) SyncMessageChunkRefs(
	ctx context.Context, refs []types.MessageChunkReference,
) error {
	if len(refs) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(&refs).Error
}

// ListChunkRefsByMessage returns the reference rows of one message.
func (r *messageFeedbackRepository) ListChunkRefsByMessage(
	ctx context.Context, messageID string,
) ([]types.MessageChunkReference, error) {
	var refs []types.MessageChunkReference
	if err := r.db.WithContext(ctx).
		Where("message_id = ?", messageID).
		Order("id ASC").
		Find(&refs).Error; err != nil {
		return nil, err
	}
	return refs, nil
}

// UpsertFeedback applies one rating mutation atomically. See the interface
// doc for the locking protocol; the short version is: lock the message row
// first (serializing all mutations of one answer), then the involved KB rows
// in sorted order (so the feedback epoch read here cannot race an admin
// reset), then adjust the feedback row, chunk counters and derived weights.
func (r *messageFeedbackRepository) UpsertFeedback(
	ctx context.Context,
	feedback *types.MessageFeedback,
	refs []types.MessageChunkReference,
	kbPolicies map[string]types.FeedbackKBPolicy,
) (string, error) {
	isPG := r.isPostgres()
	oldRating := ""

	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if isPG {
			var lockedIDs []string
			if err := tx.Model(&types.Message{}).
				Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("id = ?", feedback.MessageID).
				Pluck("id", &lockedIDs).Error; err != nil {
				return err
			}
			if len(lockedIDs) == 0 {
				return gorm.ErrRecordNotFound
			}
		}

		chunksByKB := groupChunkIDsByKB(refs)
		kbIDs := sortedKeys(chunksByKB)
		resetByKB, err := lockAndReadFeedbackEpochs(tx, isPG, kbIDs)
		if err != nil {
			return err
		}

		var existing types.MessageFeedback
		found := true
		if err := tx.Where(
			"message_id = ? AND user_id = ?", feedback.MessageID, feedback.UserID,
		).First(&existing).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			found = false
		}
		if found {
			oldRating = existing.Rating
		}

		newRating := ""
		if feedback.Rating != types.FeedbackRatingNone {
			newRating = feedback.Rating
		}

		if err := r.writeFeedbackRow(tx, feedback, &existing, found, newRating); err != nil {
			return err
		}

		for _, kbID := range kbIDs {
			chunkIDs := chunksByKB[kbID]
			oldEffective := oldRating
			if found && staleForEpoch(existing.UpdatedAt, resetByKB[kbID]) {
				oldEffective = ""
			}
			dLike, dDislike := types.FeedbackCounterDeltas(oldEffective, newRating)
			if dLike == 0 && dDislike == 0 {
				continue
			}
			if err := applyCounterDelta(tx, chunkIDs, "like_count", dLike); err != nil {
				return err
			}
			if err := applyCounterDelta(tx, chunkIDs, "dislike_count", dDislike); err != nil {
				return err
			}
			policy := kbPolicies[kbID]
			if err := refreshChunkWeights(
				tx, chunkIDs, policy.Config, types.FeedbackWeightTriggerFeedback, feedback.ID,
			); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return oldRating, nil
}

// writeFeedbackRow creates, updates or deletes the feedback row itself and
// leaves the caller-facing struct populated with the persisted state.
func (r *messageFeedbackRepository) writeFeedbackRow(
	tx *gorm.DB,
	feedback *types.MessageFeedback,
	existing *types.MessageFeedback,
	found bool,
	newRating string,
) error {
	now := time.Now().UTC()
	if newRating == "" {
		feedback.ID = ""
		feedback.Rating = ""
		if !found {
			return nil
		}
		return tx.Where("id = ?", existing.ID).Delete(&types.MessageFeedback{}).Error
	}
	if found {
		feedback.ID = existing.ID
		feedback.CreatedAt = existing.CreatedAt
		feedback.UpdatedAt = now
		return tx.Model(&types.MessageFeedback{}).
			Where("id = ?", existing.ID).
			Updates(map[string]interface{}{
				"rating":     newRating,
				"reasons":    feedback.Reasons,
				"comment":    feedback.Comment,
				"updated_at": now,
			}).Error
	}
	feedback.ID = uuid.New().String()
	feedback.CreatedAt = now
	feedback.UpdatedAt = now
	if err := tx.Create(feedback).Error; err != nil {
		// A concurrent first-time submit can slip in on engines without the
		// message row lock; fall back to updating the winner's row.
		var winner types.MessageFeedback
		if ferr := tx.Where(
			"message_id = ? AND user_id = ?", feedback.MessageID, feedback.UserID,
		).First(&winner).Error; ferr != nil {
			return err
		}
		feedback.ID = winner.ID
		feedback.CreatedAt = winner.CreatedAt
		return tx.Model(&types.MessageFeedback{}).
			Where("id = ?", winner.ID).
			Updates(map[string]interface{}{
				"rating":     newRating,
				"reasons":    feedback.Reasons,
				"comment":    feedback.Comment,
				"updated_at": now,
			}).Error
	}
	return nil
}

// lockAndReadFeedbackEpochs re-reads each involved KB's feedback_reset_at
// inside the transaction, under FOR UPDATE on postgres, in sorted key order.
// An admin reset updates the same rows, so whichever side commits first is
// fully visible to the other.
func lockAndReadFeedbackEpochs(
	tx *gorm.DB, isPG bool, kbIDs []string,
) (map[string]*time.Time, error) {
	resetByKB := make(map[string]*time.Time, len(kbIDs))
	if len(kbIDs) == 0 {
		return resetByKB, nil
	}
	q := tx.Model(&types.KnowledgeBase{}).
		Select("id", "feedback_reset_at").
		Where("id IN ?", kbIDs).
		Order("id ASC")
	if isPG {
		q = q.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var rows []types.KnowledgeBase
	if err := q.Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		resetByKB[row.ID] = row.FeedbackResetAt
	}
	return resetByKB, nil
}

// staleForEpoch reports whether a rating last updated at updatedAt predates
// the KB's feedback reset and therefore no longer counts.
func staleForEpoch(updatedAt time.Time, resetAt *time.Time) bool {
	return resetAt != nil && updatedAt.Before(*resetAt)
}

// applyCounterDelta shifts one counter column by ±1 for a chunk id set.
// Decrements are guarded so a counter can never go negative (GREATEST is not
// available on SQLite). UpdateColumn intentionally skips updated_at: feedback
// must not look like a content change.
func applyCounterDelta(tx *gorm.DB, chunkIDs []string, column string, delta int) error {
	if delta == 0 || len(chunkIDs) == 0 {
		return nil
	}
	q := tx.Model(&types.Chunk{}).Where("id IN ?", chunkIDs)
	if delta > 0 {
		return q.UpdateColumn(column, gorm.Expr(column+" + ?", delta)).Error
	}
	return q.Where(column+" > 0").
		UpdateColumn(column, gorm.Expr(column+" - ?", -delta)).Error
}

// refreshChunkWeights re-derives positive_rate, recall_weight and the
// needs-optimization flag of the given chunks from their (already updated)
// counters, logging every weight change.
func refreshChunkWeights(
	tx *gorm.DB,
	chunkIDs []string,
	cfg *types.RetrievalConfig,
	trigger string,
	feedbackID string,
) error {
	if len(chunkIDs) == 0 {
		return nil
	}
	var chunks []types.Chunk
	if err := tx.Model(&types.Chunk{}).
		Select("id", "tenant_id", "knowledge_base_id", "like_count", "dislike_count",
			"positive_rate", "recall_weight", "flags").
		Where("id IN ?", chunkIDs).
		Order("id ASC").
		Find(&chunks).Error; err != nil {
		return err
	}
	var logs []types.ChunkWeightLog
	for i := range chunks {
		chunk := &chunks[i]
		rate := types.PositiveRateOf(chunk.LikeCount, chunk.DislikeCount)
		weight, needsOpt := types.ComputeRecallWeight(chunk.LikeCount, chunk.DislikeCount, cfg)

		updates := map[string]interface{}{}
		if math.Abs(rate-chunk.PositiveRate) > weightEpsilon {
			updates["positive_rate"] = rate
		}
		if math.Abs(weight-chunk.RecallWeight) > weightEpsilon {
			updates["recall_weight"] = weight
			logs = append(logs, types.ChunkWeightLog{
				TenantID:        chunk.TenantID,
				KnowledgeBaseID: chunk.KnowledgeBaseID,
				ChunkID:         chunk.ID,
				OldWeight:       chunk.RecallWeight,
				NewWeight:       weight,
				PositiveRate:    rate,
				TriggerSource:   trigger,
				FeedbackID:      feedbackID,
			})
		}
		newFlags := chunk.Flags
		if needsOpt {
			newFlags = newFlags.SetFlag(types.ChunkFlagNeedsOptimization)
		} else {
			newFlags = newFlags.ClearFlag(types.ChunkFlagNeedsOptimization)
		}
		if newFlags != chunk.Flags {
			updates["flags"] = newFlags
		}
		if len(updates) == 0 {
			continue
		}
		if err := tx.Model(&types.Chunk{}).
			Where("id = ?", chunk.ID).
			UpdateColumns(updates).Error; err != nil {
			return err
		}
	}
	if len(logs) > 0 {
		if err := tx.Create(&logs).Error; err != nil {
			return err
		}
	}
	return nil
}

// GetByMessageAndUser returns one user's feedback on one message, nil when
// none exists.
func (r *messageFeedbackRepository) GetByMessageAndUser(
	ctx context.Context, messageID, userID string,
) (*types.MessageFeedback, error) {
	var fb types.MessageFeedback
	if err := r.db.WithContext(ctx).Where(
		"message_id = ? AND user_id = ?", messageID, userID,
	).First(&fb).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &fb, nil
}

// ListRatingsByMessageIDs returns messageID -> rating for one user.
func (r *messageFeedbackRepository) ListRatingsByMessageIDs(
	ctx context.Context, userID string, messageIDs []string,
) (map[string]string, error) {
	ratings := make(map[string]string, len(messageIDs))
	if len(messageIDs) == 0 {
		return ratings, nil
	}
	var rows []types.MessageFeedback
	if err := r.db.WithContext(ctx).
		Select("message_id", "rating").
		Where("user_id = ? AND message_id IN ?", userID, messageIDs).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		ratings[row.MessageID] = row.Rating
	}
	return ratings, nil
}

var chunkStatsSortColumns = map[string]string{
	"like_count":    "like_count",
	"dislike_count": "dislike_count",
	"positive_rate": "positive_rate",
	"recall_weight": "recall_weight",
	"total":         "(like_count + dislike_count)",
}

// ListChunkStats pages through the rated chunks of one KB. The listing scans
// the denormalized counters on chunks (portable, index-friendly) and only
// joins feedback rows for the current page to aggregate dislike reasons.
func (r *messageFeedbackRepository) ListChunkStats(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	resetAt *time.Time,
	query *interfaces.ChunkFeedbackStatsQuery,
) ([]*types.ChunkFeedbackStat, int64, error) {
	if query == nil {
		query = &interfaces.ChunkFeedbackStatsQuery{}
	}
	if query.Pagination == nil {
		query.Pagination = &types.Pagination{}
	}
	minTotal := query.MinTotal
	if minTotal < 1 {
		minTotal = 1
	}
	base := r.db.WithContext(ctx).Model(&types.Chunk{}).
		Where("tenant_id = ? AND knowledge_base_id = ?", tenantID, kbID).
		Where("(like_count + dislike_count) >= ?", minTotal)
	if query.MinRate != nil {
		base = base.Where("positive_rate >= ?", *query.MinRate)
	}
	if query.MaxRate != nil {
		base = base.Where("positive_rate <= ?", *query.MaxRate)
	}
	if query.NeedsOptimizationOnly {
		base = base.Where("(flags & ?) != 0", int(types.ChunkFlagNeedsOptimization))
	}

	var total int64
	if err := base.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	sortColumn, ok := chunkStatsSortColumns[query.SortBy]
	if !ok {
		sortColumn = "dislike_count"
	}
	direction := "DESC"
	if strings.EqualFold(query.Order, "asc") {
		direction = "ASC"
	}

	page := query.Pagination.GetPage()
	pageSize := query.Pagination.GetPageSize()
	var chunks []types.Chunk
	if err := base.Session(&gorm.Session{}).
		Select("id", "knowledge_id", "knowledge_base_id", "content", "like_count",
			"dislike_count", "positive_rate", "recall_weight", "flags").
		Order(sortColumn + " " + direction).
		Order("id ASC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&chunks).Error; err != nil {
		return nil, 0, err
	}
	if len(chunks) == 0 {
		return []*types.ChunkFeedbackStat{}, total, nil
	}

	chunkIDs := make([]string, 0, len(chunks))
	knowledgeIDs := make(map[string]bool)
	for _, chunk := range chunks {
		chunkIDs = append(chunkIDs, chunk.ID)
		knowledgeIDs[chunk.KnowledgeID] = true
	}

	reasonsByChunk, err := r.aggregateDislikeReasons(ctx, chunkIDs, resetAt)
	if err != nil {
		return nil, 0, err
	}
	sessionsByChunk, err := r.countSessionsByChunk(ctx, chunkIDs)
	if err != nil {
		return nil, 0, err
	}
	titles, err := r.loadKnowledgeTitles(ctx, sortedKeys(knowledgeIDs))
	if err != nil {
		return nil, 0, err
	}

	stats := make([]*types.ChunkFeedbackStat, 0, len(chunks))
	for _, chunk := range chunks {
		stats = append(stats, &types.ChunkFeedbackStat{
			ChunkID:           chunk.ID,
			KnowledgeID:       chunk.KnowledgeID,
			KnowledgeTitle:    titles[chunk.KnowledgeID],
			ContentPreview:    truncateRunes(chunk.Content, 120),
			LikeCount:         chunk.LikeCount,
			DislikeCount:      chunk.DislikeCount,
			Total:             chunk.LikeCount + chunk.DislikeCount,
			PositiveRate:      chunk.PositiveRate,
			RecallWeight:      chunk.RecallWeight,
			NeedsOptimization: chunk.Flags.HasFlag(types.ChunkFlagNeedsOptimization),
			DislikeReasons:    reasonsByChunk[chunk.ID],
			SessionCount:      sessionsByChunk[chunk.ID],
		})
	}
	return stats, total, nil
}

// aggregateDislikeReasons joins the page's chunks to their rated messages and
// counts preset dislike reasons per chunk, honouring the feedback epoch.
func (r *messageFeedbackRepository) aggregateDislikeReasons(
	ctx context.Context, chunkIDs []string, resetAt *time.Time,
) (map[string]map[string]int, error) {
	type reasonRow struct {
		ChunkID string
		Rating  string
		Reasons types.FeedbackReasons
	}
	q := r.db.WithContext(ctx).
		Table("message_chunk_references AS r").
		Select("r.chunk_id AS chunk_id, f.rating AS rating, f.reasons AS reasons").
		Joins("INNER JOIN message_feedbacks f ON f.message_id = r.message_id").
		Where("r.chunk_id IN ?", chunkIDs).
		Where("f.rating = ?", types.FeedbackRatingDislike)
	if resetAt != nil {
		q = q.Where("f.updated_at >= ?", *resetAt)
	}
	var rows []reasonRow
	if err := q.Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make(map[string]map[string]int)
	for _, row := range rows {
		if len(row.Reasons) == 0 {
			continue
		}
		counts := result[row.ChunkID]
		if counts == nil {
			counts = make(map[string]int)
			result[row.ChunkID] = counts
		}
		for _, reason := range row.Reasons {
			counts[reason]++
		}
	}
	return result, nil
}

// countSessionsByChunk counts distinct QA sessions that referenced each
// chunk. This is a usage statistic, deliberately independent from ratings
// and the feedback epoch.
func (r *messageFeedbackRepository) countSessionsByChunk(
	ctx context.Context, chunkIDs []string,
) (map[string]int, error) {
	type sessionRow struct {
		ChunkID string
		Count   int
	}
	var rows []sessionRow
	if err := r.db.WithContext(ctx).
		Table("message_chunk_references").
		Select("chunk_id AS chunk_id, COUNT(DISTINCT session_id) AS count").
		Where("chunk_id IN ?", chunkIDs).
		Group("chunk_id").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make(map[string]int, len(rows))
	for _, row := range rows {
		result[row.ChunkID] = row.Count
	}
	return result, nil
}

func (r *messageFeedbackRepository) loadKnowledgeTitles(
	ctx context.Context, knowledgeIDs []string,
) (map[string]string, error) {
	titles := make(map[string]string, len(knowledgeIDs))
	if len(knowledgeIDs) == 0 {
		return titles, nil
	}
	var rows []types.Knowledge
	if err := r.db.WithContext(ctx).Model(&types.Knowledge{}).
		Select("id", "title").
		Where("id IN ?", knowledgeIDs).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		titles[row.ID] = row.Title
	}
	return titles, nil
}

// ListWeightLogs pages through the weight change audit of one KB, newest first.
func (r *messageFeedbackRepository) ListWeightLogs(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	chunkID string,
	p *types.Pagination,
) ([]*types.ChunkWeightLog, int64, error) {
	if p == nil {
		p = &types.Pagination{}
	}
	base := r.db.WithContext(ctx).Model(&types.ChunkWeightLog{}).
		Where("tenant_id = ? AND knowledge_base_id = ?", tenantID, kbID)
	if chunkID != "" {
		base = base.Where("chunk_id = ?", chunkID)
	}
	var total int64
	if err := base.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var logs []*types.ChunkWeightLog
	if err := base.Session(&gorm.Session{}).
		Order("id DESC").
		Offset((p.GetPage() - 1) * p.GetPageSize()).
		Limit(p.GetPageSize()).
		Find(&logs).Error; err != nil {
		return nil, 0, err
	}
	return logs, total, nil
}

// ResetKnowledgeBaseFeedback advances the KB feedback epoch and restores all
// chunk feedback state to neutral. Ratings and reference rows are kept: the
// per-message rating display stays intact, while stats/counters start fresh
// because pre-reset ratings fail the epoch check from now on.
func (r *messageFeedbackRepository) ResetKnowledgeBaseFeedback(
	ctx context.Context, tenantID uint64, kbID string,
) (int64, error) {
	var resetChunks int64
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()
		// The UPDATE takes (and holds) the KB row lock that UpsertFeedback's
		// epoch read competes for, so no rating transaction can straddle the
		// epoch bump.
		res := tx.Model(&types.KnowledgeBase{}).
			Where("id = ? AND tenant_id = ?", kbID, tenantID).
			UpdateColumn("feedback_reset_at", now)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}

		var weighted []types.Chunk
		if err := tx.Model(&types.Chunk{}).
			Select("id", "tenant_id", "knowledge_base_id", "recall_weight").
			Where("tenant_id = ? AND knowledge_base_id = ?", tenantID, kbID).
			Where("recall_weight NOT BETWEEN ? AND ?", 1-weightEpsilon, 1+weightEpsilon).
			Order("id ASC").
			Find(&weighted).Error; err != nil {
			return err
		}

		res = tx.Model(&types.Chunk{}).
			Where("tenant_id = ? AND knowledge_base_id = ?", tenantID, kbID).
			Where("like_count > 0 OR dislike_count > 0 OR recall_weight NOT BETWEEN ? AND ? OR (flags & ?) != 0",
				1-weightEpsilon, 1+weightEpsilon, int(types.ChunkFlagNeedsOptimization)).
			UpdateColumns(map[string]interface{}{
				"like_count":    0,
				"dislike_count": 0,
				"positive_rate": 0,
				"recall_weight": 1,
				"flags":         gorm.Expr("flags & ?", ^int(types.ChunkFlagNeedsOptimization)),
			})
		if res.Error != nil {
			return res.Error
		}
		resetChunks = res.RowsAffected

		if len(weighted) == 0 {
			return nil
		}
		logs := make([]types.ChunkWeightLog, 0, len(weighted))
		for _, chunk := range weighted {
			logs = append(logs, types.ChunkWeightLog{
				TenantID:        chunk.TenantID,
				KnowledgeBaseID: chunk.KnowledgeBaseID,
				ChunkID:         chunk.ID,
				OldWeight:       chunk.RecallWeight,
				NewWeight:       1,
				PositiveRate:    0,
				TriggerSource:   types.FeedbackWeightTriggerReset,
			})
		}
		return tx.Create(&logs).Error
	})
	if err != nil {
		return 0, err
	}
	return resetChunks, nil
}

// ListChunkWeights returns chunkID -> recall_weight for the given candidates,
// omitting neutral weights so the common case stays allocation-light.
func (r *messageFeedbackRepository) ListChunkWeights(
	ctx context.Context, chunkIDs []string,
) (map[string]float64, error) {
	weights := make(map[string]float64)
	if len(chunkIDs) == 0 {
		return weights, nil
	}
	var rows []types.Chunk
	if err := r.db.WithContext(ctx).Model(&types.Chunk{}).
		Select("id", "recall_weight").
		Where("id IN ?", chunkIDs).
		Where("recall_weight NOT BETWEEN ? AND ?", 1-weightEpsilon, 1+weightEpsilon).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		weights[row.ID] = row.RecallWeight
	}
	return weights, nil
}

// RecomputeFeedbackWeights re-derives all rated chunks of a tenant after a
// retrieval config change. confirmStale runs right before commit so a slow
// recomputation aborts instead of overwriting a newer config's results.
func (r *messageFeedbackRepository) RecomputeFeedbackWeights(
	ctx context.Context,
	tenantID uint64,
	cfgByKB map[string]*types.RetrievalConfig,
	confirmStale func(ctx context.Context) (bool, error),
) (int64, error) {
	isPG := r.isPostgres()
	var changed int64
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		q := tx.Model(&types.Chunk{}).
			Select("id", "tenant_id", "knowledge_base_id", "like_count", "dislike_count",
				"positive_rate", "recall_weight", "flags").
			Where("tenant_id = ? AND (like_count + dislike_count) > 0", tenantID).
			Order("id ASC")
		if isPG {
			q = q.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		var chunks []types.Chunk
		if err := q.Find(&chunks).Error; err != nil {
			return err
		}

		var logs []types.ChunkWeightLog
		for i := range chunks {
			chunk := &chunks[i]
			cfg := cfgByKB[chunk.KnowledgeBaseID]
			if cfg == nil {
				cfg = cfgByKB[""]
			}
			rate := types.PositiveRateOf(chunk.LikeCount, chunk.DislikeCount)
			weight, needsOpt := types.ComputeRecallWeight(chunk.LikeCount, chunk.DislikeCount, cfg)

			updates := map[string]interface{}{}
			if math.Abs(rate-chunk.PositiveRate) > weightEpsilon {
				updates["positive_rate"] = rate
			}
			if math.Abs(weight-chunk.RecallWeight) > weightEpsilon {
				updates["recall_weight"] = weight
				logs = append(logs, types.ChunkWeightLog{
					TenantID:        chunk.TenantID,
					KnowledgeBaseID: chunk.KnowledgeBaseID,
					ChunkID:         chunk.ID,
					OldWeight:       chunk.RecallWeight,
					NewWeight:       weight,
					PositiveRate:    rate,
					TriggerSource:   types.FeedbackWeightTriggerConfig,
				})
			}
			newFlags := chunk.Flags
			if needsOpt {
				newFlags = newFlags.SetFlag(types.ChunkFlagNeedsOptimization)
			} else {
				newFlags = newFlags.ClearFlag(types.ChunkFlagNeedsOptimization)
			}
			if newFlags != chunk.Flags {
				updates["flags"] = newFlags
			}
			if len(updates) == 0 {
				continue
			}
			if err := tx.Model(&types.Chunk{}).
				Where("id = ?", chunk.ID).
				UpdateColumns(updates).Error; err != nil {
				return err
			}
			changed++
		}
		if len(logs) > 0 {
			if err := tx.Create(&logs).Error; err != nil {
				return err
			}
		}
		if confirmStale != nil {
			stale, err := confirmStale(ctx)
			if err != nil {
				return err
			}
			if stale {
				return types.ErrFeedbackRecomputeStale
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return changed, nil
}

// groupChunkIDsByKB buckets deduplicated chunk ids by knowledge base.
func groupChunkIDsByKB(refs []types.MessageChunkReference) map[string][]string {
	seen := make(map[string]bool, len(refs))
	groups := make(map[string][]string)
	for _, ref := range refs {
		if ref.ChunkID == "" || ref.KnowledgeBaseID == "" || seen[ref.ChunkID] {
			continue
		}
		seen[ref.ChunkID] = true
		groups[ref.KnowledgeBaseID] = append(groups[ref.KnowledgeBaseID], ref.ChunkID)
	}
	for _, ids := range groups {
		sort.Strings(ids)
	}
	return groups
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func truncateRunes(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + "…"
}
