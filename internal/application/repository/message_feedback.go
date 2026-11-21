package repository

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// MessageFeedbackRepository persists answer votes, chunk links, and weight logs.
type MessageFeedbackRepository struct {
	db *gorm.DB
}

// NewMessageFeedbackRepository creates the feedback repository.
func NewMessageFeedbackRepository(db *gorm.DB) *MessageFeedbackRepository {
	return &MessageFeedbackRepository{db: db}
}

// UpsertChunkRefs inserts citation links that are not already stored.
func (r *MessageFeedbackRepository) UpsertChunkRefs(ctx context.Context, refs []*types.MessageChunkRef) error {
	if len(refs) == 0 {
		return nil
	}
	now := time.Now()
	for _, ref := range refs {
		if ref.ID == "" {
			ref.ID = uuid.NewString()
		}
		if ref.CreatedAt.IsZero() {
			ref.CreatedAt = now
		}
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "session_tenant_id"},
			{Name: "message_id"},
			{Name: "chunk_id"},
		},
		DoNothing: true,
	}).Create(&refs).Error
}

// ListChunkRefsByMessage returns the chunks cited by one assistant message.
func (r *MessageFeedbackRepository) ListChunkRefsByMessage(
	ctx context.Context, sessionTenantID uint64, messageID string,
) ([]*types.MessageChunkRef, error) {
	var refs []*types.MessageChunkRef
	err := r.db.WithContext(ctx).
		Where("session_tenant_id = ? AND message_id = ?", sessionTenantID, messageID).
		Find(&refs).Error
	return refs, err
}

// ListFeedbackForMessages returns the caller's current votes for a page of messages.
func (r *MessageFeedbackRepository) ListFeedbackForMessages(
	ctx context.Context, sessionTenantID uint64, userID string, messageIDs []string,
) ([]*types.MessageFeedback, error) {
	if userID == "" || len(messageIDs) == 0 {
		return nil, nil
	}
	var rows []*types.MessageFeedback
	err := r.db.WithContext(ctx).
		Where("session_tenant_id = ? AND user_id = ? AND message_id IN ?", sessionTenantID, userID, messageIDs).
		Find(&rows).Error
	return rows, err
}

// GetFeedback returns one user's vote on one message.
func (r *MessageFeedbackRepository) GetFeedback(
	ctx context.Context, sessionTenantID uint64, userID, messageID string,
) (*types.MessageFeedback, error) {
	var rows []types.MessageFeedback
	err := r.db.WithContext(ctx).
		Where("session_tenant_id = ? AND user_id = ? AND message_id = ?", sessionTenantID, userID, messageID).
		Limit(1).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0], nil
}

// ApplyVote stores the vote and recomputes every chunk the message cites.
func (r *MessageFeedbackRepository) ApplyVote(
	ctx context.Context,
	row *types.MessageFeedback,
	policy types.FeedbackPolicy,
	trigger string,
) (*types.MessageFeedback, error) {
	if row == nil {
		return nil, fmt.Errorf("feedback is nil")
	}
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := saveFeedback(tx, row); err != nil {
			return err
		}
		var refs []*types.MessageChunkRef
		if err := tx.Where("session_tenant_id = ? AND message_id = ?", row.SessionTenantID, row.MessageID).
			Find(&refs).Error; err != nil {
			return err
		}
		seen := map[string]struct{}{}
		for _, ref := range refs {
			if ref == nil || ref.ChunkID == "" {
				continue
			}
			if _, ok := seen[ref.ChunkID]; ok {
				continue
			}
			seen[ref.ChunkID] = struct{}{}
			if err := refreshChunkQuality(
				tx, ref.ChunkTenantID, ref.ChunkID, policy, types.ChunkWeightLogSourceFeedback,
				row.MessageID, row.ID, trigger,
			); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return row, nil
}

// RecomputeMessageChunks refreshes every chunk cited by a message. Used after
// the citation links are written so a vote that arrived first is attributed.
func (r *MessageFeedbackRepository) RecomputeMessageChunks(
	ctx context.Context, sessionTenantID uint64, messageID string, policy types.FeedbackPolicy,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var refs []*types.MessageChunkRef
		if err := tx.Where("session_tenant_id = ? AND message_id = ?", sessionTenantID, messageID).
			Find(&refs).Error; err != nil {
			return err
		}
		seen := map[string]struct{}{}
		for _, ref := range refs {
			if ref == nil || ref.ChunkID == "" {
				continue
			}
			if _, ok := seen[ref.ChunkID]; ok {
				continue
			}
			seen[ref.ChunkID] = struct{}{}
			if err := refreshChunkQuality(tx, ref.ChunkTenantID, ref.ChunkID, policy,
				types.ChunkWeightLogSourceFeedback, messageID, "", "answer_chunks_recorded"); err != nil {
				return err
			}
		}
		return nil
	})
}

// ResetChunk zeroes vote tallies, restores a neutral weight, and logs the reset.
func (r *MessageFeedbackRepository) ResetChunk(
	ctx context.Context, tenantID uint64, chunkID string, policy types.FeedbackPolicy,
) (*types.Chunk, error) {
	policy = policy.Normalize()
	var updated types.Chunk
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		chunk, err := lockChunk(tx, tenantID, chunkID)
		if err != nil {
			return err
		}
		now := time.Now()
		oldWeight := types.EffectiveRecallWeight(chunk.RecallWeight)
		fields := map[string]interface{}{
			"like_count":         int64(0),
			"dislike_count":      int64(0),
			"positive_rate":      nil,
			"recall_weight":      policy.NeutralRecallWeight,
			"needs_optimization": false,
			"feedback_reset_at":  now,
			"updated_at":         now,
		}
		if err := tx.Model(&types.Chunk{}).
			Where("id = ? AND tenant_id = ?", chunkID, tenantID).
			Updates(fields).Error; err != nil {
			return err
		}
		logRow := &types.ChunkWeightLog{
			ID:            uuid.NewString(),
			ChunkTenantID: tenantID,
			ChunkID:       chunkID,
			OldWeight:     oldWeight,
			NewWeight:     policy.NeutralRecallWeight,
			Source:        types.ChunkWeightLogSourceAdminReset,
			Reason:        "admin reset feedback counts and recall weight",
			CreatedAt:     now,
		}
		if err := tx.Create(logRow).Error; err != nil {
			return err
		}
		if err := tx.Where("id = ? AND tenant_id = ?", chunkID, tenantID).First(&updated).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &updated, nil
}

// ChunkStats loads tallies, citing sessions, and dislike reasons for one chunk.
func (r *MessageFeedbackRepository) ChunkStats(
	ctx context.Context, tenantID uint64, chunkID string,
) (*types.ChunkFeedbackStats, error) {
	var chunk types.Chunk
	err := r.db.WithContext(ctx).Where("id = ? AND tenant_id = ?", chunkID, tenantID).First(&chunk).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrChunkNotFound
	}
	if err != nil {
		return nil, err
	}
	stats := &types.ChunkFeedbackStats{
		ChunkID:           chunk.ID,
		LikeCount:         chunk.LikeCount,
		DislikeCount:      chunk.DislikeCount,
		PositiveRate:      chunk.PositiveRate,
		RecallWeight:      types.EffectiveRecallWeight(chunk.RecallWeight),
		NeedsOptimization: chunk.NeedsOptimization,
		FeedbackResetAt:   chunk.FeedbackResetAt,
		Sessions:          []types.FeedbackSessionRef{},
		ReasonStats:       []types.FeedbackReasonStat{},
		RecentReasons:     []string{},
	}
	var sessionCount int64
	if err := r.db.WithContext(ctx).Model(&types.MessageChunkRef{}).
		Where("chunk_tenant_id = ? AND chunk_id = ?", tenantID, chunkID).
		Distinct("session_id").
		Count(&sessionCount).Error; err != nil {
		return nil, err
	}
	stats.AssociatedSessionCount = sessionCount

	type sessionRow struct {
		SessionID   string `gorm:"column:session_id"`
		LastCitedAt string `gorm:"column:last_cited_at"`
	}
	var sessions []sessionRow
	if err := r.db.WithContext(ctx).Model(&types.MessageChunkRef{}).
		Select("session_id, MAX(created_at) AS last_cited_at").
		Where("chunk_tenant_id = ? AND chunk_id = ?", tenantID, chunkID).
		Group("session_id").
		Order("last_cited_at DESC").
		Limit(50).
		Scan(&sessions).Error; err != nil {
		return nil, err
	}
	for _, row := range sessions {
		stats.Sessions = append(stats.Sessions, types.FeedbackSessionRef{
			SessionID:   row.SessionID,
			LastCitedAt: parseDBTime(row.LastCitedAt),
		})
	}

	reasonQuery := r.db.WithContext(ctx).
		Table("message_feedbacks AS f").
		Select("f.reason_code AS reason_code, COUNT(*) AS count").
		Joins(feedbackChunkJoin).
		Where("r.chunk_tenant_id = ? AND r.chunk_id = ? AND f.feedback_type = ? AND f.reason_code <> ''",
			tenantID, chunkID, types.FeedbackTypeDislike)
	if chunk.FeedbackResetAt != nil {
		reasonQuery = reasonQuery.Where("f.feedback_at > ?", *chunk.FeedbackResetAt)
	}
	var reasons []types.FeedbackReasonStat
	if err := reasonQuery.Group("f.reason_code").Order("count DESC").Scan(&reasons).Error; err != nil {
		return nil, err
	}
	if reasons != nil {
		stats.ReasonStats = reasons
	}

	textQuery := r.db.WithContext(ctx).
		Table("message_feedbacks AS f").
		Select("f.reason_text").
		Joins(feedbackChunkJoin).
		Where("r.chunk_tenant_id = ? AND r.chunk_id = ? AND f.feedback_type = ? AND f.reason_text <> ''",
			tenantID, chunkID, types.FeedbackTypeDislike)
	if chunk.FeedbackResetAt != nil {
		textQuery = textQuery.Where("f.feedback_at > ?", *chunk.FeedbackResetAt)
	}
	var texts []string
	if err := textQuery.Order("f.feedback_at DESC").Limit(20).Pluck("f.reason_text", &texts).Error; err != nil {
		return nil, err
	}
	if texts != nil {
		stats.RecentReasons = texts
	}
	return stats, nil
}

// ListWeightLogs returns the newest weight-change rows for a chunk.
func (r *MessageFeedbackRepository) ListWeightLogs(
	ctx context.Context, tenantID uint64, chunkID string, limit int,
) ([]*types.ChunkWeightLog, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var rows []*types.ChunkWeightLog
	err := r.db.WithContext(ctx).
		Where("chunk_tenant_id = ? AND chunk_id = ?", tenantID, chunkID).
		Order("created_at DESC").
		Limit(limit).
		Find(&rows).Error
	return rows, err
}

// ListChunksByFeedback lists one document's chunks with optional quality filters.
func (r *MessageFeedbackRepository) ListChunksByFeedback(
	ctx context.Context,
	tenantID uint64,
	knowledgeID string,
	page *types.Pagination,
	chunkTypes []types.ChunkType,
	filter types.ChunkFeedbackFilter,
) ([]*types.Chunk, int64, error) {
	if page == nil {
		page = &types.Pagination{Page: 1, PageSize: 10}
	}
	base := func(db *gorm.DB) *gorm.DB {
		db = db.Where(
			"tenant_id = ? AND knowledge_id = ? AND chunk_type IN (?) AND status IN (?)",
			tenantID, knowledgeID, chunkTypes,
			[]int{int(types.ChunkStatusIndexed), int(types.ChunkStatusDefault)},
		)
		if filter.MaxPositiveRate != nil {
			db = db.Where("positive_rate IS NOT NULL AND positive_rate < ?", *filter.MaxPositiveRate)
		}
		if filter.NeedsOptimization != nil {
			db = db.Where("needs_optimization = ?", *filter.NeedsOptimization)
		}
		return db
	}
	var total int64
	if err := base(r.db.WithContext(ctx).Model(&types.Chunk{})).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var chunks []*types.Chunk
	err := base(r.db.WithContext(ctx)).
		Order("chunk_index ASC").
		Offset(page.Offset()).
		Limit(page.Limit()).
		Find(&chunks).Error
	return chunks, total, err
}

const feedbackChunkJoin = "JOIN message_chunk_refs AS r " +
	"ON r.message_id = f.message_id AND r.session_tenant_id = f.session_tenant_id"

func saveFeedback(tx *gorm.DB, row *types.MessageFeedback) error {
	var existingRows []types.MessageFeedback
	err := tx.Where(
		"session_tenant_id = ? AND user_id = ? AND message_id = ?",
		row.SessionTenantID, row.UserID, row.MessageID,
	).Limit(1).Find(&existingRows).Error
	now := time.Now()
	if err != nil {
		return err
	}
	if len(existingRows) == 0 {
		if row.ID == "" {
			row.ID = uuid.NewString()
		}
		row.CreatedAt = now
		row.UpdatedAt = now
		row.FeedbackAt = now
		return tx.Create(row).Error
	}
	existing := existingRows[0]
	existing.SessionID = row.SessionID
	existing.FeedbackType = row.FeedbackType
	existing.ReasonCode = row.ReasonCode
	existing.ReasonText = row.ReasonText
	existing.FeedbackAt = now
	existing.UpdatedAt = now
	if err := tx.Save(&existing).Error; err != nil {
		return err
	}
	*row = existing
	return nil
}

func lockChunk(tx *gorm.DB, tenantID uint64, chunkID string) (*types.Chunk, error) {
	// Touch the row first so concurrent recomputes serialize on it. SQLite
	// has no FOR UPDATE; the write lock taken here covers that driver too.
	now := time.Now()
	result := tx.Model(&types.Chunk{}).
		Where("id = ? AND tenant_id = ?", chunkID, tenantID).
		Update("updated_at", now)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, ErrChunkNotFound
	}
	var chunk types.Chunk
	if err := tx.Where("id = ? AND tenant_id = ?", chunkID, tenantID).First(&chunk).Error; err != nil {
		return nil, err
	}
	return &chunk, nil
}

func refreshChunkQuality(
	tx *gorm.DB,
	tenantID uint64,
	chunkID string,
	policy types.FeedbackPolicy,
	source, messageID, feedbackID, trigger string,
) error {
	chunk, err := lockChunk(tx, tenantID, chunkID)
	if errors.Is(err, ErrChunkNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	likes, dislikes, err := countChunkVotes(tx, tenantID, chunkID, chunk.FeedbackResetAt)
	if err != nil {
		return err
	}
	quality := policy.Evaluate(likes, dislikes)
	oldWeight := types.EffectiveRecallWeight(chunk.RecallWeight)
	unchanged := chunk.LikeCount == likes &&
		chunk.DislikeCount == dislikes &&
		chunk.NeedsOptimization == quality.NeedsOptimization &&
		ratesEqual(chunk.PositiveRate, quality.PositiveRate) &&
		math.Abs(oldWeight-quality.RecallWeight) < 1e-6 &&
		chunk.RecallWeight > 0
	if unchanged {
		return nil
	}
	fields := map[string]interface{}{
		"like_count":         likes,
		"dislike_count":      dislikes,
		"positive_rate":      quality.PositiveRate,
		"recall_weight":      quality.RecallWeight,
		"needs_optimization": quality.NeedsOptimization,
		"updated_at":         time.Now(),
	}
	if err := tx.Model(&types.Chunk{}).
		Where("id = ? AND tenant_id = ?", chunkID, tenantID).
		Updates(fields).Error; err != nil {
		return err
	}
	if math.Abs(oldWeight-quality.RecallWeight) < 1e-6 {
		return nil
	}
	return tx.Create(&types.ChunkWeightLog{
		ID:               uuid.NewString(),
		ChunkTenantID:    tenantID,
		ChunkID:          chunkID,
		OldWeight:        oldWeight,
		NewWeight:        quality.RecallWeight,
		Source:           source,
		SourceMessageID:  messageID,
		SourceFeedbackID: feedbackID,
		Reason: fmt.Sprintf(
			"%s: likes=%d dislikes=%d weight %.4f -> %.4f",
			trigger, likes, dislikes, oldWeight, quality.RecallWeight,
		),
		CreatedAt: time.Now(),
	}).Error
}

func countChunkVotes(tx *gorm.DB, tenantID uint64, chunkID string, resetAt *time.Time) (int64, int64, error) {
	type tally struct {
		Likes    int64 `gorm:"column:likes"`
		Dislikes int64 `gorm:"column:dislikes"`
	}
	query := tx.Table("message_feedbacks AS f").
		Select(`COALESCE(SUM(CASE WHEN f.feedback_type = 'like' THEN 1 ELSE 0 END), 0) AS likes,
			COALESCE(SUM(CASE WHEN f.feedback_type = 'dislike' THEN 1 ELSE 0 END), 0) AS dislikes`).
		Joins(feedbackChunkJoin).
		Where("r.chunk_tenant_id = ? AND r.chunk_id = ?", tenantID, chunkID).
		Where("f.feedback_type IN ?", []string{types.FeedbackTypeLike, types.FeedbackTypeDislike})
	if resetAt != nil {
		query = query.Where("f.feedback_at > ?", *resetAt)
	}
	var row tally
	if err := query.Scan(&row).Error; err != nil {
		return 0, 0, err
	}
	return row.Likes, row.Dislikes, nil
}

func parseDBTime(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed
		}
	}
	return time.Time{}
}

func ratesEqual(left, right *float64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return math.Abs(*left-*right) < 1e-9
}
