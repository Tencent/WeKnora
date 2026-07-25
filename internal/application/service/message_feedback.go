package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

var (
	ErrInvalidMessageFeedback = errors.New("invalid message feedback")
	ErrFeedbackNotAvailable   = errors.New("feedback is only available for completed assistant answers with knowledge references")
)

var supportedDislikeReasons = map[string]struct{}{
	"inaccurate": {},
	"outdated":   {},
	"incomplete": {},
	"irrelevant": {},
	"other":      {},
}

type messageFeedbackService struct {
	repo           interfaces.MessageFeedbackRepository
	messageService interfaces.MessageService
}

func NewMessageFeedbackService(
	repo interfaces.MessageFeedbackRepository,
	messageService interfaces.MessageService,
) interfaces.MessageFeedbackService {
	return &messageFeedbackService{repo: repo, messageService: messageService}
}

func (s *messageFeedbackService) Submit(
	ctx context.Context,
	sessionID string,
	messageID string,
	rating int,
	reasonCode string,
	reasonDetail string,
) (*types.MessageFeedbackView, error) {
	reasonCode = strings.TrimSpace(reasonCode)
	reasonDetail = strings.TrimSpace(reasonDetail)
	if rating != types.MessageFeedbackLike && rating != types.MessageFeedbackDislike {
		return nil, fmt.Errorf("%w: rating must be 1 or -1", ErrInvalidMessageFeedback)
	}
	if utf8.RuneCountInString(reasonDetail) > 500 {
		return nil, fmt.Errorf("%w: reason_detail exceeds 500 characters", ErrInvalidMessageFeedback)
	}
	if rating == types.MessageFeedbackDislike {
		if _, ok := supportedDislikeReasons[reasonCode]; !ok {
			return nil, fmt.Errorf("%w: unsupported reason_code", ErrInvalidMessageFeedback)
		}
		if reasonCode == "other" && reasonDetail == "" {
			return nil, fmt.Errorf("%w: reason_detail is required for other", ErrInvalidMessageFeedback)
		}
	} else {
		reasonCode = ""
		reasonDetail = ""
	}

	message, err := s.messageService.GetMessage(ctx, sessionID, messageID)
	if err != nil {
		return nil, err
	}
	if message.Role != "assistant" || !message.IsCompleted || len(message.KnowledgeReferences) == 0 {
		return nil, ErrFeedbackNotAvailable
	}
	tenantID := types.MustTenantIDFromContext(ctx)
	actorID := types.SessionOwnerIDFromContext(ctx)
	if actorID == "" {
		return nil, fmt.Errorf("%w: actor is missing", ErrInvalidMessageFeedback)
	}
	if err := s.repo.SyncMessageChunkReferences(ctx, tenantID, message); err != nil {
		return nil, err
	}
	return s.repo.ApplyFeedback(ctx, interfaces.MessageFeedbackMutation{
		TenantID:     tenantID,
		ActorID:      actorID,
		Message:      message,
		Rating:       rating,
		ReasonCode:   reasonCode,
		ReasonDetail: reasonDetail,
	})
}

func (s *messageFeedbackService) Cancel(
	ctx context.Context,
	sessionID string,
	messageID string,
) error {
	message, err := s.messageService.GetMessage(ctx, sessionID, messageID)
	if err != nil {
		return err
	}
	if message.Role != "assistant" {
		return ErrFeedbackNotAvailable
	}
	actorID := types.SessionOwnerIDFromContext(ctx)
	if actorID == "" {
		return fmt.Errorf("%w: actor is missing", ErrInvalidMessageFeedback)
	}
	return s.repo.CancelFeedback(
		ctx,
		types.MustTenantIDFromContext(ctx),
		actorID,
		message,
	)
}

func (s *messageFeedbackService) ListChunkStats(
	ctx context.Context,
	knowledgeBaseID string,
	filter types.ChunkFeedbackStatsFilter,
) (*types.ChunkFeedbackStatsPage, error) {
	return s.repo.ListChunkStats(
		ctx,
		types.MustTenantIDFromContext(ctx),
		knowledgeBaseID,
		filter,
	)
}

func (s *messageFeedbackService) ListWeightLogs(
	ctx context.Context,
	knowledgeBaseID string,
	chunkID string,
	limit int,
) ([]*types.ChunkFeedbackWeightLog, error) {
	return s.repo.ListWeightLogs(
		ctx,
		types.MustTenantIDFromContext(ctx),
		knowledgeBaseID,
		chunkID,
		limit,
	)
}

func (s *messageFeedbackService) ResetChunk(
	ctx context.Context,
	knowledgeBaseID string,
	chunkID string,
) error {
	actorID := types.SessionOwnerIDFromContext(ctx)
	return s.repo.ResetChunk(
		ctx,
		types.MustTenantIDFromContext(ctx),
		knowledgeBaseID,
		chunkID,
		actorID,
	)
}
