package service

import (
	"context"
	"encoding/json"
	"errors"

	werrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const feedbackCommentMaxRunes = 500

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

// NewMessageFeedbackService creates a new message feedback service instance
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
	if rating != types.FeedbackRatingLike &&
		rating != types.FeedbackRatingDislike &&
		rating != types.FeedbackRatingNone {
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
	kbPolicies, err := s.buildKBPolicies(ctx, refs)
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
	if _, err := s.feedbackRepo.UpsertFeedback(ctx, feedback, refs, kbPolicies); err != nil {
		logger.Errorf(ctx, "Failed to upsert feedback for message %s: %v", messageID, err)
		return nil, err
	}
	return feedback, nil
}

// normalizeFeedbackReasons enforces the preset reason whitelist. Reasons only
// accompany dislikes; anything else is dropped.
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
	if len(runes) <= feedbackCommentMaxRunes {
		return comment
	}
	return string(runes[:feedbackCommentMaxRunes])
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

// buildKBPolicies loads the owner tenant and its retrieval config for each
// involved knowledge base. The feedback epoch is deliberately re-read inside
// the repository transaction, not here.
func (s *messageFeedbackService) buildKBPolicies(
	ctx context.Context, refs []types.MessageChunkReference,
) (map[string]types.FeedbackKBPolicy, error) {
	policies := make(map[string]types.FeedbackKBPolicy)
	if len(refs) == 0 {
		return policies, nil
	}
	kbIDSet := make(map[string]bool)
	for _, ref := range refs {
		if ref.KnowledgeBaseID != "" {
			kbIDSet[ref.KnowledgeBaseID] = true
		}
	}
	kbIDs := make([]string, 0, len(kbIDSet))
	for id := range kbIDSet {
		kbIDs = append(kbIDs, id)
	}
	kbs, err := s.kbRepo.GetKnowledgeBaseByIDs(ctx, kbIDs)
	if err != nil {
		return nil, err
	}
	configByTenant := make(map[uint64]*types.RetrievalConfig)
	for _, kb := range kbs {
		cfg, ok := configByTenant[kb.TenantID]
		if !ok {
			tenant, err := s.tenantRepo.GetTenantByID(ctx, kb.TenantID)
			if err != nil {
				logger.Warnf(ctx, "Failed to load tenant %d for feedback policy, using defaults: %v", kb.TenantID, err)
			} else {
				cfg = tenant.RetrievalConfig
			}
			configByTenant[kb.TenantID] = cfg
		}
		policies[kb.ID] = types.FeedbackKBPolicy{
			OwnerTenantID: kb.TenantID,
			Config:        cfg,
		}
	}
	return policies, nil
}

// AttachUserFeedback stamps Message.UserFeedback for the current caller on
// loaded assistant messages. Never fatal.
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
	refs := extractChunkRefs(msg)
	if len(refs) == 0 {
		return nil
	}
	return s.feedbackRepo.SyncMessageChunkRefs(ctx, refs)
}

// extractChunkRefs converts a message's stored knowledge references into
// reference rows, skipping web search results and deduplicating chunk ids.
func extractChunkRefs(msg *types.Message) []types.MessageChunkReference {
	seen := make(map[string]bool, len(msg.KnowledgeReferences))
	refs := make([]types.MessageChunkReference, 0, len(msg.KnowledgeReferences))
	for _, sr := range msg.KnowledgeReferences {
		if sr == nil || sr.ID == "" || sr.KnowledgeBaseID == "" {
			continue
		}
		if sr.ChunkType == "web_search" || sr.KnowledgeSource == "web_search" {
			continue
		}
		if seen[sr.ID] {
			continue
		}
		seen[sr.ID] = true
		refs = append(refs, types.MessageChunkReference{
			MessageID:       msg.ID,
			SessionID:       msg.SessionID,
			ChunkID:         sr.ID,
			KnowledgeBaseID: sr.KnowledgeBaseID,
		})
	}
	return refs
}

// ListChunkStats returns the paged per-chunk feedback statistics of one KB.
func (s *messageFeedbackService) ListChunkStats(
	ctx context.Context, kbID string, query *interfaces.ChunkFeedbackStatsQuery,
) (*types.PageResult, error) {
	kb, err := s.kbRepo.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		return nil, werrors.NewNotFoundError("knowledge base not found")
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
	kb, err := s.kbRepo.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		return nil, werrors.NewNotFoundError("knowledge base not found")
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
	kb, err := s.kbRepo.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		return 0, werrors.NewNotFoundError("knowledge base not found")
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
	fingerprint := retrievalConfigFingerprint(tenant.RetrievalConfig)
	confirmStale := func(ctx context.Context) (bool, error) {
		current, err := s.tenantRepo.GetTenantByID(ctx, tenantID)
		if err != nil {
			return false, err
		}
		return retrievalConfigFingerprint(current.RetrievalConfig) != fingerprint, nil
	}
	changed, err := s.feedbackRepo.RecomputeFeedbackWeights(
		ctx, tenantID,
		map[string]*types.RetrievalConfig{"": tenant.RetrievalConfig},
		confirmStale,
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

func retrievalConfigFingerprint(cfg *types.RetrievalConfig) string {
	if cfg == nil {
		return ""
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return ""
	}
	return string(raw)
}
