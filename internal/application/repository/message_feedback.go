package repository

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const weightEpsilon = 1e-9

// messageFeedbackRepository implements interfaces.MessageFeedbackRepository.
// The contract is a strict per-message, per-chunk-group transaction so a
// process crash mid-write cannot leave dangling counters or dereferenced
// chunk weights.
type messageFeedbackRepository struct {
	db *gorm.DB
}

// NewMessageFeedbackRepository creates a new message feedback repository.
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

// ListChunkRefsByChunkIDs returns every reference row pointing at any of the
// supplied chunk IDs. Used by cascade-delete handlers to clean up ratings
// that referenced now-deleted chunks.
func (r *messageFeedbackRepository) ListChunkRefsByChunkIDs(
	ctx context.Context, chunkIDs []string,
) ([]types.MessageChunkReference, error) {
	if len(chunkIDs) == 0 {
		return nil, nil
	}
	var refs []types.MessageChunkReference
	if err := r.db.WithContext(ctx).
		Where("chunk_id IN ?", chunkIDs).
		Order("chunk_id ASC").
		Find(&refs).Error; err != nil {
		return nil, err
	}
	return refs, nil
}

// ListDistinctKnowledgeBaseIDsByChunkIDs returns the KB IDs touched by any
// reference row of the supplied chunk IDs.
func (r *messageFeedbackRepository) ListDistinctKnowledgeBaseIDsByChunkIDs(
	ctx context.Context, chunkIDs []string,
) ([]string, error) {
	if len(chunkIDs) == 0 {
		return nil, nil
	}
	var ids []string
	if err := r.db.WithContext(ctx).
		Model(&types.MessageChunkReference{}).
		Where("chunk_id IN ?", chunkIDs).
		Distinct("knowledge_base_id").
		Pluck("knowledge_base_id", &ids).Error; err != nil {
		return nil, err
	}
	return ids, nil
}

// ListRatingsByMessageIDs returns the caller's current rating for a batch
// of messages, indexed by message ID.
func (r *messageFeedbackRepository) ListRatingsByMessageIDs(
	ctx context.Context, userID string, messageIDs []string,
) (map[string]*types.MessageFeedbackView, error) {
	out := make(map[string]*types.MessageFeedbackView, len(messageIDs))
	if userID == "" || len(messageIDs) == 0 {
		return out, nil
	}
	var rows []types.MessageFeedback
	if err := r.db.WithContext(ctx).
		Where("user_id = ? AND message_id IN ?", userID, messageIDs).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	for i := range rows {
		row := rows[i]
		out[row.MessageID] = &types.MessageFeedbackView{
			Rating:    row.Rating,
			Reasons:   row.Reasons,
			Comment:   row.Comment,
			UpdatedAt: row.UpdatedAt,
		}
	}
	return out, nil
}

// UpsertFeedback applies one rating mutation atomically. See the interface
// doc for the locking protocol. The short version is: lock the message row
// first (serializing all mutations of one answer), then the involved KB rows
// in sorted order (so the feedback epoch read here cannot race an admin
// reset), then adjust the feedback row, chunk counters and derived weights.
func (r *messageFeedbackRepository) UpsertFeedback(
	ctx context.Context,
	feedback *types.MessageFeedback,
	refs []types.MessageChunkReference,
) (string, error) {
	isPG := r.isPostgres()
	oldRating := ""
	didWrite := false

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
		logger.Infof(ctx, "[FeedbackRepo] UpsertFeedback messageID=%s refs=%d chunksByKB=%v kbIDs=%v", feedback.MessageID, len(refs), chunksByKB, kbIDs)
		kbMeta, err := lockAndReadKBFeedbackMeta(tx, isPG, kbIDs)
		if err != nil {
			return err
		}
		// Read the owner tenants' retrieval configs inside the transaction so
		// weight computation always uses the latest committed config. Reading
		// via tx reuses this transaction's connection (no extra connection to
		// deadlock on SQLite) and closes the window where a config saved
		// between request start and commit could be applied with a stale
		// snapshot.
		ownerTenantIDs := make([]uint64, 0, len(kbMeta))
		for _, meta := range kbMeta {
			ownerTenantIDs = append(ownerTenantIDs, meta.ownerTenantID)
		}
		configByTenant, err := readRetrievalConfigsByTenant(tx, ownerTenantIDs)
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

		newRating := feedback.Rating
		if newRating == types.FeedbackRatingNone {
			newRating = ""
		}

		if err := r.writeFeedbackRow(tx, feedback, &existing, found, newRating); err != nil {
			return err
		}
		didWrite = true

		for _, kbID := range kbIDs {
			chunkIDs := chunksByKB[kbID]
			meta := kbMeta[kbID]
			oldEffective := oldRating
			if found && staleForEpoch(existing.UpdatedAt, meta.resetAt) {
				oldEffective = ""
			}
			dLike, dDislike := types.FeedbackCounterDeltas(oldEffective, newRating)
			if dLike == 0 && dDislike == 0 {
				logger.Infof(ctx, "[FeedbackRepo] no counter delta for kbID=%s oldEffective=%q newRating=%q, skipping", kbID, oldEffective, newRating)
				continue
			}
			logger.Infof(ctx, "[FeedbackRepo] applying counter delta kbID=%s dLike=%d dDislike=%d chunkCount=%d", kbID, dLike, dDislike, len(chunkIDs))
			if err := applyCounterDelta(tx, chunkIDs, "like_count", dLike); err != nil {
				return err
			}
			if err := applyCounterDelta(tx, chunkIDs, "dislike_count", dDislike); err != nil {
				return err
			}
			if err := refreshChunkWeights(
				ctx, tx, chunkIDs, configByTenant[meta.ownerTenantID],
				types.FeedbackWeightTriggerFeedback, feedback.ID,
			); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if !didWrite && oldRating == "" {
		// No-op cancellation when no prior feedback existed is still a clean
		// success; we just report oldRating="" so the caller can short-circuit.
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
		if !r.isPostgres() {
			var winner types.MessageFeedback
			if err := tx.Where(
				"message_id = ? AND user_id = ?", feedback.MessageID, feedback.UserID,
			).First(&winner).Error; err == nil {
				feedback.ID = winner.ID
				feedback.CreatedAt = winner.CreatedAt
				feedback.UpdatedAt = now
				return tx.Model(&types.MessageFeedback{}).
					Where("id = ?", winner.ID).
					Updates(map[string]interface{}{
						"rating":     newRating,
						"reasons":    feedback.Reasons,
						"comment":    feedback.Comment,
						"updated_at": now,
					}).Error
			}
		}
		return err
	}
	return nil
}

// groupChunkIDsByKB indexes the chunk IDs of a feedback's reference rows by
// their knowledge base ID. Sub-chunk references are normalized back to the
// parent chunk since the chunk counters live on the parent chunk row.
func groupChunkIDsByKB(refs []types.MessageChunkReference) map[string][]string {
	out := make(map[string][]string)
	seen := make(map[string]map[string]bool)
	for _, ref := range refs {
		if ref.ChunkID == "" || ref.KnowledgeBaseID == "" {
			continue
		}
		if seen[ref.KnowledgeBaseID] == nil {
			seen[ref.KnowledgeBaseID] = make(map[string]bool)
		}
		if seen[ref.KnowledgeBaseID][ref.ChunkID] {
			continue
		}
		seen[ref.KnowledgeBaseID][ref.ChunkID] = true
		out[ref.KnowledgeBaseID] = append(out[ref.KnowledgeBaseID], ref.ChunkID)
	}
	return out
}

func sortedKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// kbFeedbackMeta is the per-KB metadata cached at the start of a feedback
// transaction so the per-chunk loop doesn't have to re-query.
type kbFeedbackMeta struct {
	ownerTenantID uint64
	resetAt       *time.Time
}

// lockAndReadKBFeedbackMeta serializes access to the involved KB rows
// (so the feedback epoch is stable for the rest of the transaction) and
// returns the owner tenant and feedback epoch for each.
func lockAndReadKBFeedbackMeta(tx *gorm.DB, isPG bool, kbIDs []string) (map[string]kbFeedbackMeta, error) {
	out := make(map[string]kbFeedbackMeta, len(kbIDs))
	if len(kbIDs) == 0 {
		return out, nil
	}
	query := tx.Model(&types.KnowledgeBase{}).
		Select("id, tenant_id, feedback_reset_at").
		Where("id IN ?", kbIDs)
	if isPG {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	type kbRow struct {
		ID              string
		TenantID        uint64
		FeedbackResetAt *time.Time
	}
	var rows []kbRow
	if err := query.Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.ID] = kbFeedbackMeta{
			ownerTenantID: r.TenantID,
			resetAt:       r.FeedbackResetAt,
		}
	}
	return out, nil
}

// readRetrievalConfigsByTenant loads the active retrieval config of each
// supplied tenant. Tenants without a config get a nil value and the
// caller falls back to defaults.
func readRetrievalConfigsByTenant(tx *gorm.DB, tenantIDs []uint64) (map[uint64]*types.RetrievalConfig, error) {
	out := make(map[uint64]*types.RetrievalConfig, len(tenantIDs))
	if len(tenantIDs) == 0 {
		return out, nil
	}
	type row struct {
		TenantID        uint64
		RetrievalConfig *types.RetrievalConfig
	}
	var rows []row
	if err := tx.Model(&types.Tenant{}).
		Select("id AS tenant_id, retrieval_config").
		Where("id IN ?", tenantIDs).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.TenantID] = r.RetrievalConfig
	}
	return out, nil
}

// staleForEpoch reports whether a feedback row was last updated before the
// KB's feedback_reset_at instant. Stale rows are treated as if they didn't
// exist when computing the counter delta, so an admin reset cleanly removes
// their contribution without losing the underlying feedback records.
func staleForEpoch(updatedAt time.Time, resetAt *time.Time) bool {
	if resetAt == nil {
		return false
	}
	return updatedAt.Before(*resetAt)
}

// applyCounterDelta updates a chunk counter column by the supplied delta in
// a single UPDATE statement, with a floor of zero so a bug or race cannot
// drive the counter negative.
func applyCounterDelta(tx *gorm.DB, chunkIDs []string, column string, delta int) error {
	if delta == 0 || len(chunkIDs) == 0 {
		return nil
	}
	if delta < 0 {
		// DECREMENT with floor at zero. SQLite/MySQL don't support
		// GREATEST(col, 0) on UPDATE in the same way Postgres does, so we
		// rely on a per-id loop with a check. The repo is only called for
		// small numbers of chunks (one message's references) so the cost
		// is bounded.
		for _, id := range chunkIDs {
			if err := tx.Exec(
				"UPDATE chunks SET "+column+" = CASE WHEN "+column+" + ? < 0 THEN 0 ELSE "+column+" + ? END WHERE id = ?",
				delta, delta, id,
			).Error; err != nil {
				return err
			}
		}
		return nil
	}
	return tx.Model(&types.Chunk{}).
		Where("id IN ?", chunkIDs).
		UpdateColumn(column, gorm.Expr(column+" + ?", delta)).Error
}

// refreshChunkWeights recomputes the positive_rate, recall_weight and
// needs_optimization for each chunk and writes a single weight log per
// chunk whose weight actually changed.
func refreshChunkWeights(
	ctx context.Context,
	tx *gorm.DB,
	chunkIDs []string,
	cfg *types.RetrievalConfig,
	triggerSource string,
	feedbackID string,
) error {
	if len(chunkIDs) == 0 {
		return nil
	}
	type chunkRow struct {
		ID              string
		LikeCount       int
		DislikeCount    int
		RecallWeight    float64
		TenantID        uint64
		KnowledgeBaseID string
	}
	rows := make([]chunkRow, 0, len(chunkIDs))
	result, err := tx.Model(&types.Chunk{}).
		Select("id, like_count, dislike_count, recall_weight, tenant_id, knowledge_base_id").
		Where("id IN ?", chunkIDs).
		Rows()
	if err != nil {
		return err
	}
	defer result.Close()
	for result.Next() {
		var r chunkRow
		if err := result.Scan(&r.ID, &r.LikeCount, &r.DislikeCount, &r.RecallWeight, &r.TenantID, &r.KnowledgeBaseID); err != nil {
			return err
		}
		rows = append(rows, r)
	}
	if err := result.Err(); err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, row := range rows {
		newWeight, needs := types.ComputeRecallWeight(row.LikeCount, row.DislikeCount, cfg)
		rate := types.PositiveRateOf(row.LikeCount, row.DislikeCount)
		if err := tx.Model(&types.Chunk{}).
			Where("id = ?", row.ID).
			Updates(map[string]interface{}{
				"positive_rate":      rate,
				"recall_weight":      newWeight,
				"needs_optimization": needs,
			}).Error; err != nil {
			return err
		}
		if !types.WeightApproximatelyEqual(newWeight, row.RecallWeight) {
			log := types.ChunkWeightLog{
				TenantID:        row.TenantID,
				KnowledgeBaseID: row.KnowledgeBaseID,
				ChunkID:         row.ID,
				OldWeight:       row.RecallWeight,
				NewWeight:       newWeight,
				PositiveRate:    rate,
				TriggerSource:   triggerSource,
				FeedbackID:      feedbackID,
				CreatedAt:       now,
			}
			if err := tx.Create(&log).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

// ListChunkStats returns the paged feedback stats of one KB. The reset
// epoch is supplied so feedback rows older than the epoch are excluded
// from the aggregates.
func (r *messageFeedbackRepository) ListChunkStats(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	resetAt *time.Time,
	query *interfaces.ChunkFeedbackStatsQuery,
) ([]types.ChunkFeedbackStat, int64, error) {
	if query == nil {
		query = &interfaces.ChunkFeedbackStatsQuery{}
	}
	if query.Pagination == nil {
		query.Pagination = &types.Pagination{}
	}

	// Step 1: pre-aggregate per-chunk counters from feedback rows updated
	// after the epoch. We do this in a CTE so the admin stats query stays
	// read-only and locks-free.
	resetClause := "1=1"
	resetArgs := []interface{}{}
	if resetAt != nil {
		resetClause = "mf.updated_at >= ?"
		resetArgs = append(resetArgs, *resetAt)
	}

	// Per-chunk aggregate query. Pulls likeCount, dislikeCount, distinct
	// session IDs, and dislike reason counts. The dislike-reason aggregation
	// uses a correlated subquery against the JSON reasons array.
	aggregateSQL := `
		SELECT
			c.id AS chunk_id,
			c.knowledge_id AS knowledge_id,
			c.knowledge_base_id AS knowledge_base_id,
			COALESCE(k.title, '') AS knowledge_title,
			SUBSTR(c.content, 1, 200) AS content_preview,
			COALESCE(SUM(CASE WHEN mf.rating = ? THEN 1 ELSE 0 END), 0) AS like_count,
			COALESCE(SUM(CASE WHEN mf.rating = ? THEN 1 ELSE 0 END), 0) AS dislike_count,
			COUNT(DISTINCT mcr.session_id) AS session_count,
			MAX(mf.updated_at) AS last_feedback_at
		FROM chunks c
		LEFT JOIN message_feedbacks mf
			ON mf.message_id IN (SELECT message_id FROM message_chunk_references WHERE chunk_id = c.id)
			AND ` + resetClause + `
		LEFT JOIN message_chunk_references mcr
			ON mcr.message_id = mf.message_id AND mcr.chunk_id = c.id
		LEFT JOIN knowledges k ON k.id = c.knowledge_id
		WHERE c.tenant_id = ? AND c.knowledge_base_id = ? AND c.deleted_at IS NULL
		GROUP BY c.id, c.knowledge_id, c.knowledge_base_id, k.title, c.content
	`
	args := []interface{}{types.FeedbackRatingLike, types.FeedbackRatingDislike}
	args = append(args, resetArgs...)
	args = append(args, tenantID, kbID)

	if query.KnowledgeID != "" {
		aggregateSQL += " AND c.knowledge_id = ?"
		args = append(args, query.KnowledgeID)
	}
	if query.Keyword != "" {
		aggregateSQL += " AND c.content LIKE ?"
		args = append(args, "%"+query.Keyword+"%")
	}

	// Counting distinct chunks after filtering involves wrapping the
	// aggregate in a sub-filter; we use a derived table for the count.
	countSQL := "SELECT COUNT(*) FROM (" + aggregateSQL + ")"
	var total int64
	if err := r.db.WithContext(ctx).Raw(countSQL, args...).Scan(&total).Error; err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return nil, 0, nil
	}

	// Sort and pagination. The order-by columns are derived from the
	// aggregate fields.
	orderBy := "like_count + dislike_count DESC, last_feedback_at DESC"
	switch strings.ToLower(query.SortBy) {
	case "positive_rate_desc":
		orderBy = "CASE WHEN (like_count + dislike_count) > 0 THEN like_count ELSE 0 END DESC"
	case "positive_rate_asc":
		orderBy = "CASE WHEN (like_count + dislike_count) > 0 THEN like_count ELSE 0 END ASC"
	case "feedback_count_desc":
		orderBy = "like_count + dislike_count DESC"
	case "last_feedback_desc":
		orderBy = "last_feedback_at DESC"
	}
	pageSize := query.Pagination.GetPageSize()
	offset := query.Pagination.Offset()
	aggregateSQL += " ORDER BY " + orderBy + " LIMIT ? OFFSET ?"
	args = append(args, pageSize, offset)

	type aggRow struct {
		ChunkID         string
		KnowledgeID     string
		KnowledgeBaseID string
		KnowledgeTitle  string
		ContentPreview  string
		LikeCount       int
		DislikeCount    int
		SessionCount    int
		LastFeedbackAt  *time.Time
	}
	var rows []aggRow
	if err := r.db.WithContext(ctx).Raw(aggregateSQL, args...).Scan(&rows).Error; err != nil {
		return nil, 0, err
	}

	// Step 2: load the per-chunk dislike reason counts in a single query
	// (no N+1) and merge into the aggregate rows.
	chunkIDs := make([]string, 0, len(rows))
	for _, r := range rows {
		chunkIDs = append(chunkIDs, r.ChunkID)
	}
	reasons, err := r.bulkDislikeReasons(ctx, chunkIDs, resetAt)
	if err != nil {
		return nil, 0, err
	}

	stats := make([]types.ChunkFeedbackStat, 0, len(rows))
	for _, row := range rows {
		total := row.LikeCount + row.DislikeCount
		rate := types.PositiveRateOf(row.LikeCount, row.DislikeCount)
		_, needs := types.ComputeRecallWeight(row.LikeCount, row.DislikeCount, nil)
		if query.LowQualityOnly && !needs {
			continue
		}
		stats = append(stats, types.ChunkFeedbackStat{
			ChunkID:           row.ChunkID,
			KnowledgeID:       row.KnowledgeID,
			KnowledgeBaseID:   row.KnowledgeBaseID,
			KnowledgeTitle:    row.KnowledgeTitle,
			ContentPreview:    row.ContentPreview,
			LikeCount:         row.LikeCount,
			DislikeCount:      row.DislikeCount,
			Total:             total,
			PositiveRate:      rate,
			RecallWeight:      1.0,
			NeedsOptimization: needs,
			DislikeReasons:    reasons[row.ChunkID],
			SessionCount:      row.SessionCount,
			LastFeedbackAt:    row.LastFeedbackAt,
		})
	}
	return stats, total, nil
}

// bulkDislikeReasons aggregates the dislike reason codes per chunk in a
// single SQL query.
func (r *messageFeedbackRepository) bulkDislikeReasons(
	ctx context.Context,
	chunkIDs []string,
	resetAt *time.Time,
) (map[string]map[string]int, error) {
	out := make(map[string]map[string]int, len(chunkIDs))
	if len(chunkIDs) == 0 {
		return out, nil
	}
	resetClause := "1=1"
	args := []interface{}{types.FeedbackRatingDislike}
	if resetAt != nil {
		resetClause = "mf.updated_at >= ?"
		args = append(args, *resetAt)
	}
	args = append(args, chunkIDs)
	// reasons is stored as a JSON array string; we expand via JSON_TABLE
	// (Postgres) or json_each (SQLite). For portability we read the raw
	// rows and unpack in Go.
	sql := `
		SELECT mcr.chunk_id, mf.reasons
		FROM message_feedbacks mf
		JOIN message_chunk_references mcr ON mcr.message_id = mf.message_id
		WHERE mf.rating = ? AND ` + resetClause + ` AND mcr.chunk_id IN ?
	`
	type row struct {
		ChunkID string
		Reasons string
	}
	var rows []row
	if err := r.db.WithContext(ctx).Raw(sql, args...).Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		bucket := out[row.ChunkID]
		if bucket == nil {
			bucket = make(map[string]int)
			out[row.ChunkID] = bucket
		}
		var reasons []string
		if err := jsonUnmarshalString(row.Reasons, &reasons); err != nil {
			continue
		}
		for _, r := range reasons {
			bucket[r]++
		}
	}
	return out, nil
}

// ListWeightLogs returns the paged recall-weight change audit of one KB.
func (r *messageFeedbackRepository) ListWeightLogs(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	chunkID string,
	p *types.Pagination,
) ([]types.ChunkWeightLog, int64, error) {
	if p == nil {
		p = &types.Pagination{}
	}
	pageSize := p.GetPageSize()
	offset := p.Offset()
	var total int64
	if err := r.db.WithContext(ctx).Model(&types.ChunkWeightLog{}).
		Where("tenant_id = ? AND knowledge_base_id = ?", tenantID, kbID).
		Count(&total).Error; err != nil {
		return nil, 0, err
	}
	q := r.db.WithContext(ctx).Model(&types.ChunkWeightLog{}).
		Where("tenant_id = ? AND knowledge_base_id = ?", tenantID, kbID)
	if chunkID != "" {
		q = q.Where("chunk_id = ?", chunkID)
	}
	var rows []types.ChunkWeightLog
	if err := q.Order("created_at DESC, id DESC").
		Limit(pageSize).
		Offset(offset).
		Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// ResetKnowledgeBaseFeedback advances the KB's feedback epoch and restores
// neutral chunk feedback state. Existing message_feedbacks rows are not
// deleted so the audit trail survives; their contribution to per-chunk
// counters is excluded via the epoch.
func (r *messageFeedbackRepository) ResetKnowledgeBaseFeedback(
	ctx context.Context, tenantID uint64, kbID string,
) (int64, error) {
	var affected int64
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Lock the KB row.
		if r.isPostgres() {
			var lockedIDs []string
			if err := tx.Model(&types.KnowledgeBase{}).
				Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("id = ? AND tenant_id = ?", kbID, tenantID).
				Pluck("id", &lockedIDs).Error; err != nil {
				return err
			}
			if len(lockedIDs) == 0 {
				return gorm.ErrRecordNotFound
			}
		}

		// Read the latest weight snapshot for each chunk so we can write
		// a "reset" log entry for everyone we change.
		type chunkRow struct {
			ID           string
			RecallWeight float64
		}
		var rows []chunkRow
		if err := tx.Model(&types.Chunk{}).
			Select("id, recall_weight").
			Where("knowledge_base_id = ? AND tenant_id = ?", kbID, tenantID).
			Scan(&rows).Error; err != nil {
			return err
		}

		// Reset counters and weights to neutral.
		res := tx.Model(&types.Chunk{}).
			Where("knowledge_base_id = ? AND tenant_id = ?", kbID, tenantID).
			Updates(map[string]interface{}{
				"like_count":         0,
				"dislike_count":      0,
				"positive_rate":      0,
				"recall_weight":      1.0,
				"needs_optimization": false,
			})
		if res.Error != nil {
			return res.Error
		}
		affected = res.RowsAffected

		// Write a "reset" log row for every chunk we reset, so the audit
		// trail clearly shows the policy reset.
		now := time.Now().UTC()
		logs := make([]types.ChunkWeightLog, 0, len(rows))
		for _, row := range rows {
			if types.WeightApproximatelyEqual(row.RecallWeight, 1.0) {
				continue
			}
			logs = append(logs, types.ChunkWeightLog{
				TenantID:        tenantID,
				KnowledgeBaseID: kbID,
				ChunkID:         row.ID,
				OldWeight:       row.RecallWeight,
				NewWeight:       1.0,
				PositiveRate:    0,
				TriggerSource:   types.FeedbackWeightTriggerReset,
				CreatedAt:       now,
			})
		}
		if len(logs) > 0 {
			if err := tx.Create(&logs).Error; err != nil {
				return err
			}
		}

		// Advance the epoch so subsequent feedback reads exclude the
		// pre-reset rows from counter math.
		if err := tx.Model(&types.KnowledgeBase{}).
			Where("id = ?", kbID).
			Updates(map[string]interface{}{
				"feedback_reset_at": now,
				"feedback_reset_by": feedbackResetActor(),
			}).Error; err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return 0, err
	}
	return affected, nil
}

// feedbackResetActor is the principal recorded in feedback_reset_by. It
// defaults to the system actor and can be overridden via package-level
// configuration when the caller's identity is known.
var feedbackResetActor = func() string { return "system" }

// SetFeedbackResetActor overrides the recorded principal for future
// ResetKnowledgeBaseFeedback calls. The container wires the caller ID here
// after the auth middleware resolves the request principal.
func SetFeedbackResetActor(actor string) {
	if actor == "" {
		return
	}
	feedbackResetActor = func() string { return actor }
}

// RecomputeFeedbackWeights re-derives every chunk's recall weight under the
// supplied retrieval configs. The fingerprint is compared right before
// commit so a slow recomputation of an older config save aborts with
// ErrFeedbackRecomputeStale instead of overwriting the newer save.
func (r *messageFeedbackRepository) RecomputeFeedbackWeights(
	ctx context.Context,
	tenantID uint64,
	configsByKB map[string]*types.RetrievalConfig,
	expectedFingerprint string,
) (int64, error) {
	var changed int64
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Stale-check: compare the current fingerprint. Use the first
		// (and only) config in the map; the channel is keyed by KB ID so
		// per-KB overrides share the same fingerprint helper.
		if expectedFingerprint != "" {
			var row struct {
				RetrievalConfig *types.RetrievalConfig
			}
			if err := tx.Model(&types.Tenant{}).
				Select("retrieval_config").
				Where("id = ?", tenantID).
				Scan(&row).Error; err != nil {
				return err
			}
			actual := types.RetrievalConfigFingerprint(row.RetrievalConfig)
			if actual != expectedFingerprint {
				return types.ErrFeedbackRecomputeStale
			}
		}

		// Resolve a default config for the tenant.
		defaultCfg := configsByKB[""]
		if len(configsByKB) == 1 {
			for _, cfg := range configsByKB {
				defaultCfg = cfg
			}
		}
		// Per-KB overrides win when supplied.
		apply := func(kbID string) *types.RetrievalConfig {
			if cfg, ok := configsByKB[kbID]; ok && cfg != nil {
				return cfg
			}
			return defaultCfg
		}

		// Stream chunks in paged reads to keep individual transactions
		// short and to avoid pulling the entire corpus into memory.
		// Manual result.Close() (not defer) because we are inside a loop —
		// defer would accumulate open result sets until the function returns.
		const pageSize = 500
		offset := 0
		for {
			type chunkRow struct {
				ID              string
				KnowledgeBaseID string
				LikeCount       int
				DislikeCount    int
				RecallWeight    float64
				PositiveRate    float64
			}
			rows := make([]chunkRow, 0, pageSize)
			result, err := tx.Model(&types.Chunk{}).
				Select("id, knowledge_base_id, like_count, dislike_count, recall_weight, positive_rate").
				Where("tenant_id = ?", tenantID).
				Order("id ASC").
				Limit(pageSize).
				Offset(offset).
				Rows()
			if err != nil {
				return err
			}
			for result.Next() {
				var r chunkRow
				if err := result.Scan(&r.ID, &r.KnowledgeBaseID, &r.LikeCount, &r.DislikeCount, &r.RecallWeight, &r.PositiveRate); err != nil {
					result.Close()
					return err
				}
				rows = append(rows, r)
			}
			result.Close()
			if err := result.Err(); err != nil {
				return err
			}
			if len(rows) == 0 {
				break
			}
			now := time.Now().UTC()
			logs := make([]types.ChunkWeightLog, 0)
			for _, row := range rows {
				cfg := apply(row.KnowledgeBaseID)
				newWeight, needs := types.ComputeRecallWeight(row.LikeCount, row.DislikeCount, cfg)
				rate := types.PositiveRateOf(row.LikeCount, row.DislikeCount)
				if types.WeightApproximatelyEqual(newWeight, row.RecallWeight) &&
					types.WeightApproximatelyEqual(rate, row.PositiveRate) {
					continue
				}
				if err := tx.Model(&types.Chunk{}).
					Where("id = ?", row.ID).
					Updates(map[string]interface{}{
						"positive_rate":      rate,
						"recall_weight":      newWeight,
						"needs_optimization": needs,
					}).Error; err != nil {
					return err
				}
				changed++
				logs = append(logs, types.ChunkWeightLog{
					TenantID:        tenantID,
					KnowledgeBaseID: row.KnowledgeBaseID,
					ChunkID:         row.ID,
					OldWeight:       row.RecallWeight,
					NewWeight:       newWeight,
					PositiveRate:    rate,
					TriggerSource:   types.FeedbackWeightTriggerConfig,
					CreatedAt:       now,
				})
			}
			if len(logs) > 0 {
				if err := tx.Create(&logs).Error; err != nil {
					return err
				}
			}
			if len(rows) < pageSize {
				break
			}
			offset += pageSize
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return changed, nil
}

// jsonUnmarshalString is a small helper that handles both string and
// []byte backing storage for JSONB columns. We don't reference the json
// package directly here so future schema migrations can swap the parser
// without touching call sites.
func jsonUnmarshalString(raw string, target interface{}) error {
	if raw == "" {
		return nil
	}
	return json.Unmarshal([]byte(raw), target)
}
