package repository

import (
	"context"
	"errors"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type messageFeedbackRepository struct {
	db *gorm.DB
}

func NewMessageFeedbackRepository(db *gorm.DB) interfaces.MessageFeedbackRepository {
	return &messageFeedbackRepository{db: db}
}

func (r *messageFeedbackRepository) SyncMessageChunkReferences(
	ctx context.Context,
	tenantID uint64,
	message *types.Message,
) error {
	if message == nil || message.ID == "" || message.SessionID == "" || len(message.KnowledgeReferences) == 0 {
		return nil
	}

	ids := make([]string, 0, len(message.KnowledgeReferences))
	seen := make(map[string]struct{}, len(message.KnowledgeReferences))
	for _, ref := range message.KnowledgeReferences {
		if ref == nil || ref.ID == "" {
			continue
		}
		if _, ok := seen[ref.ID]; ok {
			continue
		}
		seen[ref.ID] = struct{}{}
		ids = append(ids, ref.ID)
	}
	if len(ids) == 0 {
		return nil
	}

	var chunks []types.Chunk
	if err := r.db.WithContext(ctx).
		Select("id", "tenant_id", "knowledge_base_id", "knowledge_id").
		Where("id IN ?", ids).
		Find(&chunks).Error; err != nil {
		return err
	}
	if len(chunks) == 0 {
		return nil
	}

	rows := make([]types.MessageChunkReference, 0, len(chunks))
	for _, chunk := range chunks {
		rows = append(rows, types.MessageChunkReference{
			TenantID:        tenantID,
			SessionID:       message.SessionID,
			MessageID:       message.ID,
			RequestID:       message.RequestID,
			ChunkTenantID:   chunk.TenantID,
			KnowledgeBaseID: chunk.KnowledgeBaseID,
			KnowledgeID:     chunk.KnowledgeID,
			ChunkID:         chunk.ID,
		})
	}
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "message_id"}, {Name: "chunk_id"}},
			DoNothing: true,
		}).
		Create(&rows).Error
}

func (r *messageFeedbackRepository) GetByMessageIDs(
	ctx context.Context,
	tenantID uint64,
	actorID string,
	messageIDs []string,
) (map[string]*types.MessageFeedbackView, error) {
	result := make(map[string]*types.MessageFeedbackView)
	if tenantID == 0 || actorID == "" || len(messageIDs) == 0 {
		return result, nil
	}
	var rows []types.MessageFeedback
	if err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND actor_id = ? AND message_id IN ?", tenantID, actorID, messageIDs).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		result[row.MessageID] = &types.MessageFeedbackView{
			Rating:       row.Rating,
			ReasonCode:   row.ReasonCode,
			ReasonDetail: row.ReasonDetail,
		}
	}
	return result, nil
}

func (r *messageFeedbackRepository) ApplyFeedback(
	ctx context.Context,
	mutation interfaces.MessageFeedbackMutation,
) (*types.MessageFeedbackView, error) {
	var view *types.MessageFeedbackView
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		refs, err := listMessageChunkReferences(tx, mutation.TenantID, mutation.Message.ID)
		if err != nil {
			return err
		}
		if len(refs) == 0 {
			return errors.New("assistant message has no attributable knowledge chunks")
		}

		var feedback types.MessageFeedback
		err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where(
				"tenant_id = ? AND message_id = ? AND actor_id = ?",
				mutation.TenantID, mutation.Message.ID, mutation.ActorID,
			).
			First(&feedback).Error
		existed := err == nil
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if existed &&
			feedback.Rating == mutation.Rating &&
			feedback.ReasonCode == mutation.ReasonCode &&
			feedback.ReasonDetail == mutation.ReasonDetail {
			view = feedbackView(&feedback)
			return nil
		}

		oldRating := 0
		if existed {
			oldRating = feedback.Rating
		} else {
			feedback = types.MessageFeedback{
				TenantID:  mutation.TenantID,
				SessionID: mutation.Message.SessionID,
				MessageID: mutation.Message.ID,
				ActorID:   mutation.ActorID,
			}
		}
		feedback.Rating = mutation.Rating
		feedback.ReasonCode = mutation.ReasonCode
		feedback.ReasonDetail = mutation.ReasonDetail
		if existed {
			if err := tx.Model(&feedback).Updates(map[string]interface{}{
				"rating":        feedback.Rating,
				"reason_code":   feedback.ReasonCode,
				"reason_detail": feedback.ReasonDetail,
				"updated_at":    time.Now(),
			}).Error; err != nil {
				return err
			}
		} else if err := tx.Create(&feedback).Error; err != nil {
			return err
		}

		affected, err := attributedChunkKeys(tx, feedback.ID)
		if err != nil {
			return err
		}
		if err := tx.Where("feedback_id = ?", feedback.ID).
			Delete(&types.MessageFeedbackAttribution{}).Error; err != nil {
			return err
		}
		attributions := make([]types.MessageFeedbackAttribution, 0, len(refs))
		for _, ref := range refs {
			attributions = append(attributions, types.MessageFeedbackAttribution{
				FeedbackID:      feedback.ID,
				TenantID:        mutation.TenantID,
				SessionID:       mutation.Message.SessionID,
				MessageID:       mutation.Message.ID,
				ChunkTenantID:   ref.ChunkTenantID,
				KnowledgeBaseID: ref.KnowledgeBaseID,
				KnowledgeID:     ref.KnowledgeID,
				ChunkID:         ref.ChunkID,
				Rating:          mutation.Rating,
				ReasonCode:      mutation.ReasonCode,
				ReasonDetail:    mutation.ReasonDetail,
			})
			affected[chunkKey{tenantID: ref.ChunkTenantID, chunkID: ref.ChunkID}] = struct{}{}
		}
		if err := tx.Create(&attributions).Error; err != nil {
			return err
		}

		action := feedbackAction(oldRating, mutation.Rating)
		for _, key := range sortedChunkKeys(affected) {
			if err := recomputeChunkFeedback(
				tx, key, feedback.ID, mutation.ActorID,
				types.ChunkWeightTriggerUserFeedback, action, false,
			); err != nil {
				return err
			}
		}
		view = feedbackView(&feedback)
		return nil
	})
	return view, err
}

func (r *messageFeedbackRepository) CancelFeedback(
	ctx context.Context,
	tenantID uint64,
	actorID string,
	message *types.Message,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var feedback types.MessageFeedback
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("tenant_id = ? AND message_id = ? AND actor_id = ?", tenantID, message.ID, actorID).
			First(&feedback).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		affected, err := attributedChunkKeys(tx, feedback.ID)
		if err != nil {
			return err
		}
		if err := tx.Where("feedback_id = ?", feedback.ID).
			Delete(&types.MessageFeedbackAttribution{}).Error; err != nil {
			return err
		}
		if err := tx.Delete(&feedback).Error; err != nil {
			return err
		}
		for _, key := range sortedChunkKeys(affected) {
			if err := recomputeChunkFeedback(
				tx, key, feedback.ID, actorID,
				types.ChunkWeightTriggerUserFeedback, "cancel", false,
			); err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *messageFeedbackRepository) ListChunkStats(
	ctx context.Context,
	tenantID uint64,
	knowledgeBaseID string,
	filter types.ChunkFeedbackStatsFilter,
) (*types.ChunkFeedbackStatsPage, error) {
	page, pageSize := filter.Page, filter.PageSize
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}

	base := r.db.WithContext(ctx).Model(&types.Chunk{}).
		Where("chunks.tenant_id = ? AND chunks.knowledge_base_id = ?", tenantID, knowledgeBaseID)
	if filter.MinPositiveRate != nil {
		base = base.Where(
			"(chunks.positive_feedback_count + chunks.negative_feedback_count) > 0 AND chunks.positive_feedback_rate >= ?",
			*filter.MinPositiveRate,
		)
	}
	if filter.MaxPositiveRate != nil {
		base = base.Where(
			"(chunks.positive_feedback_count + chunks.negative_feedback_count) > 0 AND chunks.positive_feedback_rate <= ?",
			*filter.MaxPositiveRate,
		)
	}
	if filter.Status != "" {
		base = base.Where("chunks.feedback_status = ?", filter.Status)
	}
	if keyword := strings.TrimSpace(filter.Keyword); keyword != "" {
		like := "%" + strings.ToLower(keyword) + "%"
		base = base.Joins("LEFT JOIN knowledges keyword_knowledge ON keyword_knowledge.id = chunks.knowledge_id").
			Where("(LOWER(chunks.content) LIKE ? OR LOWER(keyword_knowledge.title) LIKE ?)", like, like)
	}

	var total int64
	if err := base.Count(&total).Error; err != nil {
		return nil, err
	}

	sortColumns := map[string]string{
		"positive_rate":         "chunks.positive_feedback_rate",
		"positive_count":        "chunks.positive_feedback_count",
		"negative_count":        "chunks.negative_feedback_count",
		"recall_weight":         "chunks.recall_weight",
		"related_session_count": "related_session_count",
		"updated_at":            "chunks.updated_at",
	}
	sortColumn := sortColumns[filter.SortBy]
	if sortColumn == "" {
		sortColumn = "chunks.positive_feedback_rate"
	}
	sortOrder := "ASC"
	if strings.EqualFold(filter.SortOrder, "desc") {
		sortOrder = "DESC"
	}

	var data []*types.ChunkFeedbackStats
	query := base.
		Select(
			"chunks.id AS chunk_id, chunks.knowledge_id, knowledges.title AS knowledge_title, " +
				"chunks.chunk_index, chunks.content, " +
				"chunks.positive_feedback_count AS positive_count, " +
				"chunks.negative_feedback_count AS negative_count, " +
				"chunks.positive_feedback_rate AS positive_rate, chunks.recall_weight, chunks.feedback_status, " +
				"COUNT(DISTINCT message_chunk_references.session_id) AS related_session_count",
		).
		Joins("LEFT JOIN knowledges ON knowledges.id = chunks.knowledge_id AND knowledges.deleted_at IS NULL").
		Joins("LEFT JOIN message_chunk_references ON message_chunk_references.chunk_id = chunks.id").
		Group(
			"chunks.id, chunks.knowledge_id, knowledges.title, chunks.chunk_index, chunks.content, " +
				"chunks.positive_feedback_count, chunks.negative_feedback_count, " +
				"chunks.positive_feedback_rate, chunks.recall_weight, chunks.feedback_status",
		).
		Order(sortColumn + " " + sortOrder).
		Order("chunks.id ASC").
		Offset((page - 1) * pageSize).
		Limit(pageSize)
	if err := query.Scan(&data).Error; err != nil {
		return nil, err
	}
	if err := r.attachReasonCounts(ctx, data); err != nil {
		return nil, err
	}
	return &types.ChunkFeedbackStatsPage{
		Data: data, Total: total, Page: page, PageSize: pageSize,
	}, nil
}

func (r *messageFeedbackRepository) attachReasonCounts(
	ctx context.Context,
	stats []*types.ChunkFeedbackStats,
) error {
	if len(stats) == 0 {
		return nil
	}
	ids := make([]string, 0, len(stats))
	byID := make(map[string]*types.ChunkFeedbackStats, len(stats))
	for _, item := range stats {
		ids = append(ids, item.ChunkID)
		item.ReasonCounts = make([]types.FeedbackReasonCount, 0)
		byID[item.ChunkID] = item
	}
	var rows []struct {
		ChunkID    string
		ReasonCode string
		Count      int64
	}
	if err := r.db.WithContext(ctx).Model(&types.MessageFeedbackAttribution{}).
		Select("chunk_id, reason_code, COUNT(*) AS count").
		Where("chunk_id IN ? AND rating = ? AND reason_code <> ''", ids, types.MessageFeedbackDislike).
		Group("chunk_id, reason_code").
		Order("count DESC").
		Scan(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		if item := byID[row.ChunkID]; item != nil {
			item.ReasonCounts = append(item.ReasonCounts, types.FeedbackReasonCount{
				ReasonCode: row.ReasonCode,
				Count:      row.Count,
			})
		}
	}
	return nil
}

func (r *messageFeedbackRepository) ListWeightLogs(
	ctx context.Context,
	tenantID uint64,
	knowledgeBaseID string,
	chunkID string,
	limit int,
) ([]*types.ChunkFeedbackWeightLog, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var logs []*types.ChunkFeedbackWeightLog
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND knowledge_base_id = ? AND chunk_id = ?", tenantID, knowledgeBaseID, chunkID).
		Order("created_at DESC, id DESC").
		Limit(limit).
		Find(&logs).Error
	return logs, err
}

func (r *messageFeedbackRepository) ResetChunk(
	ctx context.Context,
	tenantID uint64,
	knowledgeBaseID string,
	chunkID string,
	actorID string,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var chunk types.Chunk
		if err := tx.
			Where("tenant_id = ? AND knowledge_base_id = ? AND id = ?", tenantID, knowledgeBaseID, chunkID).
			First(&chunk).Error; err != nil {
			return err
		}
		if err := tx.Where("chunk_tenant_id = ? AND chunk_id = ?", tenantID, chunkID).
			Delete(&types.MessageFeedbackAttribution{}).Error; err != nil {
			return err
		}
		return recomputeChunkFeedback(
			tx,
			chunkKey{tenantID: tenantID, chunkID: chunkID},
			"",
			actorID,
			types.ChunkWeightTriggerAdminReset,
			"reset",
			true,
		)
	})
}

type chunkKey struct {
	tenantID uint64
	chunkID  string
}

func listMessageChunkReferences(
	tx *gorm.DB,
	tenantID uint64,
	messageID string,
) ([]types.MessageChunkReference, error) {
	var refs []types.MessageChunkReference
	err := tx.Where("tenant_id = ? AND message_id = ?", tenantID, messageID).Find(&refs).Error
	return refs, err
}

func attributedChunkKeys(tx *gorm.DB, feedbackID string) (map[chunkKey]struct{}, error) {
	var rows []struct {
		ChunkTenantID uint64
		ChunkID       string
	}
	if err := tx.Model(&types.MessageFeedbackAttribution{}).
		Select("chunk_tenant_id, chunk_id").
		Where("feedback_id = ?", feedbackID).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	result := make(map[chunkKey]struct{}, len(rows))
	for _, row := range rows {
		result[chunkKey{tenantID: row.ChunkTenantID, chunkID: row.ChunkID}] = struct{}{}
	}
	return result, nil
}

func sortedChunkKeys(keys map[chunkKey]struct{}) []chunkKey {
	result := make([]chunkKey, 0, len(keys))
	for key := range keys {
		result = append(result, key)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].tenantID != result[j].tenantID {
			return result[i].tenantID < result[j].tenantID
		}
		return result[i].chunkID < result[j].chunkID
	})
	return result
}

func feedbackView(feedback *types.MessageFeedback) *types.MessageFeedbackView {
	return &types.MessageFeedbackView{
		Rating:       feedback.Rating,
		ReasonCode:   feedback.ReasonCode,
		ReasonDetail: feedback.ReasonDetail,
	}
}

func feedbackAction(oldRating, newRating int) string {
	if oldRating == types.MessageFeedbackLike && newRating == types.MessageFeedbackDislike {
		return "switch_to_dislike"
	}
	if oldRating == types.MessageFeedbackDislike && newRating == types.MessageFeedbackLike {
		return "switch_to_like"
	}
	if newRating == types.MessageFeedbackLike {
		return "like"
	}
	return "dislike"
}

func recomputeChunkFeedback(
	tx *gorm.DB,
	key chunkKey,
	feedbackID string,
	actorID string,
	triggerSource string,
	triggerAction string,
	forceLog bool,
) error {
	var chunk types.Chunk
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("tenant_id = ? AND id = ?", key.tenantID, key.chunkID).
		First(&chunk).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}

	var counts struct {
		Positive int64
		Negative int64
	}
	if err := tx.Model(&types.MessageFeedbackAttribution{}).
		Select(
			"COALESCE(SUM(CASE WHEN rating = ? THEN 1 ELSE 0 END), 0) AS positive, "+
				"COALESCE(SUM(CASE WHEN rating = ? THEN 1 ELSE 0 END), 0) AS negative",
			types.MessageFeedbackLike,
			types.MessageFeedbackDislike,
		).
		Where("chunk_tenant_id = ? AND chunk_id = ?", key.tenantID, key.chunkID).
		Scan(&counts).Error; err != nil {
		return err
	}

	var tenant types.Tenant
	if err := tx.Select("id", "retrieval_config").First(&tenant, key.tenantID).Error; err != nil {
		return err
	}
	rate, weight, status := calculateChunkFeedbackMetrics(counts.Positive, counts.Negative, tenant.RetrievalConfig)
	oldWeight := chunk.EffectiveRecallWeight()
	oldStatus := chunk.FeedbackStatus
	if oldStatus == "" {
		oldStatus = types.ChunkFeedbackStatusNormal
	}

	changedWeight := math.Abs(oldWeight-weight) > 1e-9 || oldStatus != status
	oldPositive, oldNegative, oldRate := chunk.PositiveFeedbackCount, chunk.NegativeFeedbackCount, chunk.PositiveFeedbackRate
	if err := tx.Model(&types.Chunk{}).
		Where("tenant_id = ? AND id = ?", key.tenantID, key.chunkID).
		Updates(map[string]interface{}{
			"positive_feedback_count": counts.Positive,
			"negative_feedback_count": counts.Negative,
			"positive_feedback_rate":  rate,
			"recall_weight":           weight,
			"feedback_status":         status,
			"updated_at":              time.Now(),
		}).Error; err != nil {
		return err
	}
	if !changedWeight && !forceLog {
		return nil
	}
	return tx.Create(&types.ChunkFeedbackWeightLog{
		TenantID:         key.tenantID,
		KnowledgeBaseID:  chunk.KnowledgeBaseID,
		ChunkID:          key.chunkID,
		FeedbackID:       feedbackID,
		ActorID:          actorID,
		TriggerSource:    triggerSource,
		TriggerAction:    triggerAction,
		OldPositiveCount: oldPositive,
		NewPositiveCount: counts.Positive,
		OldNegativeCount: oldNegative,
		NewNegativeCount: counts.Negative,
		OldPositiveRate:  oldRate,
		NewPositiveRate:  rate,
		OldRecallWeight:  oldWeight,
		NewRecallWeight:  weight,
		OldStatus:        oldStatus,
		NewStatus:        status,
	}).Error
}

func calculateChunkFeedbackMetrics(
	positive int64,
	negative int64,
	config *types.RetrievalConfig,
) (rate float64, weight float64, status string) {
	total := positive + negative
	weight = 1
	status = types.ChunkFeedbackStatusNormal
	if total == 0 {
		return
	}
	rate = float64(positive) / float64(total)
	if total < int64(config.GetEffectiveFeedbackMinCount()) {
		return
	}
	positiveThreshold, negativeThreshold, optimizeThreshold := config.GetEffectiveFeedbackThresholds()
	boostWeight, reduceWeight := config.GetEffectiveFeedbackWeights()
	switch {
	case rate >= positiveThreshold:
		weight = boostWeight
	case rate < negativeThreshold:
		weight = reduceWeight
	}
	if rate < optimizeThreshold {
		status = types.ChunkFeedbackStatusPendingOptimization
	}
	return
}
