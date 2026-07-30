package service

import (
	"context"
	"errors"
	"math"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrFeedbackInvalidVote        = errors.New("invalid feedback vote")
	ErrFeedbackNoChunkRefs        = errors.New("message has no chunk references")
	ErrFeedbackReasonTooLong      = errors.New("dislike reason too long")
	ErrFeedbackReasonRequired     = errors.New("dislike reason is required")
	ErrChunkFeedbackChunkNotFound = errors.New("chunk not found")
)

type chunkFeedbackService struct {
	db                     *gorm.DB
	systemSettingService   interfaces.SystemSettingService
}

func NewChunkFeedbackService(db *gorm.DB, systemSettingService interfaces.SystemSettingService) interfaces.ChunkFeedbackService {
	return &chunkFeedbackService{db: db, systemSettingService: systemSettingService}
}


func (s *chunkFeedbackService) getThresholdRate(ctx context.Context, key string, defaultVal float64) float64 {
	if s.systemSettingService == nil {
		return defaultVal
	}
	val := s.systemSettingService.GetInt(ctx, key, "", int64(defaultVal*100))
	return float64(val) / 100.0
}

func (s *chunkFeedbackService) PersistMessageChunkRefs(ctx context.Context, message *types.Message) error {
	if message == nil || len(message.KnowledgeReferences) == 0 {
		return nil
	}
	tenantID := types.MustTenantIDFromContext(ctx)
	refs := make([]*types.MessageChunkRef, 0, len(message.KnowledgeReferences))
	for _, r := range message.KnowledgeReferences {
		if r == nil {
			continue
		}
		if r.ChunkType == string(types.ChunkTypeWebSearch) {
			continue
		}
		if r.ID == "" || r.KnowledgeBaseID == "" || r.KnowledgeID == "" {
			continue
		}
		refs = append(refs, &types.MessageChunkRef{
			TenantID:        tenantID,
			MessageID:       message.ID,
			ChunkID:         r.ID,
			KnowledgeBaseID: r.KnowledgeBaseID,
			KnowledgeID:     r.KnowledgeID,
		})
	}
	if len(refs) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(&refs).Error
}

func (s *chunkFeedbackService) SetMessageFeedback(ctx context.Context, sessionID, messageID, userID string, tenantID uint64, vote types.UserMessageFeedbackVote, dislikeReason string) error {
	switch vote {
	case types.UserMessageFeedbackVoteLike, types.UserMessageFeedbackVoteDislike:
	default:
		return ErrFeedbackInvalidVote
	}

	dislikeReason = strings.TrimSpace(dislikeReason)
	if vote == types.UserMessageFeedbackVoteDislike {
		if dislikeReason == "" {
			return ErrFeedbackReasonRequired
		}
		if len([]rune(dislikeReason)) > 500 {
			return ErrFeedbackReasonTooLong
		}
	} else {
		dislikeReason = ""
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var chunkIDs []string
		if err := tx.Model(&types.MessageChunkRef{}).
			Where("tenant_id = ? AND message_id = ?", tenantID, messageID).
			Pluck("chunk_id", &chunkIDs).Error; err != nil {
			return err
		}
		if len(chunkIDs) == 0 {
			return ErrFeedbackNoChunkRefs
		}

		var existing types.UserMessageFeedback
		exists := true
		if err := tx.Where("tenant_id = ? AND user_id = ? AND message_id = ?", tenantID, userID, messageID).
			First(&existing).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				exists = false
			} else {
				return err
			}
		}

		likeDelta := int64(0)
		dislikeDelta := int64(0)
		if !exists {
			if vote == types.UserMessageFeedbackVoteLike {
				likeDelta = 1
			} else {
				dislikeDelta = 1
			}
		} else if existing.Vote != vote {
			if existing.Vote == types.UserMessageFeedbackVoteLike {
				likeDelta -= 1
			} else if existing.Vote == types.UserMessageFeedbackVoteDislike {
				dislikeDelta -= 1
			}
			if vote == types.UserMessageFeedbackVoteLike {
				likeDelta += 1
			} else {
				dislikeDelta += 1
			}
		}

		if !exists {
			existing = types.UserMessageFeedback{
				ID:            uuid.New().String(),
				TenantID:      tenantID,
				UserID:        userID,
				SessionID:     sessionID,
				MessageID:     messageID,
				Vote:          vote,
				DislikeReason: dislikeReason,
			}
		} else {
			existing.Vote = vote
			existing.DislikeReason = dislikeReason
		}
		if err := tx.Save(&existing).Error; err != nil {
			return err
		}

		triggerType := "user_like"
		if vote == types.UserMessageFeedbackVoteDislike {
			triggerType = "user_dislike"
		}

		if likeDelta != 0 {
			if err := tx.Model(&types.Chunk{}).
				Where("tenant_id = ? AND id IN ?", tenantID, chunkIDs).
				UpdateColumn("like_count", gorm.Expr("like_count + ?", likeDelta)).Error; err != nil {
				return err
			}
		}
		if dislikeDelta != 0 {
			if err := tx.Model(&types.Chunk{}).
				Where("tenant_id = ? AND id IN ?", tenantID, chunkIDs).
				UpdateColumn("dislike_count", gorm.Expr("dislike_count + ?", dislikeDelta)).Error; err != nil {
				return err
			}
		}

		if likeDelta == 0 && dislikeDelta == 0 {
			return nil
		}
		return s.recomputeChunkFeedbackStatsTx(ctx, tx, tenantID, chunkIDs, triggerType, userID, messageID)
	})
}

func (s *chunkFeedbackService) CancelMessageFeedback(ctx context.Context, sessionID, messageID, userID string, tenantID uint64) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var chunkIDs []string
		if err := tx.Model(&types.MessageChunkRef{}).
			Where("tenant_id = ? AND message_id = ?", tenantID, messageID).
			Pluck("chunk_id", &chunkIDs).Error; err != nil {
			return err
		}
		if len(chunkIDs) == 0 {
			return ErrFeedbackNoChunkRefs
		}

		var existing types.UserMessageFeedback
		if err := tx.Where("tenant_id = ? AND user_id = ? AND message_id = ?", tenantID, userID, messageID).
			First(&existing).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}

		likeDelta := int64(0)
		dislikeDelta := int64(0)
		switch existing.Vote {
		case types.UserMessageFeedbackVoteLike:
			likeDelta = -1
		case types.UserMessageFeedbackVoteDislike:
			dislikeDelta = -1
		}

		if err := tx.Where("tenant_id = ? AND user_id = ? AND message_id = ?", tenantID, userID, messageID).
			Delete(&types.UserMessageFeedback{}).Error; err != nil {
			return err
		}

		if likeDelta != 0 {
			if err := tx.Model(&types.Chunk{}).
				Where("tenant_id = ? AND id IN ?", tenantID, chunkIDs).
				UpdateColumn("like_count", gorm.Expr("like_count + ?", likeDelta)).Error; err != nil {
				return err
			}
		}
		if dislikeDelta != 0 {
			if err := tx.Model(&types.Chunk{}).
				Where("tenant_id = ? AND id IN ?", tenantID, chunkIDs).
				UpdateColumn("dislike_count", gorm.Expr("dislike_count + ?", dislikeDelta)).Error; err != nil {
				return err
			}
		}

		if likeDelta == 0 && dislikeDelta == 0 {
			return nil
		}
		return s.recomputeChunkFeedbackStatsTx(ctx, tx, tenantID, chunkIDs, "user_cancel", userID, messageID)
	})
}

func (s *chunkFeedbackService) recomputeChunkFeedbackStatsTx(ctx context.Context, tx *gorm.DB, tenantID uint64, chunkIDs []string, triggerType, userID, messageID string) error {
	var chunks []*types.Chunk
	if err := tx.Where("tenant_id = ? AND id IN ?", tenantID, chunkIDs).Find(&chunks).Error; err != nil {
		return err
	}

	for _, chunk := range chunks {
		if chunk == nil {
			continue
		}
		total := chunk.LikeCount + chunk.DislikeCount
		rate := 0.0
		if total > 0 {
			rate = float64(chunk.LikeCount) / float64(total)
		}

		highThreshold := s.getThresholdRate(ctx, "chunk_feedback.positive_rate_high_threshold", 0.8)
		lowThreshold := s.getThresholdRate(ctx, "chunk_feedback.positive_rate_low_threshold", 0.5)
		needsOptThreshold := s.getThresholdRate(ctx, "chunk_feedback.needs_optimization_threshold", 0.2)
		needsOptimization := total > 0 && rate < needsOptThreshold
		oldWeight := chunk.RecallWeight
		newWeight := oldWeight

		if total > 0 {
			switch {
			case rate >= highThreshold:
				newWeight = math.Min(2.0, oldWeight*1.05)
			case rate < lowThreshold:
				newWeight = math.Max(0.5, oldWeight*0.95)
			}
		}

		if err := tx.Model(&types.Chunk{}).
			Where("tenant_id = ? AND id = ?", tenantID, chunk.ID).
			Updates(map[string]interface{}{
				"positive_rate":      rate,
				"needs_optimization": needsOptimization,
				"recall_weight":      newWeight,
			}).Error; err != nil {
			return err
		}

		if newWeight != oldWeight && !almostEqual(newWeight, oldWeight) {
			logEntry := &types.ChunkRecallWeightLog{
				ID:           uuid.New().String(),
				TenantID:     tenantID,
				ChunkID:      chunk.ID,
				TriggerType:  triggerType,
				UserID:       userID,
				MessageID:    messageID,
				OldWeight:    oldWeight,
				NewWeight:    newWeight,
				LikeCount:    chunk.LikeCount,
				DislikeCount: chunk.DislikeCount,
				PositiveRate: rate,
			}
			if err := tx.Create(logEntry).Error; err != nil {
				logger.Warnf(ctx, "create chunk recall weight log failed: %v", err)
			}
		}
	}
	return nil
}

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

func (s *chunkFeedbackService) ListKnowledgeBaseChunkFeedbackStats(ctx context.Context, tenantID uint64, knowledgeBaseID string, pagination *types.Pagination, maxPositiveRate *float64, needsOptimization *bool) (*types.PageResult, error) {
	if pagination == nil {
		pagination = &types.Pagination{}
	}

	whereSQL := "c.tenant_id = ? AND c.knowledge_base_id = ? AND c.deleted_at IS NULL"
	whereArgs := []interface{}{tenantID, knowledgeBaseID}
	if maxPositiveRate != nil {
		whereSQL += " AND c.positive_rate <= ?"
		whereArgs = append(whereArgs, *maxPositiveRate)
	}
	if needsOptimization != nil {
		whereSQL += " AND c.needs_optimization = ?"
		whereArgs = append(whereArgs, *needsOptimization)
	}

	var total int64
	if err := s.db.WithContext(ctx).
		Raw("SELECT COUNT(1) FROM chunks c WHERE "+whereSQL, whereArgs...).
		Scan(&total).Error; err != nil {
		return nil, err
	}

	listSQL := `
SELECT
    c.id AS chunk_id,
    c.knowledge_id,
    k.title AS knowledge_title,
    k.file_name AS knowledge_filename,
    c.chunk_index,
    substr(c.content, 1, 200) AS content_preview,
    c.like_count,
    c.dislike_count,
    c.positive_rate,
    c.recall_weight,
    c.needs_optimization,
    COALESCE(sc.session_count, 0) AS session_count
FROM chunks c
JOIN knowledges k ON k.id = c.knowledge_id
LEFT JOIN (
    SELECT
        mcr.chunk_id,
        COUNT(DISTINCT m.session_id) AS session_count
    FROM message_chunk_refs mcr
    JOIN messages m ON m.id = mcr.message_id
    WHERE mcr.tenant_id = ? AND mcr.knowledge_base_id = ?
    GROUP BY mcr.chunk_id
) sc ON sc.chunk_id = c.id
WHERE ` + whereSQL + `
ORDER BY c.positive_rate ASC, c.dislike_count DESC
LIMIT ? OFFSET ?`

	args := make([]interface{}, 0, 2+len(whereArgs)+2)
	args = append(args, tenantID, knowledgeBaseID)
	args = append(args, whereArgs...)
	args = append(args, pagination.Limit(), pagination.Offset())

	var items []*types.ChunkFeedbackStats
	if err := s.db.WithContext(ctx).Raw(listSQL, args...).Scan(&items).Error; err != nil {
		return nil, err
	}

	if len(items) > 0 {
		chunkIDs := make([]string, 0, len(items))
		for _, it := range items {
			if it != nil && it.ChunkID != "" {
				chunkIDs = append(chunkIDs, it.ChunkID)
			}
		}

		type row struct {
			ChunkID string `gorm:"column:chunk_id"`
			Reason  string `gorm:"column:reason"`
			Count   int64  `gorm:"column:count"`
		}
		var rows []row
		reasonSQL := `
SELECT
    mcr.chunk_id AS chunk_id,
    umf.dislike_reason AS reason,
    COUNT(1) AS count
FROM user_message_feedbacks umf
JOIN message_chunk_refs mcr ON mcr.tenant_id = umf.tenant_id AND mcr.message_id = umf.message_id
WHERE
    umf.tenant_id = ?
    AND mcr.knowledge_base_id = ?
    AND mcr.chunk_id IN ?
    AND umf.vote = ?
    AND umf.dislike_reason <> ''
GROUP BY mcr.chunk_id, umf.dislike_reason`
		if err := s.db.WithContext(ctx).
			Raw(reasonSQL, tenantID, knowledgeBaseID, chunkIDs, string(types.UserMessageFeedbackVoteDislike)).
			Scan(&rows).Error; err != nil {
			return nil, err
		}

		byChunk := make(map[string][]*types.DislikeReasonStat)
		reasonCounts := make(map[string]map[string]int64) // chunkID -> reasonCode -> count
		for _, r := range rows {
			// Parse the dislike_reason which may be comma-separated codes, e.g. "inaccurate,outdated|supplement"
			rawReason := r.Reason
			// Extract reason codes before the pipe separator
			codesPart := rawReason
			if idx := strings.Index(rawReason, "|"); idx >= 0 {
				codesPart = rawReason[:idx]
			}
			// Split by comma to get individual reasons
			codes := strings.Split(codesPart, ",")
			for _, code := range codes {
				code = strings.TrimSpace(code)
				if code == "" {
					continue
				}
				if reasonCounts[r.ChunkID] == nil {
					reasonCounts[r.ChunkID] = make(map[string]int64)
				}
				reasonCounts[r.ChunkID][code] += r.Count
			}
		}
		for chunkID, counts := range reasonCounts {
			for code, cnt := range counts {
				byChunk[chunkID] = append(byChunk[chunkID], &types.DislikeReasonStat{
					Reason: code,
					Count:  cnt,
				})
			}
		}
		for k, v := range byChunk {
			sort.SliceStable(v, func(i, j int) bool { return v[i].Count > v[j].Count })
			if len(v) > 5 {
				v = v[:5]
			}
			byChunk[k] = v
		}

		for _, it := range items {
			if it == nil {
				continue
			}
			it.DislikeReasons = byChunk[it.ChunkID]
		}
	}

	return types.NewPageResult(total, pagination, items), nil
}

func (s *chunkFeedbackService) ListChunkRecallWeightLogs(ctx context.Context, tenantID uint64, knowledgeBaseID, chunkID string, limit int) ([]*types.ChunkRecallWeightLog, error) {
	var chunk types.Chunk
	if err := s.db.WithContext(ctx).
		Select("id").
		Where("tenant_id = ? AND id = ? AND knowledge_base_id = ?", tenantID, chunkID, knowledgeBaseID).
		First(&chunk).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrChunkFeedbackChunkNotFound
		}
		return nil, err
	}

	if limit <= 0 {
		limit = 50
	}
	var logs []*types.ChunkRecallWeightLog
	err := s.db.WithContext(ctx).
		Where("tenant_id = ? AND chunk_id = ?", tenantID, chunkID).
		Order("created_at DESC").
		Limit(limit).
		Find(&logs).Error
	return logs, err
}

func (s *chunkFeedbackService) UpdateChunkWeight(ctx context.Context, tenantID uint64, knowledgeBaseID, chunkID, userID string, weight float64) error {
	if weight < 0.1 || weight > 10.0 {
		return errors.New("weight must be between 0.1 and 10.0")
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var chunk types.Chunk
		if err := tx.Where("tenant_id = ? AND id = ? AND knowledge_base_id = ?", tenantID, chunkID, knowledgeBaseID).
			First(&chunk).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrChunkFeedbackChunkNotFound
			}
			return err
		}

		oldWeight := chunk.RecallWeight
		if err := tx.Model(&types.Chunk{}).
			Where("tenant_id = ? AND id = ?", tenantID, chunkID).
			UpdateColumn("recall_weight", weight).Error; err != nil {
			return err
		}

		logEntry := &types.ChunkRecallWeightLog{
			ID:           uuid.New().String(),
			TenantID:     tenantID,
			ChunkID:      chunkID,
			TriggerType:  "admin_manual",
			UserID:       userID,
			OldWeight:    oldWeight,
			NewWeight:    weight,
			LikeCount:    chunk.LikeCount,
			DislikeCount: chunk.DislikeCount,
			PositiveRate: chunk.PositiveRate,
		}
		if err := tx.Create(logEntry).Error; err != nil {
			logger.Warnf(ctx, "create chunk recall weight log failed: %v", err)
		}
		return nil
	})
}

func (s *chunkFeedbackService) ResetChunkFeedback(ctx context.Context, tenantID uint64, knowledgeBaseID, chunkID, userID string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var chunk types.Chunk
		if err := tx.Where("tenant_id = ? AND id = ? AND knowledge_base_id = ?", tenantID, chunkID, knowledgeBaseID).
			First(&chunk).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrChunkFeedbackChunkNotFound
			}
			return err
		}

		oldWeight := chunk.RecallWeight
		if err := tx.Model(&types.Chunk{}).
			Where("tenant_id = ? AND id = ?", tenantID, chunkID).
			Updates(map[string]interface{}{
				"like_count":         0,
				"dislike_count":      0,
				"positive_rate":      0,
				"recall_weight":      1,
				"needs_optimization": false,
			}).Error; err != nil {
			return err
		}

		logEntry := &types.ChunkRecallWeightLog{
			ID:           uuid.New().String(),
			TenantID:     tenantID,
			ChunkID:      chunkID,
			TriggerType:  "admin_reset",
			UserID:       userID,
			OldWeight:    oldWeight,
			NewWeight:    1,
			LikeCount:    0,
			DislikeCount: 0,
			PositiveRate: 0,
		}
		if err := tx.Create(logEntry).Error; err != nil {
			logger.Warnf(ctx, "create chunk recall weight log failed: %v", err)
		}
		return nil
	})
}
