package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const maxFeedbackReasonTextRunes = 1000

type messageFeedbackService struct {
	feedbackRepo interfaces.MessageFeedbackRepository
	messageRepo  interfaces.MessageRepository
	sessionRepo  interfaces.SessionRepository
	chunkRepo    interfaces.ChunkRepository
}

func NewMessageFeedbackService(
	feedbackRepo interfaces.MessageFeedbackRepository,
	messageRepo interfaces.MessageRepository,
	sessionRepo interfaces.SessionRepository,
	chunkRepo interfaces.ChunkRepository,
) interfaces.MessageFeedbackService {
	return &messageFeedbackService{
		feedbackRepo: feedbackRepo,
		messageRepo:  messageRepo,
		sessionRepo:  sessionRepo,
		chunkRepo:    chunkRepo,
	}
}

func (s *messageFeedbackService) SaveMessageChunkRefs(
	ctx context.Context,
	sessionTenantID uint64,
	searchTargets types.SearchTargets,
	message *types.Message,
) error {
	refs, err := s.buildMessageChunkRefs(ctx, sessionTenantID, searchTargets, message)
	if err != nil {
		return err
	}
	return s.feedbackRepo.SaveMessageChunkRefs(ctx, refs)
}

func (s *messageFeedbackService) SetMessageFeedback(
	ctx context.Context,
	sessionID string,
	messageID string,
	req types.MessageFeedbackRequest,
) (*types.MessageFeedbackResponse, error) {
	feedbackType := strings.TrimSpace(req.FeedbackType)
	if feedbackType != types.FeedbackTypeLike &&
		feedbackType != types.FeedbackTypeDislike &&
		feedbackType != types.FeedbackTypeNone {
		return nil, apperrors.NewBadRequestError("invalid feedback_type")
	}
	sessionTenantID, ok := sessionTenantIDForLookup(ctx)
	if !ok || sessionTenantID == 0 {
		return nil, errors.New("tenant ID not found in context")
	}
	userID, ok := types.UserIDFromContext(ctx)
	if !ok || userID == "" {
		return nil, apperrors.NewUnauthorizedError("user is required for message feedback")
	}
	if _, err := s.sessionRepo.Get(ctx, sessionTenantID, userID, sessionID); err != nil {
		return nil, err
	}
	message, err := s.messageRepo.GetMessage(ctx, sessionID, messageID)
	if err != nil {
		return nil, err
	}
	if message.Role != "assistant" {
		return nil, apperrors.NewBadRequestError("only assistant messages can be rated")
	}
	if !message.IsCompleted {
		return nil, apperrors.NewBadRequestError("only completed assistant messages can be rated")
	}

	reasonCode := strings.TrimSpace(req.ReasonCode)
	reasonText := strings.TrimSpace(req.ReasonText)
	if feedbackType != types.FeedbackTypeDislike {
		reasonCode = ""
		reasonText = ""
	} else {
		reasonText = truncateRunes(reasonText, maxFeedbackReasonTextRunes)
	}

	now := time.Now().UTC()
	var saved *types.MessageFeedback
	err = s.feedbackRepo.WithTransaction(ctx, func(repo interfaces.MessageFeedbackRepository) error {
		refs, err := repo.GetMessageChunkRefs(ctx, sessionTenantID, messageID)
		if err != nil {
			return err
		}
		if len(refs) == 0 && len(message.KnowledgeReferences) > 0 {
			builtRefs, buildErr := s.buildMessageChunkRefs(ctx, sessionTenantID, nil, message)
			if buildErr != nil {
				return buildErr
			}
			if err := repo.SaveMessageChunkRefs(ctx, builtRefs); err != nil {
				return err
			}
			refs = builtRefs
		}

		feedback := &types.MessageFeedback{
			ID:              uuid.New().String(),
			SessionTenantID: sessionTenantID,
			UserID:          userID,
			SessionID:       sessionID,
			MessageID:       messageID,
			FeedbackType:    feedbackType,
			ReasonCode:      reasonCode,
			ReasonText:      reasonText,
			FeedbackAt:      now,
		}
		if err := repo.UpsertMessageFeedback(ctx, feedback); err != nil {
			return err
		}
		saved = feedback

		seenChunks := make(map[string]*types.MessageChunkRef, len(refs))
		for _, ref := range refs {
			if ref == nil || ref.ChunkID == "" || ref.ChunkTenantID == 0 {
				continue
			}
			seenChunks[fmt.Sprintf("%d:%s", ref.ChunkTenantID, ref.ChunkID)] = ref
		}
		for _, ref := range seenChunks {
			chunk, err := s.chunkRepo.GetChunkByID(ctx, ref.ChunkTenantID, ref.ChunkID)
			if err != nil {
				if isChunkNotFoundErr(err) {
					continue
				}
				return err
			}
			oldWeight := chunk.RecallWeight
			if oldWeight == 0 {
				oldWeight = 1.0
			}
			stats, err := repo.RecalculateChunkFeedback(ctx, ref.ChunkTenantID, ref.ChunkID)
			if err != nil {
				return err
			}
			if !floatEqual(oldWeight, stats.RecallWeight) {
				if err := repo.CreateChunkWeightLog(ctx, &types.ChunkWeightLog{
					ChunkTenantID:    ref.ChunkTenantID,
					ChunkID:          ref.ChunkID,
					OldWeight:        oldWeight,
					NewWeight:        stats.RecallWeight,
					Source:           types.ChunkWeightLogSourceFeedback,
					SourceMessageID:  messageID,
					SourceFeedbackID: feedback.ID,
					Reason:           fmt.Sprintf("feedback_type=%s", feedbackType),
				}); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	resp := &types.MessageFeedbackResponse{FeedbackType: feedbackType}
	if saved != nil && feedbackType == types.FeedbackTypeDislike {
		resp.ReasonCode = saved.ReasonCode
		resp.ReasonText = saved.ReasonText
	}
	return resp, nil
}

func (s *messageFeedbackService) AttachFeedbackToMessages(
	ctx context.Context,
	sessionTenantID uint64,
	userID string,
	messages []*types.Message,
) error {
	if sessionTenantID == 0 || userID == "" || len(messages) == 0 {
		return nil
	}
	ids := make([]string, 0, len(messages))
	for _, message := range messages {
		if message != nil && message.Role == "assistant" && message.ID != "" {
			ids = append(ids, message.ID)
		}
	}
	feedbacks, err := s.feedbackRepo.GetFeedbacksByMessageIDs(ctx, sessionTenantID, userID, ids)
	if err != nil {
		return err
	}
	byMessage := make(map[string]*types.MessageFeedback, len(feedbacks))
	for _, feedback := range feedbacks {
		byMessage[feedback.MessageID] = feedback
	}
	for _, message := range messages {
		if message == nil {
			continue
		}
		if feedback, ok := byMessage[message.ID]; ok {
			message.FeedbackType = feedback.FeedbackType
			if feedback.FeedbackType == types.FeedbackTypeDislike {
				message.FeedbackReasonCode = feedback.ReasonCode
				message.FeedbackReasonText = feedback.ReasonText
			}
		}
	}
	return nil
}

func (s *messageFeedbackService) GetChunkFeedbackStats(
	ctx context.Context,
	chunkID string,
) (*types.ChunkFeedbackStats, error) {
	chunk, err := s.chunkRepo.GetChunkByIDOnly(ctx, chunkID)
	if err != nil {
		if isChunkNotFoundErr(err) {
			return nil, ErrChunkNotFound
		}
		return nil, err
	}
	return s.feedbackRepo.GetChunkFeedbackStats(ctx, chunk.TenantID, chunk.ID)
}

func (s *messageFeedbackService) GetChunkWeightLogs(
	ctx context.Context,
	chunkID string,
	limit int,
) ([]*types.ChunkWeightLog, error) {
	chunk, err := s.chunkRepo.GetChunkByIDOnly(ctx, chunkID)
	if err != nil {
		if isChunkNotFoundErr(err) {
			return nil, ErrChunkNotFound
		}
		return nil, err
	}
	return s.feedbackRepo.GetChunkWeightLogs(ctx, chunk.TenantID, chunk.ID, limit)
}

func (s *messageFeedbackService) ResetChunkFeedback(
	ctx context.Context,
	chunkID string,
) (*types.ChunkFeedbackStats, error) {
	chunk, err := s.chunkRepo.GetChunkByIDOnly(ctx, chunkID)
	if err != nil {
		if isChunkNotFoundErr(err) {
			return nil, ErrChunkNotFound
		}
		return nil, err
	}
	oldWeight := chunk.RecallWeight
	if oldWeight == 0 {
		oldWeight = 1.0
	}
	resetAt := time.Now().UTC()
	err = s.feedbackRepo.WithTransaction(ctx, func(repo interfaces.MessageFeedbackRepository) error {
		if err := repo.ResetChunkFeedback(ctx, chunk.TenantID, chunk.ID, resetAt); err != nil {
			return err
		}
		return repo.CreateChunkWeightLog(ctx, &types.ChunkWeightLog{
			ChunkTenantID: chunk.TenantID,
			ChunkID:       chunk.ID,
			OldWeight:     oldWeight,
			NewWeight:     1.0,
			Source:        types.ChunkWeightLogSourceAdminReset,
			Reason:        "admin reset feedback aggregates",
		})
	})
	if err != nil {
		return nil, err
	}
	return s.feedbackRepo.GetChunkFeedbackStats(ctx, chunk.TenantID, chunk.ID)
}

func (s *messageFeedbackService) buildMessageChunkRefs(
	ctx context.Context,
	sessionTenantID uint64,
	searchTargets types.SearchTargets,
	message *types.Message,
) ([]*types.MessageChunkRef, error) {
	if message == nil || !message.IsCompleted || len(message.KnowledgeReferences) == 0 {
		return nil, nil
	}

	tenantByKB := searchTargets.GetKBTenantMap()
	needChunkTenant := make([]string, 0)
	seenNeedChunkTenant := make(map[string]struct{})
	seen := make(map[string]struct{}, len(message.KnowledgeReferences))
	for _, ref := range message.KnowledgeReferences {
		if !isAttributableReference(ref) {
			continue
		}
		chunkTenantID := tenantByKB[ref.KnowledgeBaseID]
		if chunkTenantID == 0 {
			if _, ok := seenNeedChunkTenant[ref.ID]; !ok {
				needChunkTenant = append(needChunkTenant, ref.ID)
				seenNeedChunkTenant[ref.ID] = struct{}{}
			}
		}
	}
	if len(needChunkTenant) > 0 {
		chunks, err := s.chunkRepo.ListChunksByIDOnly(ctx, needChunkTenant)
		if err != nil {
			return nil, err
		}
		for _, chunk := range chunks {
			if chunk != nil {
				tenantByKB[chunk.KnowledgeBaseID] = chunk.TenantID
			}
		}
	}

	refs := make([]*types.MessageChunkRef, 0, len(message.KnowledgeReferences))
	for _, ref := range message.KnowledgeReferences {
		if !isAttributableReference(ref) {
			continue
		}
		if _, ok := seen[ref.ID]; ok {
			continue
		}
		seen[ref.ID] = struct{}{}
		chunkTenantID := tenantByKB[ref.KnowledgeBaseID]
		if chunkTenantID == 0 {
			chunk, err := s.chunkRepo.GetChunkByIDOnly(ctx, ref.ID)
			if err != nil {
				logger.Warnf(ctx, "skip message chunk ref: cannot resolve chunk tenant chunk_id=%s err=%v", ref.ID, err)
				continue
			}
			chunkTenantID = chunk.TenantID
		}
		if chunkTenantID == 0 {
			continue
		}
		refs = append(refs, &types.MessageChunkRef{
			ID:              uuid.New().String(),
			SessionTenantID: sessionTenantID,
			ChunkTenantID:   chunkTenantID,
			SessionID:       message.SessionID,
			MessageID:       message.ID,
			ChunkID:         ref.ID,
			KnowledgeBaseID: ref.KnowledgeBaseID,
			KnowledgeID:     ref.KnowledgeID,
			ChunkIndex:      ref.ChunkIndex,
			ChunkType:       ref.ChunkType,
			MatchType:       ref.MatchType,
			Score:           ref.Score,
		})
	}
	return refs, nil
}

func isAttributableReference(ref *types.SearchResult) bool {
	if ref == nil || ref.ID == "" {
		return false
	}
	if ref.MatchType == types.MatchTypeHistory || ref.MatchType == types.MatchTypeWebSearch {
		return false
	}
	if strings.EqualFold(ref.KnowledgeSource, "web_search") {
		return false
	}
	return true
}

func truncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes])
}

func floatEqual(a, b float64) bool {
	return math.Abs(a-b) < 0.0000001
}

func isChunkNotFoundErr(err error) bool {
	return errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, ErrChunkNotFound) || err.Error() == ErrChunkNotFound.Error()
}
