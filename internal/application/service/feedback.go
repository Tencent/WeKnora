package service

import (
	"context"
	"errors"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/feedbackweight"
	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/config"
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
	// ErrFeedbackDisabled indicates that the global collection switch is off.
	ErrFeedbackDisabled = errors.New("feedback is disabled")
)

type feedbackService struct {
	repo   interfaces.FeedbackRepository
	config *config.FeedbackConfig
}

// NewFeedbackService creates the feedback application service.
func NewFeedbackService(repo interfaces.FeedbackRepository, cfg *config.Config) interfaces.FeedbackService {
	var feedbackConfig *config.FeedbackConfig
	if cfg != nil {
		feedbackConfig = cfg.Feedback
	}
	return &feedbackService{repo: repo, config: feedbackConfig}
}

func (s *feedbackService) collectionEnabled() bool {
	return config.FeedbackCollectionEnabled(s.config)
}

func (s *feedbackService) ApplyMessageFeedback(
	ctx context.Context,
	sessionID, messageID string,
	feedbackType types.FeedbackType,
	reason *types.FeedbackReasonCode,
) (*types.MessageFeedbackState, error) {
	if !s.collectionEnabled() {
		return nil, ErrFeedbackDisabled
	}
	principal, ok := types.PrincipalFromContext(ctx)
	if !ok || principal.Type != types.PrincipalWebUser || strings.TrimSpace(principal.ID) == "" {
		return nil, ErrFeedbackForbidden
	}
	if sessionID == "" || messageID == "" {
		return nil, ErrInvalidFeedback
	}
	switch feedbackType {
	case types.FeedbackTypeNone:
		if reason != nil {
			return nil, ErrInvalidFeedback
		}
	case types.FeedbackTypeLike:
		if reason != nil {
			return nil, ErrInvalidFeedback
		}
	case types.FeedbackTypeDislike:
		if reason == nil || !validFeedbackReason(*reason) {
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
	if !s.collectionEnabled() {
		return ErrFeedbackDisabled
	}
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
	if !s.collectionEnabled() {
		return nil, ErrFeedbackDisabled
	}
	if chunkID == "" {
		return nil, ErrInvalidFeedback
	}
	details, err := s.repo.GetChunkFeedbackDetails(ctx, types.MustTenantIDFromContext(ctx), chunkID)
	if err != nil {
		return nil, err
	}
	weight, _, err := feedbackweight.EffectiveWeight(
		s.config,
		details.LikeCount,
		details.DislikeCount,
	)
	if err != nil {
		weight = 1
	}
	details.EffectiveRecallWeight = weight
	details.NeedsOptimization = feedbackNeedsOptimization(s.config, details.LikeCount, details.DislikeCount)
	return details, nil
}

func (s *feedbackService) ListChunkFeedback(
	ctx context.Context,
	kbID string,
	query *types.ChunkFeedbackListQuery,
) (*types.PageResult, error) {
	if !s.collectionEnabled() {
		return nil, ErrFeedbackDisabled
	}
	if err := requireFeedbackGovernancePrincipal(ctx); err != nil {
		return nil, err
	}
	if strings.TrimSpace(kbID) == "" || query == nil {
		return nil, ErrInvalidFeedback
	}
	if err := query.Validate(); err != nil {
		return nil, ErrInvalidFeedback
	}
	items, total, err := s.repo.ListChunkFeedbackGovernance(
		ctx,
		types.MustTenantIDFromContext(ctx),
		strings.TrimSpace(kbID),
		query,
	)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if item == nil {
			continue
		}
		weight, _, calcErr := feedbackweight.EffectiveWeight(
			s.config,
			item.LikeCount,
			item.DislikeCount,
		)
		if calcErr != nil {
			weight = 1
		}
		item.EffectiveRecallWeight = weight
		item.NeedsOptimization = feedbackNeedsOptimization(s.config, item.LikeCount, item.DislikeCount)
	}
	return types.NewPageResult(total, query.Pagination(), items), nil
}

func (s *feedbackService) GetChunkFeedbackGovernanceDetails(
	ctx context.Context,
	kbID, chunkID string,
) (*types.ChunkFeedbackDetails, error) {
	if !s.collectionEnabled() {
		return nil, ErrFeedbackDisabled
	}
	if err := requireFeedbackGovernancePrincipal(ctx); err != nil {
		return nil, err
	}
	if strings.TrimSpace(kbID) == "" || strings.TrimSpace(chunkID) == "" {
		return nil, ErrInvalidFeedback
	}
	details, err := s.GetChunkFeedbackDetails(ctx, strings.TrimSpace(chunkID))
	if err != nil {
		return nil, err
	}
	if details.KnowledgeBaseID != strings.TrimSpace(kbID) {
		return nil, repository.ErrFeedbackChunkNotFound
	}
	return details, nil
}

func (s *feedbackService) ListChunkFeedbackHistory(
	ctx context.Context,
	kbID, chunkID string,
	page *types.Pagination,
) (*types.PageResult, error) {
	if !s.collectionEnabled() {
		return nil, ErrFeedbackDisabled
	}
	if err := requireFeedbackGovernancePrincipal(ctx); err != nil {
		return nil, err
	}
	if strings.TrimSpace(kbID) == "" || strings.TrimSpace(chunkID) == "" {
		return nil, ErrInvalidFeedback
	}
	if page == nil {
		page = &types.Pagination{Page: 1, PageSize: 20}
	}
	page.Page = page.GetPage()
	page.PageSize = page.GetPageSize()
	if page.PageSize > 100 {
		return nil, ErrInvalidFeedback
	}
	audits, total, err := s.repo.ListChunkFeedbackHistory(
		ctx,
		types.MustTenantIDFromContext(ctx),
		strings.TrimSpace(kbID),
		strings.TrimSpace(chunkID),
		page,
	)
	if err != nil {
		return nil, err
	}
	return types.NewPageResult(total, page, audits), nil
}

func requireFeedbackGovernancePrincipal(ctx context.Context) error {
	principal, ok := types.PrincipalFromContext(ctx)
	if !ok || principal.Type != types.PrincipalWebUser || strings.TrimSpace(principal.ID) == "" {
		return ErrFeedbackForbidden
	}
	return nil
}

func feedbackNeedsOptimization(policy *config.FeedbackConfig, likes, dislikes int64) bool {
	total := likes + dislikes
	if total <= 0 || total < likes || total < dislikes {
		return false
	}
	threshold := config.DefaultFeedbackConfig().EffectiveOptimizationThreshold()
	if policy != nil {
		threshold = policy.EffectiveOptimizationThreshold()
	}
	return float64(likes)/float64(total) < threshold
}
