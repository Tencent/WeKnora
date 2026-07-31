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
	principal, actorTenantID, sourceTenantID, err := requireFeedbackGovernanceAccess(ctx)
	if err != nil {
		return err
	}
	if kbID == "" || chunkID == "" {
		return ErrInvalidFeedback
	}
	return s.repo.ResetChunkFeedback(ctx, types.ResetChunkFeedbackInput{
		ChunkTenantID:   sourceTenantID,
		ActorTenantID:   actorTenantID,
		ActorUserID:     principal.ID,
		KnowledgeBaseID: kbID,
		ChunkID:         chunkID,
	})
}

// requireFeedbackGovernanceAccess distinguishes the authenticated actor tenant
// from the KB/chunk source tenant. KBAccess rewrites TenantIDContextKey for a
// shared resource, while TenantInfo retains the tenant selected by the caller
// during authentication. Governance is intentionally not delegable through a
// shared Viewer or Editor grant.
func requireFeedbackGovernanceAccess(ctx context.Context) (types.Principal, uint64, uint64, error) {
	principal, ok := types.PrincipalFromContext(ctx)
	if !ok || principal.Type != types.PrincipalWebUser || strings.TrimSpace(principal.ID) == "" ||
		!types.TenantRoleFromContext(ctx).HasPermission(types.TenantRoleAdmin) {
		return types.Principal{}, 0, 0, ErrFeedbackForbidden
	}
	actorTenant, ok := types.TenantInfoFromContext(ctx)
	if !ok || actorTenant == nil || actorTenant.ID == 0 {
		return types.Principal{}, 0, 0, ErrFeedbackForbidden
	}
	sourceTenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || sourceTenantID == 0 || actorTenant.ID != sourceTenantID {
		return types.Principal{}, 0, 0, ErrFeedbackForbidden
	}
	return principal, actorTenant.ID, sourceTenantID, nil
}

// requireFeedbackGovernancePrincipal preserves the principal-only check for
// non-KB operations that share the same interactive-admin policy. KB/chunk
// governance must use requireFeedbackGovernanceAccess above.
func requireFeedbackGovernancePrincipal(ctx context.Context) (types.Principal, error) {
	principal, ok := types.PrincipalFromContext(ctx)
	if !ok || principal.Type != types.PrincipalWebUser || strings.TrimSpace(principal.ID) == "" ||
		!types.TenantRoleFromContext(ctx).HasPermission(types.TenantRoleAdmin) {
		return types.Principal{}, ErrFeedbackForbidden
	}
	return principal, nil
}

func (s *feedbackService) GetChunkFeedbackDetails(
	ctx context.Context, chunkID string,
) (*types.ChunkFeedbackDetails, error) {
	_, _, sourceTenantID, err := requireFeedbackGovernanceAccess(ctx)
	if err != nil {
		return nil, err
	}
	if chunkID == "" {
		return nil, ErrInvalidFeedback
	}
	return s.repo.GetChunkFeedbackDetails(ctx, sourceTenantID, chunkID)
}
