package service

import (
	"context"
	"errors"

	werrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// messageFeedbackService orchestrates answer like/dislike feedback: session
// ownership checks, chunk attribution via message references, per-KB weight
// policies and the statistics surfaces.
type messageFeedbackService struct {
	feedbackRepo interfaces.MessageFeedbackRepository
	messageRepo  interfaces.MessageRepository
	sessionRepo  interfaces.SessionRepository
	kbRepo       interfaces.KnowledgeBaseRepository
	tenantRepo   interfaces.TenantRepository
}

// NewMessageFeedbackService creates a new message feedback service instance.
func NewMessageFeedbackService(
	feedbackRepo interfaces.MessageFeedbackRepository,
	messageRepo interfaces.MessageRepository,
	sessionRepo interfaces.SessionRepository,
	kbRepo interfaces.KnowledgeBaseRepository,
	tenantRepo interfaces.TenantRepository,
) interfaces.MessageFeedbackService {
	return &messageFeedbackService{
		feedbackRepo: feedbackRepo,
		messageRepo:  messageRepo,
		sessionRepo:  sessionRepo,
		kbRepo:       kbRepo,
		tenantRepo:   tenantRepo,
	}
}

// UpsertFeedback validates and applies one rating mutation for the current
// caller. rating "none" cancels an existing rating.
func (s *messageFeedbackService) UpsertFeedback(
	ctx context.Context,
	sessionID string,
	messageID string,
	rating string,
	reasons []string,
	comment string,
) (*types.MessageFeedback, error) {
	if !types.IsValidFeedbackRating(rating) {
		return nil, werrors.NewBadRequestError("invalid rating, must be like, dislike or none")
	}

	tenantID := types.MustTenantIDFromContext(ctx)
	if _, err := s.sessionRepo.Get(ctx, tenantID, sessionUserIDForLookup(ctx), sessionID); err != nil {
		logger.Errorf(ctx, "Failed to get session %s for feedback: %v", sessionID, err)
		return nil, werrors.ErrSessionNotFound
	}

	msg, err := s.messageRepo.GetMessage(ctx, sessionID, messageID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get message %s for feedback: %v", messageID, err)
		return nil, werrors.NewNotFoundError("message not found")
	}
	if msg.Role != "assistant" {
		return nil, werrors.NewBadRequestError("feedback is only supported on assistant messages")
	}

	cleanReasons, err := normalizeFeedbackReasons(rating, reasons)
	if err != nil {
		return nil, err
	}
	if rating != types.FeedbackRatingDislike {
		comment = ""
	}
	comment = truncateFeedbackComment(comment)

	refs, err := s.resolveMessageChunkRefs(ctx, msg)
	if err != nil {
		return nil, err
	}

	feedback := &types.MessageFeedback{
		TenantID:  tenantID,
		SessionID: sessionID,
		MessageID: messageID,
		UserID:    types.SessionOwnerIDFromContext(ctx),
		Rating:    rating,
		Reasons:   cleanReasons,
		Comment:   comment,
	}
	// The repository reads each involved KB's owner tenant and that tenant's
	// retrieval config inside its transaction, so no per-KB policy is threaded
	// from here — this closes the window where a config saved between now and
	// the transaction commit would be applied from a stale snapshot.
	if _, err := s.feedbackRepo.UpsertFeedback(ctx, feedback, refs); err != nil {
		logger.Errorf(ctx, "Failed to upsert feedback for message %s: %v", messageID, err)
		return nil, err
	}
	return feedback, nil
}

// normalizeFeedbackReasons enforces the preset reason whitelist. Reasons only
// accompany dislikes; anything else is dropped. Duplicates are deduped while
// preserving the caller's order.
func normalizeFeedbackReasons(rating string, reasons []string) (types.FeedbackReasons, error) {
	if rating != types.FeedbackRatingDislike || len(reasons) == 0 {
		return types.FeedbackReasons{}, nil
	}
	seen := make(map[string]bool, len(reasons))
	clean := make(types.FeedbackReasons, 0, len(reasons))
	for _, reason := range reasons {
		if !types.FeedbackDislikeReasons[reason] {
			return nil, werrors.NewBadRequestError("unknown dislike reason: " + reason)
		}
		if seen[reason] {
			continue
		}
		seen[reason] = true
		clean = append(clean, reason)
	}
	return clean, nil
}

func truncateFeedbackComment(comment string) string {
	runes := []rune(comment)
	if len(runes) <= types.FeedbackCommentMaxRunes {
		return comment
	}
	return string(runes[:types.FeedbackCommentMaxRunes])
}

// resolveMessageChunkRefs returns the persisted reference rows of a message,
// lazily backfilling them from the message's stored KnowledgeReferences for
// answers created before reference persistence existed.
func (s *messageFeedbackService) resolveMessageChunkRefs(
	ctx context.Context, msg *types.Message,
) ([]types.MessageChunkReference, error) {
	refs, err := s.feedbackRepo.ListChunkRefsByMessage(ctx, msg.ID)
	if err != nil {
		return nil, err
	}
	if len(refs) > 0 {
		return refs, nil
	}
	if err := s.RecordMessageReferences(ctx, msg); err != nil {
		return nil, err
	}
	return s.feedbackRepo.ListChunkRefsByMessage(ctx, msg.ID)
}

// AttachUserFeedback stamps Message.UserFeedback for the current caller on
// loaded assistant messages. Never fatal — a hydration failure should not
// block message load.
func (s *messageFeedbackService) AttachUserFeedback(ctx context.Context, messages []*types.Message) {
	userID := types.SessionOwnerIDFromContext(ctx)
	if userID == "" || len(messages) == 0 {
		return
	}
	messageIDs := make([]string, 0, len(messages))
	for _, msg := range messages {
		if msg != nil && msg.Role == "assistant" && msg.ID != "" {
			messageIDs = append(messageIDs, msg.ID)
		}
	}
	if len(messageIDs) == 0 {
		return
	}
	ratings, err := s.feedbackRepo.ListRatingsByMessageIDs(ctx, userID, messageIDs)
	if err != nil {
		logger.Warnf(ctx, "Failed to attach user feedback: %v", err)
		return
	}
	for _, msg := range messages {
		if msg != nil {
			msg.UserFeedback = ratings[msg.ID]
		}
	}
}

// RecordMessageReferences persists the answer->chunk reference facts of a
// completed assistant message. Idempotent via the (message_id, chunk_id)
// unique index.
func (s *messageFeedbackService) RecordMessageReferences(ctx context.Context, msg *types.Message) error {
	if msg == nil || msg.ID == "" || msg.Role != "assistant" {
		return nil
	}
	refs := extractChunkRefs(ctx, msg)
	if len(refs) == 0 {
		return nil
	}
	return s.feedbackRepo.SyncMessageChunkRefs(ctx, refs)
}

// extractChunkRefs converts a message's stored knowledge references into
// reference rows, skipping web search results and deduplicating chunk ids.
// The merge pipeline folds adjacent/overlapping chunks into one passage and
// records the folded-in chunk ids under SubChunkID; attribution must credit
// every constituent chunk, so the union of ID and SubChunkID is persisted
// (all sharing the passage's knowledge base).
func extractChunkRefs(ctx context.Context, msg *types.Message) []types.MessageChunkReference {
	tenantID := types.MustTenantIDFromContext(ctx)
	seen := make(map[string]bool)
	refs := make([]types.MessageChunkReference, 0, len(msg.KnowledgeReferences))
	add := func(chunkID, kbID string, isSub bool) {
		if chunkID == "" || kbID == "" || seen[chunkID] {
			return
		}
		seen[chunkID] = true
		refs = append(refs, types.MessageChunkReference{
			TenantID:        tenantID,
			MessageID:       msg.ID,
			SessionID:       msg.SessionID,
			ChunkID:         chunkID,
			KnowledgeID:     msg.KnowledgeID,
			KnowledgeBaseID: kbID,
			IsSubChunk:      isSub,
		})
	}
	for _, sr := range msg.KnowledgeReferences {
		if sr == nil || sr.KnowledgeBaseID == "" {
			continue
		}
		if sr.ChunkType == "web_search" || sr.KnowledgeSource == "web_search" {
			continue
		}
		add(sr.ID, sr.KnowledgeBaseID, false)
		for _, subID := range sr.SubChunkID {
			add(subID, sr.KnowledgeBaseID, true)
		}
	}
	return refs
}

// requireOwnedKB loads a KB and enforces that the caller's active tenant owns
// it (system admins bypass). Feedback statistics, logs and reset are
// owner-only management surfaces: they expose and mutate the owner tenant's
// aggregate quality signals (including end-user dislike comments), so shared
// viewers / shared-agent users / shared editors — who pass the KBAccess guards
// on cross-tenant KBs — must not reach them. A cross-tenant caller gets a 404
// rather than a 403 to avoid confirming the KB's existence.
func (s *messageFeedbackService) requireOwnedKB(ctx context.Context, kbID string) (*types.KnowledgeBase, error) {
	kb, err := s.kbRepo.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		return nil, werrors.NewNotFoundError("knowledge base not found")
	}
	if types.IsSystemAdminFromContext(ctx) {
		return kb, nil
	}
	callerTenant, ok := types.TenantIDFromContext(ctx)
	if !ok || callerTenant != kb.TenantID {
		return nil, werrors.NewNotFoundError("knowledge base not found")
	}
	return kb, nil
}

// ListChunkStats returns the paged per-chunk feedback statistics of one KB.
func (s *messageFeedbackService) ListChunkStats(
	ctx context.Context, kbID string, query *interfaces.ChunkFeedbackStatsQuery,
) (*types.PageResult, error) {
	kb, err := s.requireOwnedKB(ctx, kbID)
	if err != nil {
		return nil, err
	}
	if query == nil {
		query = &interfaces.ChunkFeedbackStatsQuery{}
	}
	if query.Pagination == nil {
		query.Pagination = &types.Pagination{}
	}
	stats, total, err := s.feedbackRepo.ListChunkStats(ctx, kb.TenantID, kb.ID, kb.FeedbackResetAt, query)
	if err != nil {
		return nil, err
	}
	return types.NewPageResult(total, query.Pagination, stats), nil
}

// ListWeightLogs returns the paged recall-weight change audit of one KB.
func (s *messageFeedbackService) ListWeightLogs(
	ctx context.Context, kbID string, chunkID string, p *types.Pagination,
) (*types.PageResult, error) {
	kb, err := s.requireOwnedKB(ctx, kbID)
	if err != nil {
		return nil, err
	}
	if p == nil {
		p = &types.Pagination{}
	}
	logs, total, err := s.feedbackRepo.ListWeightLogs(ctx, kb.TenantID, kb.ID, chunkID, p)
	if err != nil {
		return nil, err
	}
	return types.NewPageResult(total, p, logs), nil
}

// ResetKnowledgeBaseFeedback advances the KB feedback epoch and restores
// neutral chunk feedback state.
func (s *messageFeedbackService) ResetKnowledgeBaseFeedback(ctx context.Context, kbID string) (int64, error) {
	kb, err := s.requireOwnedKB(ctx, kbID)
	if err != nil {
		return 0, err
	}
	reset, err := s.feedbackRepo.ResetKnowledgeBaseFeedback(ctx, kb.TenantID, kb.ID)
	if err != nil {
		logger.Errorf(ctx, "Failed to reset feedback for KB %s: %v", kbID, err)
		return 0, err
	}
	logger.Infof(ctx, "Reset feedback for KB %s, %d chunks cleared", kbID, reset)
	return reset, nil
}

// RecomputeTenantFeedbackWeights refreshes all stored feedback weights of a
// tenant after its retrieval config changed. A config fingerprint is checked
// right before commit so a slow recomputation of an older config save aborts
// instead of overwriting the results of a newer one.
func (s *messageFeedbackService) RecomputeTenantFeedbackWeights(
	ctx context.Context, tenantID uint64,
) (int64, error) {
	tenant, err := s.tenantRepo.GetTenantByID(ctx, tenantID)
	if err != nil {
		return 0, err
	}
	fingerprint := types.RetrievalConfigFingerprint(tenant.RetrievalConfig)
	changed, err := s.feedbackRepo.RecomputeFeedbackWeights(
		ctx, tenantID,
		map[string]*types.RetrievalConfig{"": tenant.RetrievalConfig},
		fingerprint,
	)
	if err != nil {
		if errors.Is(err, types.ErrFeedbackRecomputeStale) {
			logger.Infof(ctx, "Feedback weight recompute for tenant %d skipped: config changed again", tenantID)
			return 0, nil
		}
		return 0, err
	}
	logger.Infof(ctx, "Recomputed feedback weights for tenant %d, %d chunks changed", tenantID, changed)
	return changed, nil
}