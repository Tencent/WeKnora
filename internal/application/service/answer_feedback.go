package service

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// answerFeedbackService attributes answer ratings to the knowledge chunks
// those answers cited, and exposes the chunk stats an admin uses to clean up
// low-quality material.
type answerFeedbackService struct {
	db       *gorm.DB
	sessions interfaces.SessionRepository
	tenants  interfaces.TenantRepository
	// policy is replaced in tests. Production uses the tenant retrieval config.
	policy func(ctx context.Context, tenantID uint64) types.ChunkFeedbackConfig
}

// NewAnswerFeedbackService creates the feedback service.
func NewAnswerFeedbackService(
	db *gorm.DB,
	sessions interfaces.SessionRepository,
	tenants interfaces.TenantRepository,
) interfaces.AnswerFeedbackService {
	svc := &answerFeedbackService{db: db, sessions: sessions, tenants: tenants}
	svc.policy = svc.loadPolicy
	return svc
}

func (s *answerFeedbackService) loadPolicy(ctx context.Context, tenantID uint64) types.ChunkFeedbackConfig {
	if s.tenants == nil || tenantID == 0 {
		return types.DefaultChunkFeedbackConfig()
	}
	tenant, err := s.tenants.GetTenantByID(ctx, tenantID)
	if err != nil || tenant == nil {
		return types.DefaultChunkFeedbackConfig()
	}
	return tenant.RetrievalConfig.EffectiveChunkFeedback()
}

func (s *answerFeedbackService) RecordAnswerChunks(
	ctx context.Context, sessionID, messageID string, refs []types.AnswerChunkRef,
) error {
	tenantID, err := feedbackTenantID(ctx)
	if err != nil {
		return err
	}
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(messageID) == "" {
		return apperrors.NewBadRequestError("session_id and message_id are required")
	}
	return s.insertLinks(s.db.WithContext(ctx), tenantID, sessionID, messageID, refs)
}

func (s *answerFeedbackService) Submit(
	ctx context.Context, sessionID, messageID, rating, reason string,
) (*types.MessageFeedbackState, error) {
	tenantID, userID, err := feedbackActor(ctx)
	if err != nil {
		return nil, err
	}
	next, err := requireFeedbackRating(rating)
	if err != nil {
		return nil, err
	}
	reason = strings.TrimSpace(reason)
	if next != types.AnswerFeedbackDislike {
		reason = ""
	}
	if utf8.RuneCountInString(reason) > types.AnswerFeedbackReasonMaxRunes {
		return nil, apperrors.NewBadRequestError("dislike reason is too long")
	}
	if err := s.requireOwnedSession(ctx, tenantID, sessionID); err != nil {
		return nil, err
	}
	message, err := s.loadAssistantMessage(ctx, sessionID, messageID)
	if err != nil {
		return nil, err
	}
	policy := s.policy(ctx, tenantID)
	var state types.MessageFeedbackState
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		saved, applyErr := s.applyFeedback(tx, tenantID, userID, message, next, reason, policy)
		if applyErr != nil {
			return applyErr
		}
		state = *saved
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &state, nil
}

func (s *answerFeedbackService) Get(
	ctx context.Context, sessionID, messageID string,
) (*types.MessageFeedbackState, error) {
	tenantID, userID, err := feedbackActor(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.requireOwnedSession(ctx, tenantID, sessionID); err != nil {
		return nil, err
	}
	if _, err := s.loadAssistantMessage(ctx, sessionID, messageID); err != nil {
		return nil, err
	}
	row, err := findFeedback(s.db.WithContext(ctx), messageID, userID)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return &types.MessageFeedbackState{Rating: types.AnswerFeedbackNone}, nil
	}
	return feedbackState(row, 0), nil
}

func (s *answerFeedbackService) ListForMessages(
	ctx context.Context, messageIDs []string,
) (map[string]*types.MessageFeedbackState, error) {
	tenantID, userID, err := feedbackActor(ctx)
	if err != nil || len(messageIDs) == 0 {
		if err != nil {
			return nil, err
		}
		return map[string]*types.MessageFeedbackState{}, nil
	}
	var rows []types.AnswerFeedback
	err = s.db.WithContext(ctx).
		Where("tenant_id = ? AND user_id = ? AND message_id IN ?", tenantID, userID, messageIDs).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make(map[string]*types.MessageFeedbackState, len(rows))
	for i := range rows {
		out[rows[i].MessageID] = feedbackState(&rows[i], 0)
	}
	return out, nil
}

func (s *answerFeedbackService) ListChunkFeedback(
	ctx context.Context, knowledgeBaseID string, filter types.ChunkFeedbackListFilter,
) (*types.ChunkFeedbackPage, error) {
	tenantID, err := feedbackTenantID(ctx)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(knowledgeBaseID) == "" {
		return nil, apperrors.NewBadRequestError("knowledge base id is required")
	}
	page, pageSize := normalizeFeedbackPage(filter.Page, filter.PageSize)
	policy := s.policy(ctx, tenantID)
	q := s.db.WithContext(ctx).Model(&types.Chunk{}).Where("knowledge_base_id = ?", knowledgeBaseID)
	if !filter.IncludeUnrated {
		q = q.Where(
			"like_count + dislike_count > 0 OR needs_optimization = ? OR (recall_weight > 0 AND recall_weight <> 1)",
			true,
		)
	}
	if filter.MinApproval != nil {
		q = q.Where("approval_rate IS NOT NULL AND approval_rate >= ?", *filter.MinApproval)
	}
	if filter.MaxApproval != nil {
		q = q.Where("approval_rate IS NOT NULL AND approval_rate <= ?", *filter.MaxApproval)
	}
	if filter.NeedsOptimization != nil {
		q = q.Where("needs_optimization = ?", *filter.NeedsOptimization)
	}
	if filter.LowQuality {
		q = q.Where(
			"needs_optimization = ? OR (approval_rate IS NOT NULL AND approval_rate < ?)",
			true, policy.DegradeThreshold,
		)
	}
	var total int64
	if err := q.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, err
	}
	var chunks []types.Chunk
	err = q.Session(&gorm.Session{}).
		Order("CASE WHEN approval_rate IS NULL THEN 1 ELSE 0 END, approval_rate ASC, dislike_count DESC, id ASC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&chunks).Error
	if err != nil {
		return nil, err
	}
	items, err := s.summarizeChunks(ctx, chunks)
	if err != nil {
		return nil, err
	}
	return &types.ChunkFeedbackPage{Items: items, Total: total, Page: page, PageSize: pageSize}, nil
}

func (s *answerFeedbackService) GetChunkFeedback(
	ctx context.Context, knowledgeBaseID, chunkID string,
) (*types.ChunkFeedbackDetail, error) {
	if _, err := feedbackTenantID(ctx); err != nil {
		return nil, err
	}
	chunk, err := s.loadKBChunk(ctx, knowledgeBaseID, chunkID)
	if err != nil {
		return nil, err
	}
	return s.chunkDetail(ctx, chunk)
}

func (s *answerFeedbackService) ResetChunkFeedback(
	ctx context.Context, knowledgeBaseID, chunkID string,
) (*types.ChunkFeedbackDetail, error) {
	tenantID, userID, err := feedbackActor(ctx)
	if err != nil {
		return nil, err
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		chunk, loadErr := lockKBChunk(tx, knowledgeBaseID, chunkID)
		if loadErr != nil {
			return loadErr
		}
		now := time.Now().UTC()
		oldWeight := types.EffectiveRecallWeight(chunk.RecallWeight)
		if err := tx.Model(&types.Chunk{}).Where("id = ?", chunk.ID).Updates(map[string]interface{}{
			"like_count":         0,
			"dislike_count":      0,
			"approval_rate":      nil,
			"recall_weight":      1,
			"needs_optimization": false,
			"updated_at":         now,
		}).Error; err != nil {
			return err
		}
		if err := tx.Where("chunk_id = ?", chunk.ID).Delete(&types.ChunkFeedbackAttribution{}).Error; err != nil {
			return err
		}
		return tx.Create(&types.ChunkWeightLog{
			ID:              uuid.NewString(),
			TenantID:        tenantID,
			ChunkID:         chunk.ID,
			KnowledgeID:     chunk.KnowledgeID,
			KnowledgeBaseID: chunk.KnowledgeBaseID,
			OldWeight:       oldWeight,
			NewWeight:       1,
			OldApprovalRate: cloneFloat(chunk.ApprovalRate),
			LikeCount:       0,
			DislikeCount:    0,
			TriggerSource:   types.ChunkWeightTriggerReset,
			UserID:          userID,
			CreatedAt:       now,
		}).Error
	})
	if err != nil {
		return nil, err
	}
	return s.GetChunkFeedback(ctx, knowledgeBaseID, chunkID)
}

func (s *answerFeedbackService) applyFeedback(
	tx *gorm.DB,
	tenantID uint64,
	userID string,
	message *types.Message,
	next, reason string,
	policy types.ChunkFeedbackConfig,
) (*types.MessageFeedbackState, error) {
	existing, err := findFeedback(tx, message.ID, userID)
	if err != nil {
		return nil, err
	}
	if existing == nil && next == types.AnswerFeedbackNone {
		return &types.MessageFeedbackState{Rating: types.AnswerFeedbackNone}, nil
	}
	now := time.Now().UTC()
	feedback := existing
	if feedback == nil {
		feedback = &types.AnswerFeedback{
			ID:        uuid.NewString(),
			TenantID:  tenantID,
			UserID:    userID,
			SessionID: message.SessionID,
			MessageID: message.ID,
			CreatedAt: now,
		}
	}
	if next == types.AnswerFeedbackNone {
		if err := tx.Delete(&types.AnswerFeedback{}, "id = ?", feedback.ID).Error; err != nil {
			return nil, err
		}
	} else {
		feedback.Rating = next
		feedback.Reason = reason
		feedback.UpdatedAt = now
		if existing == nil {
			if err := tx.Create(feedback).Error; err != nil {
				return nil, err
			}
		} else if err := tx.Model(&types.AnswerFeedback{}).Where("id = ?", feedback.ID).Updates(map[string]interface{}{
			"rating":     next,
			"reason":     reason,
			"updated_at": now,
		}).Error; err != nil {
			return nil, err
		}
	}

	var linkCount int64
	countErr := tx.Model(&types.AnswerChunkLink{}).Where("message_id = ?", message.ID).Count(&linkCount).Error
	if countErr != nil {
		return nil, countErr
	}
	if linkCount == 0 {
		refs := types.AnswerChunkRefsFromMessage(message)
		if err := s.insertLinks(tx, tenantID, message.SessionID, message.ID, refs); err != nil {
			return nil, err
		}
	}
	var links []types.AnswerChunkLink
	if err := tx.Where("message_id = ?", message.ID).Find(&links).Error; err != nil {
		return nil, err
	}
	trigger := types.WeightTriggerForRating(next)
	for _, link := range links {
		err := s.applyChunkDelta(
			tx, tenantID, userID, feedback.ID, message.ID, link, next, reason, trigger, policy, now,
		)
		if err != nil {
			return nil, err
		}
	}
	if next == types.AnswerFeedbackNone {
		return &types.MessageFeedbackState{Rating: types.AnswerFeedbackNone, ChunkCount: len(links)}, nil
	}
	return &types.MessageFeedbackState{Rating: next, Reason: reason, ChunkCount: len(links)}, nil
}

func (s *answerFeedbackService) applyChunkDelta(
	tx *gorm.DB,
	tenantID uint64,
	userID, feedbackID, messageID string,
	link types.AnswerChunkLink,
	next, reason, trigger string,
	policy types.ChunkFeedbackConfig,
	now time.Time,
) error {
	chunk, err := lockChunk(tx, link.ChunkID)
	if err != nil {
		return err
	}
	if chunk == nil {
		return nil
	}
	var attr types.ChunkFeedbackAttribution
	prev := types.AnswerFeedbackNone
	attrErr := tx.Where("feedback_id = ? AND chunk_id = ?", feedbackID, chunk.ID).First(&attr).Error
	switch {
	case attrErr == nil:
		prev = attr.Rating
	case errors.Is(attrErr, gorm.ErrRecordNotFound):
	default:
		return attrErr
	}
	dLike, dDislike := types.FeedbackCountDelta(prev, next)
	likes := chunk.LikeCount + dLike
	dislikes := chunk.DislikeCount + dDislike
	if likes < 0 {
		likes = 0
	}
	if dislikes < 0 {
		dislikes = 0
	}
	decision := types.DecideChunkWeight(policy, likes, dislikes, chunk.RecallWeight)
	if err := tx.Model(&types.Chunk{}).Where("id = ?", chunk.ID).Updates(map[string]interface{}{
		"like_count":         likes,
		"dislike_count":      dislikes,
		"approval_rate":      decision.ApprovalRate,
		"recall_weight":      decision.Weight,
		"needs_optimization": decision.NeedsOptimization,
		"updated_at":         now,
	}).Error; err != nil {
		return err
	}
	if decision.Changed {
		log := types.ChunkWeightLog{
			ID:              uuid.NewString(),
			TenantID:        tenantID,
			ChunkID:         chunk.ID,
			KnowledgeID:     chunk.KnowledgeID,
			KnowledgeBaseID: chunk.KnowledgeBaseID,
			OldWeight:       types.EffectiveRecallWeight(chunk.RecallWeight),
			NewWeight:       decision.Weight,
			OldApprovalRate: cloneFloat(chunk.ApprovalRate),
			NewApprovalRate: cloneFloat(decision.ApprovalRate),
			LikeCount:       likes,
			DislikeCount:    dislikes,
			TriggerSource:   trigger,
			MessageID:       messageID,
			UserID:          userID,
			CreatedAt:       now,
		}
		if err := tx.Create(&log).Error; err != nil {
			return err
		}
	}
	if next == types.AnswerFeedbackNone {
		if attr.ID != "" {
			return tx.Delete(&types.ChunkFeedbackAttribution{}, "id = ?", attr.ID).Error
		}
		return nil
	}
	if attr.ID == "" {
		return tx.Create(&types.ChunkFeedbackAttribution{
			ID:         uuid.NewString(),
			TenantID:   tenantID,
			FeedbackID: feedbackID,
			ChunkID:    chunk.ID,
			Rating:     next,
			Reason:     reason,
			AppliedAt:  now,
		}).Error
	}
	return tx.Model(&types.ChunkFeedbackAttribution{}).Where("id = ?", attr.ID).Updates(map[string]interface{}{
		"rating":     next,
		"reason":     reason,
		"applied_at": now,
	}).Error
}

func (s *answerFeedbackService) insertLinks(
	db *gorm.DB, tenantID uint64, sessionID, messageID string, refs []types.AnswerChunkRef,
) error {
	if len(refs) == 0 {
		return nil
	}
	now := time.Now().UTC()
	seen := make(map[string]struct{}, len(refs))
	links := make([]types.AnswerChunkLink, 0, len(refs))
	for _, ref := range refs {
		id := strings.TrimSpace(ref.ChunkID)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		links = append(links, types.AnswerChunkLink{
			ID:              uuid.NewString(),
			TenantID:        tenantID,
			SessionID:       sessionID,
			MessageID:       messageID,
			ChunkID:         id,
			KnowledgeID:     ref.KnowledgeID,
			KnowledgeBaseID: ref.KnowledgeBaseID,
			CreatedAt:       now,
		})
	}
	if len(links) == 0 {
		return nil
	}
	err := db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "message_id"}, {Name: "chunk_id"}},
		DoNothing: true,
	}).Create(&links).Error
	if err != nil {
		ctx := db.Statement.Context
		if ctx == nil {
			ctx = context.Background()
		}
		logger.Warnf(ctx, "Failed to store answer chunk links for message %s: %v", messageID, err)
	}
	return err
}

func (s *answerFeedbackService) requireOwnedSession(ctx context.Context, tenantID uint64, sessionID string) error {
	if s.sessions == nil {
		return apperrors.NewInternalServerError("session repository is not configured")
	}
	if strings.TrimSpace(sessionID) == "" {
		return apperrors.NewBadRequestError("session_id is required")
	}
	_, err := s.sessions.Get(ctx, tenantID, sessionUserIDForLookup(ctx), sessionID)
	return err
}

func (s *answerFeedbackService) loadAssistantMessage(
	ctx context.Context, sessionID, messageID string,
) (*types.Message, error) {
	if strings.TrimSpace(messageID) == "" {
		return nil, apperrors.NewBadRequestError("message_id is required")
	}
	var message types.Message
	err := s.db.WithContext(ctx).Where("id = ? AND session_id = ?", messageID, sessionID).First(&message).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperrors.NewNotFoundError("message not found")
	}
	if err != nil {
		return nil, err
	}
	if message.Role != "assistant" {
		return nil, apperrors.NewBadRequestError("feedback is only available on assistant answers")
	}
	if !message.IsCompleted {
		return nil, apperrors.NewBadRequestError("answer is not ready for feedback")
	}
	return &message, nil
}

func findFeedback(db *gorm.DB, messageID, userID string) (*types.AnswerFeedback, error) {
	var row types.AnswerFeedback
	err := db.Where("message_id = ? AND user_id = ?", messageID, userID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func (s *answerFeedbackService) loadKBChunk(
	ctx context.Context, knowledgeBaseID, chunkID string,
) (*types.Chunk, error) {
	if strings.TrimSpace(knowledgeBaseID) == "" || strings.TrimSpace(chunkID) == "" {
		return nil, apperrors.NewBadRequestError("knowledge base id and chunk id are required")
	}
	var chunk types.Chunk
	err := s.db.WithContext(ctx).
		Where("id = ? AND knowledge_base_id = ?", chunkID, knowledgeBaseID).
		First(&chunk).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperrors.NewNotFoundError("chunk not found")
	}
	if err != nil {
		return nil, err
	}
	return &chunk, nil
}

func (s *answerFeedbackService) summarizeChunks(
	ctx context.Context, chunks []types.Chunk,
) ([]types.ChunkFeedbackSummary, error) {
	items := make([]types.ChunkFeedbackSummary, 0, len(chunks))
	if len(chunks) == 0 {
		return items, nil
	}
	ids := make([]string, 0, len(chunks))
	knowledgeIDs := make([]string, 0, len(chunks))
	seenKnowledge := map[string]struct{}{}
	for _, chunk := range chunks {
		ids = append(ids, chunk.ID)
		if chunk.KnowledgeID != "" {
			if _, ok := seenKnowledge[chunk.KnowledgeID]; !ok {
				seenKnowledge[chunk.KnowledgeID] = struct{}{}
				knowledgeIDs = append(knowledgeIDs, chunk.KnowledgeID)
			}
		}
	}
	counts, err := s.sessionCounts(ctx, ids)
	if err != nil {
		return nil, err
	}
	titles, err := s.knowledgeTitles(ctx, knowledgeIDs)
	if err != nil {
		return nil, err
	}
	for _, chunk := range chunks {
		items = append(items, summaryFromChunk(&chunk, titles[chunk.KnowledgeID], counts[chunk.ID]))
	}
	return items, nil
}

func (s *answerFeedbackService) chunkDetail(
	ctx context.Context, chunk *types.Chunk,
) (*types.ChunkFeedbackDetail, error) {
	summaries, err := s.summarizeChunks(ctx, []types.Chunk{*chunk})
	if err != nil {
		return nil, err
	}
	detail := &types.ChunkFeedbackDetail{ChunkFeedbackSummary: summaries[0]}
	var reasons []types.ChunkFeedbackReason
	err = s.db.WithContext(ctx).Model(&types.ChunkFeedbackAttribution{}).
		Select("reason, COUNT(*) as count").
		Where("chunk_id = ? AND rating = ?", chunk.ID, types.AnswerFeedbackDislike).
		Group("reason").
		Order("count DESC").
		Scan(&reasons).Error
	if err != nil {
		return nil, err
	}
	if reasons == nil {
		reasons = []types.ChunkFeedbackReason{}
	}
	detail.DislikeReasons = reasons

	var links []types.AnswerChunkLink
	err = s.db.WithContext(ctx).
		Where("chunk_id = ?", chunk.ID).
		Order("created_at DESC").
		Limit(50).
		Find(&links).Error
	if err != nil {
		return nil, err
	}
	sessions := make([]types.ChunkFeedbackSession, 0, len(links))
	for _, link := range links {
		sessions = append(sessions, types.ChunkFeedbackSession{
			SessionID: link.SessionID,
			MessageID: link.MessageID,
			CreatedAt: link.CreatedAt,
		})
	}
	detail.Sessions = sessions

	var logs []types.ChunkWeightLog
	err = s.db.WithContext(ctx).
		Where("chunk_id = ?", chunk.ID).
		Order("created_at DESC").
		Limit(50).
		Find(&logs).Error
	if err != nil {
		return nil, err
	}
	views := make([]types.ChunkWeightLogView, 0, len(logs))
	for _, row := range logs {
		views = append(views, types.ChunkWeightLogView{
			ID:              row.ID,
			OldWeight:       row.OldWeight,
			NewWeight:       row.NewWeight,
			OldApprovalRate: row.OldApprovalRate,
			NewApprovalRate: row.NewApprovalRate,
			LikeCount:       row.LikeCount,
			DislikeCount:    row.DislikeCount,
			TriggerSource:   row.TriggerSource,
			MessageID:       row.MessageID,
			UserID:          row.UserID,
			CreatedAt:       row.CreatedAt,
		})
	}
	detail.WeightLogs = views
	return detail, nil
}

func (s *answerFeedbackService) sessionCounts(ctx context.Context, chunkIDs []string) (map[string]int, error) {
	out := map[string]int{}
	if len(chunkIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		ChunkID string
		N       int
	}
	err := s.db.WithContext(ctx).Model(&types.AnswerChunkLink{}).
		Select("chunk_id, COUNT(DISTINCT session_id) as n").
		Where("chunk_id IN ?", chunkIDs).
		Group("chunk_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.ChunkID] = row.N
	}
	return out, nil
}

func (s *answerFeedbackService) knowledgeTitles(ctx context.Context, knowledgeIDs []string) (map[string]string, error) {
	out := map[string]string{}
	if len(knowledgeIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		ID    string
		Title string
	}
	err := s.db.WithContext(ctx).Table("knowledges").
		Select("id, title").
		Where("id IN ?", knowledgeIDs).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.ID] = row.Title
	}
	return out, nil
}

func summaryFromChunk(chunk *types.Chunk, title string, sessions int) types.ChunkFeedbackSummary {
	weight := types.EffectiveRecallWeight(chunk.RecallWeight)
	return types.ChunkFeedbackSummary{
		ChunkID:           chunk.ID,
		KnowledgeID:       chunk.KnowledgeID,
		KnowledgeTitle:    title,
		ContentPreview:    types.ChunkContentPreview(chunk.Content),
		LikeCount:         chunk.LikeCount,
		DislikeCount:      chunk.DislikeCount,
		ApprovalRate:      cloneFloat(chunk.ApprovalRate),
		RecallWeight:      weight,
		NeedsOptimization: chunk.NeedsOptimization,
		SessionCount:      sessions,
	}
}

func lockChunk(tx *gorm.DB, id string) (*types.Chunk, error) {
	q := tx
	if tx.Name() != "sqlite" {
		q = tx.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var chunk types.Chunk
	err := q.Where("id = ?", id).First(&chunk).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &chunk, nil
}

func lockKBChunk(tx *gorm.DB, knowledgeBaseID, chunkID string) (*types.Chunk, error) {
	if strings.TrimSpace(knowledgeBaseID) == "" || strings.TrimSpace(chunkID) == "" {
		return nil, apperrors.NewBadRequestError("knowledge base id and chunk id are required")
	}
	q := tx
	if tx.Name() != "sqlite" {
		q = tx.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var chunk types.Chunk
	err := q.Where("id = ? AND knowledge_base_id = ?", chunkID, knowledgeBaseID).First(&chunk).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperrors.NewNotFoundError("chunk not found")
	}
	if err != nil {
		return nil, err
	}
	return &chunk, nil
}

func feedbackState(row *types.AnswerFeedback, chunkCount int) *types.MessageFeedbackState {
	if row == nil {
		return &types.MessageFeedbackState{Rating: types.AnswerFeedbackNone, ChunkCount: chunkCount}
	}
	return &types.MessageFeedbackState{Rating: row.Rating, Reason: row.Reason, ChunkCount: chunkCount}
}

func feedbackTenantID(ctx context.Context) (uint64, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return 0, apperrors.NewBadRequestError("tenant is required")
	}
	return tenantID, nil
}

func feedbackActor(ctx context.Context) (uint64, string, error) {
	tenantID, err := feedbackTenantID(ctx)
	if err != nil {
		return 0, "", err
	}
	userID, ok := types.UserIDFromContext(ctx)
	if !ok || strings.TrimSpace(userID) == "" {
		return 0, "", apperrors.NewUnauthorizedError("user is required")
	}
	return tenantID, userID, nil
}

func requireFeedbackRating(rating string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(rating)) {
	case types.AnswerFeedbackLike:
		return types.AnswerFeedbackLike, nil
	case types.AnswerFeedbackDislike:
		return types.AnswerFeedbackDislike, nil
	case types.AnswerFeedbackNone:
		return types.AnswerFeedbackNone, nil
	default:
		return "", apperrors.NewBadRequestError("rating must be like, dislike, or none")
	}
}

func normalizeFeedbackPage(page, pageSize int) (int, int) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	return page, pageSize
}

func cloneFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}
