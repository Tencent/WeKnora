package repository

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	defaultStatsPageSize = 20
	maxStatsPageSize     = 100
)

// chunkFeedbackRepository implements interfaces.ChunkFeedbackRepository.
type chunkFeedbackRepository struct {
	db *gorm.DB
}

// NewChunkFeedbackRepository creates a new chunk feedback repository.
func NewChunkFeedbackRepository(db *gorm.DB) interfaces.ChunkFeedbackRepository {
	return &chunkFeedbackRepository{db: db}
}

// RecordMessageChunkLinks upserts message->chunk links idempotently.
func (r *chunkFeedbackRepository) RecordMessageChunkLinks(ctx context.Context, links []*types.MessageChunkLink) error {
	if len(links) == 0 {
		return nil
	}
	// Deduplicate by message_id + chunk_id before upserting.
	seen := make(map[string]struct{}, len(links))
	out := make([]*types.MessageChunkLink, 0, len(links))
	for _, l := range links {
		if l == nil || l.MessageID == "" || l.ChunkID == "" {
			continue
		}
		key := l.MessageID + "|" + l.ChunkID
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, l)
	}
	if len(out) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "message_id"},
			{Name: "chunk_id"},
		},
		DoUpdates: clause.AssignmentColumns([]string{
			"chunk_seq_id", "knowledge_id", "knowledge_base_id",
			"knowledge_title", "chunk_content", "tenant_id", "session_id", "deleted_at",
		}),
	}).Create(&out).Error
}

// ListChunkLinksByMessageID returns active chunk links for a message.
func (r *chunkFeedbackRepository) ListChunkLinksByMessageID(ctx context.Context, messageID string) ([]*types.MessageChunkLink, error) {
	var links []*types.MessageChunkLink
	if err := r.db.WithContext(ctx).Where("message_id = ?", messageID).Find(&links).Error; err != nil {
		return nil, err
	}
	return links, nil
}

// ListChunkLinksByMessageIDs returns active chunk links for several messages.
func (r *chunkFeedbackRepository) ListChunkLinksByMessageIDs(ctx context.Context, messageIDs []string) ([]*types.MessageChunkLink, error) {
	if len(messageIDs) == 0 {
		return nil, nil
	}
	var links []*types.MessageChunkLink
	if err := r.db.WithContext(ctx).Where("message_id IN ?", messageIDs).Find(&links).Error; err != nil {
		return nil, err
	}
	return links, nil
}

// GetFeedbackRecord returns the active rating record of a user for a message.
func (r *chunkFeedbackRepository) GetFeedbackRecord(ctx context.Context, userID, messageID string) (*types.ChunkFeedbackRecord, error) {
	var record types.ChunkFeedbackRecord
	if err := r.db.WithContext(ctx).
		Where("user_id = ? AND message_id = ?", userID, messageID).
		First(&record).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &record, nil
}

// UpsertFeedbackRecord creates or updates (restoring when soft-deleted) a
// user's rating record for a message.
func (r *chunkFeedbackRepository) UpsertFeedbackRecord(ctx context.Context, record *types.ChunkFeedbackRecord) error {
	if record == nil || record.UserID == "" || record.MessageID == "" {
		return errors.New("feedback record requires user_id and message_id")
	}
	now := time.Now()

	var existing types.ChunkFeedbackRecord
	err := r.db.WithContext(ctx).Unscoped().
		Where("user_id = ? AND message_id = ?", record.UserID, record.MessageID).
		First(&existing).Error
	switch {
	case err == nil:
		// Restore soft-deleted rows or update the active one.
		updates := map[string]interface{}{
			"rating":     record.Rating,
			"reason":     record.Reason,
			"session_id": record.SessionID,
			"tenant_id":  record.TenantID,
			"updated_at": now,
			"deleted_at": nil,
		}
		return r.db.WithContext(ctx).Unscoped().Model(&types.ChunkFeedbackRecord{}).
			Where("id = ?", existing.ID).Updates(updates).Error
	case errors.Is(err, gorm.ErrRecordNotFound):
		record.CreatedAt = now
		record.UpdatedAt = now
		return r.db.WithContext(ctx).Create(record).Error
	default:
		return err
	}
}

// DeleteFeedbackRecord soft-deletes a user's rating record for a message.
func (r *chunkFeedbackRepository) DeleteFeedbackRecord(ctx context.Context, userID, messageID string) error {
	return r.db.WithContext(ctx).
		Where("user_id = ? AND message_id = ?", userID, messageID).
		Delete(&types.ChunkFeedbackRecord{}).Error
}

// GetFeedbackRatingsByMessages returns active ratings of a user keyed by message id.
func (r *chunkFeedbackRepository) GetFeedbackRatingsByMessages(ctx context.Context, userID string, messageIDs []string) (map[string]string, error) {
	out := make(map[string]string, len(messageIDs))
	if len(messageIDs) == 0 {
		return out, nil
	}
	var records []*types.ChunkFeedbackRecord
	if err := r.db.WithContext(ctx).
		Where("user_id = ? AND message_id IN ?", userID, messageIDs).
		Find(&records).Error; err != nil {
		return nil, err
	}
	for _, rec := range records {
		if rec != nil {
			out[rec.MessageID] = rec.Rating
		}
	}
	return out, nil
}

// CountFeedbackByChunk recomputes like/dislike/total counts and the last
// feedback time for a chunk from its linked records.
func (r *chunkFeedbackRepository) CountFeedbackByChunk(ctx context.Context, tenantID uint64, chunkID string) (like, dislike, total int64, lastAt *time.Time, err error) {
	var row struct {
		LikeCount    int64
		DislikeCount int64
		TotalCount   int64
		LastAt       *time.Time
	}
	err = r.db.WithContext(ctx).Raw(`
		SELECT
			COUNT(DISTINCT CASE WHEN fr.rating = 'like' THEN fr.id END) AS like_count,
			COUNT(DISTINCT CASE WHEN fr.rating = 'dislike' THEN fr.id END) AS dislike_count,
			COUNT(DISTINCT fr.id) AS total_count,
			MAX(fr.updated_at) AS last_at
		FROM message_chunk_links ml
		JOIN chunk_feedback_records fr
			ON fr.message_id = ml.message_id AND fr.deleted_at IS NULL
		WHERE ml.chunk_id = ? AND ml.tenant_id = ? AND ml.deleted_at IS NULL
	`, chunkID, tenantID).Scan(&row).Error
	if err != nil {
		return 0, 0, 0, nil, err
	}
	return row.LikeCount, row.DislikeCount, row.TotalCount, row.LastAt, nil
}

// UpdateChunkFeedbackCounters persists the recomputed like/dislike counts and
// approval rate for a chunk.
func (r *chunkFeedbackRepository) UpdateChunkFeedbackCounters(ctx context.Context, tenantID uint64, chunkID string, like, dislike int64) error {
	rate := 0.0
	if like+dislike > 0 {
		rate = float64(like) / float64(like+dislike)
	}
	return r.db.WithContext(ctx).Model(&types.Chunk{}).
		Where("id = ? AND tenant_id = ?", chunkID, tenantID).
		Updates(map[string]interface{}{
			"like_count":    like,
			"dislike_count": dislike,
			"approval_rate": rate,
			"updated_at":    time.Now(),
		}).Error
}

// UpdateChunkRecallWeight persists a chunk's recall weight and the
// needs-optimization flag.
func (r *chunkFeedbackRepository) UpdateChunkRecallWeight(ctx context.Context, tenantID uint64, chunkID string, weight float64, needsOptimization bool) error {
	return r.db.WithContext(ctx).Model(&types.Chunk{}).
		Where("id = ? AND tenant_id = ?", chunkID, tenantID).
		Updates(map[string]interface{}{
			"recall_weight":      weight,
			"needs_optimization": needsOptimization,
			"updated_at":         time.Now(),
		}).Error
}

// GetChunkFeedbackStats returns paged per-chunk feedback stats.
func (r *chunkFeedbackRepository) GetChunkFeedbackStats(ctx context.Context, params *interfaces.ChunkFeedbackStatsParams) ([]*types.ChunkFeedbackStat, int64, error) {
	if params == nil {
		params = &interfaces.ChunkFeedbackStatsParams{}
	}
	page, pageSize := normalizePage(params.Page, params.PageSize, defaultStatsPageSize, maxStatsPageSize)

	q := r.db.WithContext(ctx).Model(&types.Chunk{}).Where("deleted_at IS NULL")
	if params.TenantID > 0 {
		q = q.Where("tenant_id = ?", params.TenantID)
	}
	if params.KnowledgeBaseID != "" {
		q = q.Where("knowledge_base_id = ?", params.KnowledgeBaseID)
	}
	if params.KnowledgeID != "" {
		q = q.Where("knowledge_id = ?", params.KnowledgeID)
	}
	if params.MinApprovalRate != nil {
		q = q.Where("approval_rate >= ?", *params.MinApprovalRate)
	}
	if params.MaxApprovalRate != nil {
		q = q.Where("approval_rate <= ?", *params.MaxApprovalRate)
	}
	if params.NeedsOptimization != nil {
		q = q.Where("needs_optimization = ?", *params.NeedsOptimization)
	}
	if kw := strings.TrimSpace(params.Keyword); kw != "" {
		like := "%" + kw + "%"
		q = q.Where("(LOWER(content) LIKE LOWER(?) OR knowledge_id = ?)", like, kw)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return []*types.ChunkFeedbackStat{}, 0, nil
	}

	var chunks []*types.Chunk
	sortCol := normalizeStatsSort(params.SortBy)
	order := "DESC"
	if strings.EqualFold(params.SortOrder, "asc") {
		order = "ASC"
	}
	if err := q.Order(sortCol + " " + order).Order("id ASC").
		Offset((page - 1) * pageSize).Limit(pageSize).Find(&chunks).Error; err != nil {
		return nil, 0, err
	}

	chunkIDs := make([]string, 0, len(chunks))
	for _, c := range chunks {
		if c != nil {
			chunkIDs = append(chunkIDs, c.ID)
		}
	}

	// Session + link counts per chunk.
	type sessionAgg struct {
		ChunkID      string
		SessionCount int64
		LinkCount    int64
	}
	var sessionAggs []sessionAgg
	if err := r.db.WithContext(ctx).Raw(`
		SELECT chunk_id,
			COUNT(DISTINCT session_id) AS session_count,
			COUNT(*) AS link_count
		FROM message_chunk_links
		WHERE chunk_id IN ? AND deleted_at IS NULL
		GROUP BY chunk_id`, chunkIDs).Scan(&sessionAggs).Error; err != nil {
		return nil, 0, err
	}

	// Feedback record counts per chunk.
	type feedbackAgg struct {
		ChunkID       string
		FeedbackCount int64
	}
	var feedbackAggs []feedbackAgg
	if err := r.db.WithContext(ctx).Raw(`
		SELECT ml.chunk_id, COUNT(DISTINCT fr.id) AS feedback_count
		FROM message_chunk_links ml
		JOIN chunk_feedback_records fr
			ON fr.message_id = ml.message_id AND fr.deleted_at IS NULL
		WHERE ml.chunk_id IN ? AND ml.deleted_at IS NULL
		GROUP BY ml.chunk_id`, chunkIDs).Scan(&feedbackAggs).Error; err != nil {
		return nil, 0, err
	}

	sessionMap := make(map[string]sessionAgg, len(sessionAggs))
	for _, a := range sessionAggs {
		sessionMap[a.ChunkID] = a
	}
	feedbackMap := make(map[string]int64, len(feedbackAggs))
	for _, a := range feedbackAggs {
		feedbackMap[a.ChunkID] = a.FeedbackCount
	}

	// Batch-load knowledge titles/file names.
	knowledgeIDs := make([]string, 0, len(chunks))
	for _, c := range chunks {
		if c != nil && c.KnowledgeID != "" {
			knowledgeIDs = append(knowledgeIDs, c.KnowledgeID)
		}
	}
	titleMap := make(map[string]struct {
		Title    string
		FileName string
	}, len(knowledgeIDs))
	if len(knowledgeIDs) > 0 {
		type knowledgeRow struct {
			ID       string
			Title    string
			FileName string
		}
		var rows []knowledgeRow
		if err := r.db.WithContext(ctx).Model(&types.Knowledge{}).
			Select("id", "title", "file_name").
			Where("id IN ?", knowledgeIDs).Scan(&rows).Error; err != nil {
			return nil, 0, err
		}
		for _, row := range rows {
			titleMap[row.ID] = struct {
				Title    string
				FileName string
			}{row.Title, row.FileName}
		}
	}

	stats := make([]*types.ChunkFeedbackStat, 0, len(chunks))
	for _, c := range chunks {
		if c == nil {
			continue
		}
		stat := &types.ChunkFeedbackStat{
			ChunkID:           c.ID,
			ChunkSeqID:        c.SeqID,
			KnowledgeID:       c.KnowledgeID,
			KnowledgeBaseID:   c.KnowledgeBaseID,
			KnowledgeTitle:    titleMap[c.KnowledgeID].Title,
			KnowledgeFileName: titleMap[c.KnowledgeID].FileName,
			ChunkIndex:        c.ChunkIndex,
			ContentPreview:    previewContent(c.Content),
			ChunkType:         string(c.ChunkType),
			LikeCount:         c.LikeCount,
			DislikeCount:      c.DislikeCount,
			ApprovalRate:      c.ApprovalRate,
			RecallWeight:      c.RecallWeight,
			NeedsOptimization: c.NeedsOptimization,
			FeedbackCount:     feedbackMap[c.ID],
			UpdatedAt:         c.UpdatedAt,
		}
		if agg, ok := sessionMap[c.ID]; ok {
			stat.SessionCount = agg.SessionCount
		}
		stats = append(stats, stat)
	}
	return stats, total, nil
}

// GetChunkFeedbackDetail returns per-chunk stats plus dislike reason
// aggregation and related chat sessions.
func (r *chunkFeedbackRepository) GetChunkFeedbackDetail(ctx context.Context, tenantID uint64, chunkID string) (*types.ChunkFeedbackDetail, error) {
	var chunk types.Chunk
	if err := r.db.WithContext(ctx).
		Where("id = ? AND tenant_id = ?", chunkID, tenantID).
		First(&chunk).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}

	detail := &types.ChunkFeedbackDetail{
		ChunkFeedbackStat: types.ChunkFeedbackStat{
			ChunkID:           chunk.ID,
			ChunkSeqID:        chunk.SeqID,
			KnowledgeID:       chunk.KnowledgeID,
			KnowledgeBaseID:   chunk.KnowledgeBaseID,
			ChunkIndex:        chunk.ChunkIndex,
			ContentPreview:    previewContent(chunk.Content),
			ChunkType:         string(chunk.ChunkType),
			LikeCount:         chunk.LikeCount,
			DislikeCount:      chunk.DislikeCount,
			ApprovalRate:      chunk.ApprovalRate,
			RecallWeight:      chunk.RecallWeight,
			NeedsOptimization: chunk.NeedsOptimization,
			UpdatedAt:         chunk.UpdatedAt,
		},
		DislikeReasons:  make([]types.DislikeReasonStat, 0),
		RelatedSessions: make([]types.RelatedSessionStat, 0),
	}

	if chunk.KnowledgeID != "" {
		var knowledge types.Knowledge
		if err := r.db.WithContext(ctx).
			Select("id", "title", "file_name").
			Where("id = ?", chunk.KnowledgeID).First(&knowledge).Error; err == nil {
			detail.KnowledgeTitle = knowledge.Title
			detail.KnowledgeFileName = knowledge.FileName
		}
	}

	// Session + feedback count.
	var agg struct {
		SessionCount  int64
		FeedbackCount int64
		LastAt        *time.Time
	}
	_ = r.db.WithContext(ctx).Raw(`
		SELECT
			COUNT(DISTINCT ml.session_id) AS session_count,
			COUNT(DISTINCT fr.id) AS feedback_count,
			MAX(fr.updated_at) AS last_at
		FROM message_chunk_links ml
		LEFT JOIN chunk_feedback_records fr
			ON fr.message_id = ml.message_id AND fr.deleted_at IS NULL
		WHERE ml.chunk_id = ? AND ml.tenant_id = ? AND ml.deleted_at IS NULL
	`, chunkID, tenantID).Scan(&agg).Error
	detail.SessionCount = agg.SessionCount
	detail.FeedbackCount = agg.FeedbackCount

	// Dislike reasons.
	var reasons []types.DislikeReasonStat
	if err := r.db.WithContext(ctx).Raw(`
		SELECT fr.reason AS reason, COUNT(*) AS count
		FROM chunk_feedback_records fr
		JOIN message_chunk_links ml ON ml.message_id = fr.message_id AND ml.deleted_at IS NULL
		WHERE ml.chunk_id = ? AND fr.deleted_at IS NULL AND fr.rating = 'dislike' AND fr.reason <> ''
		GROUP BY fr.reason
		ORDER BY count DESC
		LIMIT 50`, chunkID).Scan(&reasons).Error; err != nil {
		return nil, err
	}
	detail.DislikeReasons = reasons

	// Related sessions.
	var sessions []types.RelatedSessionStat
	if err := r.db.WithContext(ctx).Raw(`
		SELECT ml.session_id AS session_id,
			COALESCE(s.title, '') AS title,
			COUNT(DISTINCT ml.message_id) AS message_count,
			MAX(s.updated_at) AS last_active_at
		FROM message_chunk_links ml
		LEFT JOIN sessions s ON s.id = ml.session_id AND s.deleted_at IS NULL
		WHERE ml.chunk_id = ? AND ml.tenant_id = ? AND ml.deleted_at IS NULL
		GROUP BY ml.session_id, s.title
		ORDER BY last_active_at DESC
		LIMIT 50`, chunkID, tenantID).Scan(&sessions).Error; err != nil {
		return nil, err
	}
	detail.RelatedSessions = sessions

	return detail, nil
}

// CreateWeightLog appends a recall-weight change audit row.
func (r *chunkFeedbackRepository) CreateWeightLog(ctx context.Context, log *types.ChunkWeightLog) error {
	if log == nil {
		return nil
	}
	if log.CreatedAt.IsZero() {
		log.CreatedAt = time.Now()
	}
	return r.db.WithContext(ctx).Create(log).Error
}

// ListWeightLogs returns paged weight-change logs.
func (r *chunkFeedbackRepository) ListWeightLogs(ctx context.Context, tenantID uint64, chunkID, source string, page, pageSize int) ([]*types.ChunkWeightLog, int64, error) {
	page, pageSize = normalizePage(page, pageSize, defaultStatsPageSize, maxStatsPageSize)
	q := r.db.WithContext(ctx).Model(&types.ChunkWeightLog{}).Where("tenant_id = ?", tenantID)
	if chunkID != "" {
		q = q.Where("chunk_id = ?", chunkID)
	}
	if source != "" {
		q = q.Where("source = ?", source)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var logs []*types.ChunkWeightLog
	if err := q.Order("created_at DESC").Order("id ASC").
		Offset((page - 1) * pageSize).Limit(pageSize).Find(&logs).Error; err != nil {
		return nil, 0, err
	}
	if logs == nil {
		logs = make([]*types.ChunkWeightLog, 0)
	}
	return logs, total, nil
}

// ResetChunkFeedback zeroes counters/approval rate/weight/flag for the given
// chunks and removes their linked feedback records.
func (r *chunkFeedbackRepository) ResetChunkFeedback(ctx context.Context, tenantID uint64, chunkIDs []string) error {
	if len(chunkIDs) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Soft-delete feedback records linked to these chunks.
		if err := tx.Exec(`
			UPDATE chunk_feedback_records
			SET deleted_at = ?, updated_at = ?
			WHERE deleted_at IS NULL AND message_id IN (
				SELECT message_id FROM message_chunk_links
				WHERE chunk_id IN ? AND deleted_at IS NULL
			)`, time.Now(), time.Now(), chunkIDs).Error; err != nil {
			return err
		}
		return tx.Model(&types.Chunk{}).
			Where("id IN ? AND tenant_id = ?", chunkIDs, tenantID).
			Updates(map[string]interface{}{
				"like_count":         0,
				"dislike_count":      0,
				"approval_rate":      0.0,
				"recall_weight":      1.0,
				"needs_optimization": false,
				"updated_at":         time.Now(),
			}).Error
	})
}

// UpdateChunkWeights directly rewrites recall weight for the given chunks
// (manual override and reset flows).
func (r *chunkFeedbackRepository) UpdateChunkWeights(ctx context.Context, tenantID uint64, chunkIDs []string, weight float64) error {
	if len(chunkIDs) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Model(&types.Chunk{}).
		Where("id IN ? AND tenant_id = ?", chunkIDs, tenantID).
		Updates(map[string]interface{}{
			"recall_weight": weight,
			"updated_at":    time.Now(),
		}).Error
}

// GetFeedbackConfig loads the tenant config, returning defaults when absent.
func (r *chunkFeedbackRepository) GetFeedbackConfig(ctx context.Context, tenantID uint64) (*types.ChunkFeedbackConfig, error) {
	var cfg types.ChunkFeedbackConfig
	if err := r.db.WithContext(ctx).Where("tenant_id = ?", tenantID).First(&cfg).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return types.DefaultChunkFeedbackConfig(tenantID), nil
		}
		return nil, err
	}
	return &cfg, nil
}

// UpdateFeedbackConfig upserts the tenant config.
func (r *chunkFeedbackRepository) UpdateFeedbackConfig(ctx context.Context, cfg *types.ChunkFeedbackConfig) error {
	if cfg == nil {
		return nil
	}
	cfg.UpdatedAt = time.Now()
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "tenant_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"boost_threshold", "degrade_threshold", "optimize_threshold", "min_votes", "weight_step", "max_weight", "min_weight", "updated_at"}),
	}).Create(cfg).Error
}

// normalizePage clamps pagination parameters.
func normalizePage(page, pageSize, defPageSize, maxPageSize int) (int, int) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = defPageSize
	}
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}
	return page, pageSize
}

// normalizeStatsSort maps a sort key onto a whitelisted chunk column.
func normalizeStatsSort(sortBy string) string {
	switch strings.ToLower(sortBy) {
	case "like_count":
		return "like_count"
	case "dislike_count":
		return "dislike_count"
	case "approval_rate":
		return "approval_rate"
	case "recall_weight":
		return "recall_weight"
	case "created_at":
		return "created_at"
	default:
		return "updated_at"
	}
}

// previewContent truncates chunk content for list display.
func previewContent(content string) string {
	const maxRunes = 200
	runes := []rune(content)
	if len(runes) <= maxRunes {
		return content
	}
	return string(runes[:maxRunes]) + "..."
}

// ensure feedback repository implements the interface at compile time.
var _ interfaces.ChunkFeedbackRepository = (*chunkFeedbackRepository)(nil)
