package service

import (
	"context"
	"errors"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/config"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// AnswerChunkRecorder persists the chunks an assistant reply cited.
type AnswerChunkRecorder interface {
	RecordAnswerChunks(ctx context.Context, message *types.Message) error
}

// AttachAnswerChunkRecorder wires citation persistence onto the message
// service after both objects exist, so neither constructor depends on the other.
func AttachAnswerChunkRecorder(svc interfaces.MessageService, recorder AnswerChunkRecorder) {
	concrete, ok := svc.(*messageService)
	if !ok || concrete == nil {
		return
	}
	concrete.answerChunks = recorder
}

type messageFeedbackService struct {
	repo     *repository.MessageFeedbackRepository
	chunks   interfaces.ChunkRepository
	messages interfaces.MessageService
	policy   types.FeedbackPolicy
}

// NewMessageFeedbackService builds the feedback service from the loaded config.
func NewMessageFeedbackService(
	repo *repository.MessageFeedbackRepository,
	chunks interfaces.ChunkRepository,
	messages interfaces.MessageService,
	cfg *config.Config,
) interfaces.MessageFeedbackService {
	return &messageFeedbackService{
		repo:     repo,
		chunks:   chunks,
		messages: messages,
		policy:   policyFromConfig(cfg),
	}
}

func policyFromConfig(cfg *config.Config) types.FeedbackPolicy {
	policy := types.DefaultFeedbackPolicy()
	if cfg == nil || cfg.MessageFeedback == nil {
		return policy.Normalize()
	}
	mf := cfg.MessageFeedback
	policy.MinFeedbackCount = mf.MinFeedbackCount
	policy.BoostPositiveRateThreshold = mf.BoostPositiveRateThreshold
	policy.NeutralPositiveRateThreshold = mf.NeutralPositiveRateThreshold
	policy.NeedsOptimizationRateThreshold = mf.NeedsOptimizationRateThreshold
	policy.BoostRecallWeight = mf.BoostRecallWeight
	policy.NeutralRecallWeight = mf.NeutralRecallWeight
	policy.PenaltyRecallWeight = mf.PenaltyRecallWeight
	return policy.Normalize()
}

func (s *messageFeedbackService) RecordAnswerChunks(ctx context.Context, message *types.Message) error {
	if s == nil || s.repo == nil || message == nil || message.ID == "" || message.SessionID == "" {
		return nil
	}
	if message.Role != "assistant" || len(message.KnowledgeReferences) == 0 {
		return nil
	}
	tenantID, ok := sessionTenantIDForLookup(ctx)
	if !ok || tenantID == 0 {
		logger.Warnf(ctx, "skip answer chunk refs for %s: tenant missing", message.ID)
		return nil
	}
	candidates := attributableRefs(message)
	if len(candidates) == 0 {
		return nil
	}
	ids := make([]string, 0, len(candidates))
	for _, ref := range candidates {
		ids = append(ids, ref.ID)
	}
	stored, err := s.chunks.ListChunksByIDOnly(ctx, ids)
	if err != nil {
		return err
	}
	byID := make(map[string]*types.Chunk, len(stored))
	for _, chunk := range stored {
		if chunk != nil && chunk.ID != "" {
			byID[chunk.ID] = chunk
		}
	}
	refs := make([]*types.MessageChunkRef, 0, len(candidates))
	for _, ref := range candidates {
		chunk := byID[ref.ID]
		if chunk == nil {
			continue
		}
		refs = append(refs, &types.MessageChunkRef{
			SessionTenantID: tenantID,
			ChunkTenantID:   chunk.TenantID,
			SessionID:       message.SessionID,
			MessageID:       message.ID,
			ChunkID:         chunk.ID,
			KnowledgeBaseID: firstNonEmpty(chunk.KnowledgeBaseID, ref.KnowledgeBaseID),
			KnowledgeID:     firstNonEmpty(chunk.KnowledgeID, ref.KnowledgeID),
			ChunkIndex:      chunk.ChunkIndex,
			ChunkType:       string(chunk.ChunkType),
			MatchType:       ref.MatchType,
			Score:           ref.Score,
		})
	}
	if len(refs) == 0 {
		return nil
	}
	if err := s.repo.UpsertChunkRefs(ctx, refs); err != nil {
		return err
	}
	return s.repo.RecomputeMessageChunks(ctx, tenantID, message.ID, s.policy)
}

func (s *messageFeedbackService) Submit(
	ctx context.Context, sessionID, messageID string, req *types.MessageFeedbackRequest,
) (*types.MessageFeedbackView, error) {
	if err := types.NormalizeFeedbackRequest(req); err != nil {
		return nil, apperrors.NewBadRequestError("invalid feedback")
	}
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(messageID) == "" {
		return nil, apperrors.NewBadRequestError("session_id and message_id are required")
	}
	userID := feedbackActorID(ctx)
	if userID == "" {
		return nil, apperrors.NewBadRequestError("user is required")
	}
	message, err := s.messages.GetMessage(ctx, sessionID, messageID)
	if err != nil {
		if errors.Is(err, apperrors.ErrSessionNotFound) || errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperrors.NewNotFoundError("message not found")
		}
		return nil, err
	}
	if message == nil || message.Role != "assistant" {
		return nil, apperrors.NewBadRequestError("feedback is only available on assistant replies")
	}
	tenantID, ok := sessionTenantIDForLookup(ctx)
	if !ok || tenantID == 0 {
		return nil, apperrors.NewBadRequestError("tenant is required")
	}
	current, err := s.repo.GetFeedback(ctx, tenantID, userID, messageID)
	if err != nil {
		return nil, err
	}
	if current != nil && current.FeedbackType == req.FeedbackType &&
		current.ReasonCode == req.ReasonCode && current.ReasonText == req.ReasonText {
		return current.View(), nil
	}
	row := &types.MessageFeedback{
		SessionTenantID: tenantID,
		UserID:          userID,
		SessionID:       sessionID,
		MessageID:       messageID,
		FeedbackType:    req.FeedbackType,
		ReasonCode:      req.ReasonCode,
		ReasonText:      req.ReasonText,
	}
	saved, err := s.repo.ApplyVote(ctx, row, s.policy, req.FeedbackType)
	if err != nil {
		return nil, err
	}
	return saved.View(), nil
}

func (s *messageFeedbackService) Attach(ctx context.Context, messages []*types.Message) {
	if s == nil || s.repo == nil || len(messages) == 0 {
		return
	}
	userID := feedbackActorID(ctx)
	tenantID, ok := sessionTenantIDForLookup(ctx)
	if userID == "" || !ok || tenantID == 0 {
		return
	}
	ids := make([]string, 0, len(messages))
	for _, message := range messages {
		if message == nil || message.Role != "assistant" || message.ID == "" {
			continue
		}
		ids = append(ids, message.ID)
	}
	if len(ids) == 0 {
		return
	}
	rows, err := s.repo.ListFeedbackForMessages(ctx, tenantID, userID, ids)
	if err != nil {
		logger.Warnf(ctx, "load message feedback: %v", err)
		return
	}
	byMessage := make(map[string]*types.MessageFeedback, len(rows))
	for _, row := range rows {
		if row != nil {
			byMessage[row.MessageID] = row
		}
	}
	for _, message := range messages {
		if message == nil || message.Role != "assistant" {
			continue
		}
		if row := byMessage[message.ID]; row != nil {
			message.Feedback = row.View()
			continue
		}
		message.Feedback = &types.MessageFeedbackView{FeedbackType: types.FeedbackTypeNone}
	}
}

func (s *messageFeedbackService) ChunkStats(ctx context.Context, chunkID string) (*types.ChunkFeedbackStats, error) {
	tenantID, err := requireTenant(ctx)
	if err != nil {
		return nil, err
	}
	stats, err := s.repo.ChunkStats(ctx, tenantID, chunkID)
	if errors.Is(err, repository.ErrChunkNotFound) {
		return nil, apperrors.NewNotFoundError("chunk not found")
	}
	return stats, err
}

func (s *messageFeedbackService) ListWeightLogs(
	ctx context.Context, chunkID string, limit int,
) ([]*types.ChunkWeightLog, error) {
	tenantID, err := requireTenant(ctx)
	if err != nil {
		return nil, err
	}
	return s.repo.ListWeightLogs(ctx, tenantID, chunkID, limit)
}

func (s *messageFeedbackService) ResetChunk(ctx context.Context, chunkID string) (*types.Chunk, error) {
	tenantID, err := requireTenant(ctx)
	if err != nil {
		return nil, err
	}
	chunk, err := s.repo.ResetChunk(ctx, tenantID, chunkID, s.policy)
	if errors.Is(err, repository.ErrChunkNotFound) {
		return nil, apperrors.NewNotFoundError("chunk not found")
	}
	return chunk, err
}

func (s *messageFeedbackService) ListKnowledgeChunks(
	ctx context.Context,
	knowledgeID string,
	page *types.Pagination,
	chunkTypes []types.ChunkType,
	filter types.ChunkFeedbackFilter,
) (*types.PageResult, error) {
	tenantID, err := requireTenant(ctx)
	if err != nil {
		return nil, err
	}
	if len(chunkTypes) == 0 {
		chunkTypes = []types.ChunkType{types.ChunkTypeText}
	}
	chunks, total, err := s.repo.ListChunksByFeedback(ctx, tenantID, knowledgeID, page, chunkTypes, filter)
	if err != nil {
		return nil, err
	}
	return types.NewPageResult(total, page, chunks), nil
}

func attributableRefs(message *types.Message) []*types.SearchResult {
	seen := map[string]struct{}{}
	out := make([]*types.SearchResult, 0, len(message.KnowledgeReferences))
	for _, ref := range message.KnowledgeReferences {
		if !types.AttributableKnowledgeRef(ref) {
			continue
		}
		if _, ok := seen[ref.ID]; ok {
			continue
		}
		seen[ref.ID] = struct{}{}
		out = append(out, ref)
	}
	return out
}

func feedbackActorID(ctx context.Context) string {
	if principal, ok := types.PrincipalFromContext(ctx); ok {
		if id := principal.StorageID(); id != "" {
			return id
		}
	}
	return strings.TrimSpace(types.CallerFromContext(ctx).UserID)
}

func requireTenant(ctx context.Context) (uint64, error) {
	tenantID, ok := sessionTenantIDForLookup(ctx)
	if !ok || tenantID == 0 {
		return 0, apperrors.NewBadRequestError("tenant is required")
	}
	return tenantID, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
