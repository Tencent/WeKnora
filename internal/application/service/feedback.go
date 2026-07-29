package service

import (
	"context"
	"errors"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

var (
	// ErrInvalidFeedback indicates that the feedback request is malformed.
	ErrInvalidFeedback = errors.New("invalid feedback")
	// ErrFeedbackForbidden indicates that the principal cannot submit feedback.
	ErrFeedbackForbidden = errors.New("feedback is only available to signed-in web users")
	// ErrFeedbackNotEligible indicates that the message has no attributable chunks.
	ErrFeedbackNotEligible = repository.ErrFeedbackNotEligible
	// ErrFeedbackNotFound indicates that the requested message does not exist.
	ErrFeedbackNotFound = repository.ErrFeedbackMessageNotFound
)

type feedbackService struct {
	repo interfaces.FeedbackRepository
}

// NewFeedbackService creates the feedback application service.
func NewFeedbackService(repo interfaces.FeedbackRepository) interfaces.FeedbackService {
	return &feedbackService{repo: repo}
}

func (s *feedbackService) ApplyMessageFeedback(
	ctx context.Context,
	sessionID, messageID string,
	feedbackType types.FeedbackType,
	reason *types.FeedbackReasonCode,
) (*types.MessageFeedbackState, error) {
	principal, ok := types.PrincipalFromContext(ctx)
	if !ok || principal.Type != types.PrincipalWebUser || strings.TrimSpace(principal.ID) == "" {
		return nil, ErrFeedbackForbidden
	}
	if sessionID == "" || messageID == "" {
		return nil, ErrInvalidFeedback
	}
	switch feedbackType {
	case types.FeedbackTypeNone:
		reason = nil
	case types.FeedbackTypeLike:
		if reason != nil {
			return nil, ErrInvalidFeedback
		}
	case types.FeedbackTypeDislike:
		if reason != nil && !validFeedbackReason(*reason) {
			return nil, ErrInvalidFeedback
		}
	default:
		return nil, ErrInvalidFeedback
	}
	tenantID := types.MustTenantIDFromContext(ctx)
	return s.repo.ApplyMessageFeedback(ctx, types.ApplyMessageFeedbackInput{
		MessageTenantID: tenantID,
		ActorTenantID:   tenantID,
		ActorUserID:     principal.ID,
		SessionID:       sessionID,
		MessageID:       messageID,
		Type:            feedbackType,
		ReasonCode:      reason,
	})
}

func validFeedbackReason(reason types.FeedbackReasonCode) bool {
	switch reason {
	case types.FeedbackReasonInaccurate, types.FeedbackReasonIrrelevant,
		types.FeedbackReasonIncomplete, types.FeedbackReasonOutdated,
		types.FeedbackReasonOther:
		return true
	default:
		return false
	}
}

func (s *feedbackService) ResetChunkFeedback(ctx context.Context, kbID, chunkID string) error {
	principal, ok := types.PrincipalFromContext(ctx)
	if !ok || principal.Type != types.PrincipalWebUser || principal.ID == "" {
		return ErrFeedbackForbidden
	}
	if kbID == "" || chunkID == "" {
		return ErrInvalidFeedback
	}
	chunkTenantID := types.MustTenantIDFromContext(ctx)
	actorTenantID := chunkTenantID
	if tenant, ok := types.TenantInfoFromContext(ctx); ok && tenant != nil && tenant.ID != 0 {
		actorTenantID = tenant.ID
	}
	return s.repo.ResetChunkFeedback(ctx, types.ResetChunkFeedbackInput{
		ChunkTenantID:   chunkTenantID,
		ActorTenantID:   actorTenantID,
		ActorUserID:     principal.ID,
		KnowledgeBaseID: kbID,
		ChunkID:         chunkID,
	})
}

func (s *feedbackService) GetChunkFeedbackDetails(
	ctx context.Context, chunkID string,
) (*types.ChunkFeedbackDetails, error) {
	if chunkID == "" {
		return nil, ErrInvalidFeedback
	}
	return s.repo.GetChunkFeedbackDetails(ctx, types.MustTenantIDFromContext(ctx), chunkID)
}
