package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// chunkFeedbackService implements interfaces.ChunkFeedbackService.
type chunkFeedbackService struct {
	repo       interfaces.ChunkFeedbackRepository
	messageSvc interfaces.MessageService
	chunkRepo  interfaces.ChunkRepository
}

// NewChunkFeedbackService creates the chunk feedback service.
func NewChunkFeedbackService(
	repo interfaces.ChunkFeedbackRepository,
	messageSvc interfaces.MessageService,
	chunkRepo interfaces.ChunkRepository,
) interfaces.ChunkFeedbackService {
	return &chunkFeedbackService{
		repo:       repo,
		messageSvc: messageSvc,
		chunkRepo:  chunkRepo,
	}
}

// RecordMessageChunkLinks snapshots the knowledge references of an assistant
// message into message_chunk_links. User messages and reference-less messages
// are ignored.
func (s *chunkFeedbackService) RecordMessageChunkLinks(ctx context.Context, message *types.Message) error {
	tenantID, _ := sessionTenantIDForLookup(ctx)
	links := buildMessageChunkLinks(message, tenantID)
	if len(links) == 0 {
		return nil
	}
	if err := s.repo.RecordMessageChunkLinks(ctx, links); err != nil {
		logger.Warnf(ctx, "record message chunk links failed for message %s: %v", message.ID, err)
		return err
	}
	logger.Debugf(ctx, "recorded %d chunk links for message %s", len(links), message.ID)
	return nil
}

// buildMessageChunkLinks converts the knowledge references of an assistant
// message into MessageChunkLink rows. Returns nil for user messages and for
// messages without references.
func buildMessageChunkLinks(message *types.Message, tenantID uint64) []*types.MessageChunkLink {
	if message == nil || message.ID == "" || message.Role != "assistant" {
		return nil
	}
	refs := message.KnowledgeReferences
	if len(refs) == 0 {
		return nil
	}
	links := make([]*types.MessageChunkLink, 0, len(refs))
	for _, ref := range refs {
		if ref == nil || ref.ID == "" {
			continue
		}
		links = append(links, &types.MessageChunkLink{
			TenantID:        tenantID,
			SessionID:       message.SessionID,
			MessageID:       message.ID,
			ChunkID:         ref.ID,
			KnowledgeID:     ref.KnowledgeID,
			KnowledgeBaseID: ref.KnowledgeBaseID,
			KnowledgeTitle:  ref.KnowledgeTitle,
			ChunkContent:    ref.Content,
		})
	}
	return links
}

// SubmitFeedback records a user's like/dislike for an assistant message and
// attributes it to all linked chunks.
func (s *chunkFeedbackService) SubmitFeedback(ctx context.Context, userID, sessionID, messageID string, rating types.ChunkFeedbackRating, reason string) error {
	if !rating.Valid() {
		return errors.New("invalid feedback rating")
	}
	reason = strings.TrimSpace(reason)
	if len([]rune(reason)) > 500 {
		return errors.New("feedback reason is too long (max 500 characters)")
	}

	message, err := s.messageSvc.GetMessage(ctx, sessionID, messageID)
	if err != nil {
		return err
	}
	if message.Role != "assistant" {
		return errors.New("feedback is only allowed on assistant answers")
	}

	tenantID := types.MustTenantIDFromContext(ctx)

	// Idempotent re-submission: only refresh the reason.
	existing, err := s.repo.GetFeedbackRecord(ctx, userID, messageID)
	if err != nil {
		return err
	}
	if existing != nil && existing.Rating == string(rating) && existing.Reason == reason {
		return nil
	}

	if err := s.repo.UpsertFeedbackRecord(ctx, &types.ChunkFeedbackRecord{
		TenantID:  tenantID,
		UserID:    userID,
		SessionID: sessionID,
		MessageID: messageID,
		Rating:    string(rating),
		Reason:    reason,
	}); err != nil {
		return err
	}

	links, err := s.repo.ListChunkLinksByMessageID(ctx, messageID)
	if err != nil {
		return err
	}
	for _, link := range links {
		if link == nil {
			continue
		}
		if err := s.applyChunkFeedback(ctx, tenantID, link.ChunkID, messageID, userID); err != nil {
			logger.Warnf(ctx, "apply chunk feedback for chunk %s failed: %v", link.ChunkID, err)
		}
	}
	return nil
}

// CancelFeedback removes a user's rating for a message and re-attributes the
// remaining ratings to the linked chunks.
func (s *chunkFeedbackService) CancelFeedback(ctx context.Context, userID, sessionID, messageID string) error {
	if _, err := s.messageSvc.GetMessage(ctx, sessionID, messageID); err != nil {
		return err
	}
	existing, err := s.repo.GetFeedbackRecord(ctx, userID, messageID)
	if err != nil {
		return err
	}
	if existing == nil {
		return nil
	}
	if err := s.repo.DeleteFeedbackRecord(ctx, userID, messageID); err != nil {
		return err
	}

	tenantID := types.MustTenantIDFromContext(ctx)
	links, err := s.repo.ListChunkLinksByMessageID(ctx, messageID)
	if err != nil {
		return err
	}
	for _, link := range links {
		if link == nil {
			continue
		}
		if err := s.applyChunkFeedback(ctx, tenantID, link.ChunkID, messageID, userID); err != nil {
			logger.Warnf(ctx, "re-apply chunk feedback for chunk %s failed: %v", link.ChunkID, err)
		}
	}
	return nil
}

// GetMyRating returns the active rating of a user for a message.
func (s *chunkFeedbackService) GetMyRating(ctx context.Context, userID, messageID string) (string, error) {
	record, err := s.repo.GetFeedbackRecord(ctx, userID, messageID)
	if err != nil || record == nil {
		return "", err
	}
	return record.Rating, nil
}

// GetMyRatingsForMessages returns the active ratings of a user keyed by message id.
func (s *chunkFeedbackService) GetMyRatingsForMessages(ctx context.Context, userID string, messageIDs []string) (map[string]string, error) {
	return s.repo.GetFeedbackRatingsByMessages(ctx, userID, messageIDs)
}

// applyChunkFeedback recomputes a chunk's counters/approval rate and applies
// the automatic recall-weight adjustment based on the configured thresholds.
func (s *chunkFeedbackService) applyChunkFeedback(ctx context.Context, tenantID uint64, chunkID, messageID, userID string) error {
	like, dislike, total, _, err := s.repo.CountFeedbackByChunk(ctx, tenantID, chunkID)
	if err != nil {
		return err
	}
	if err := s.repo.UpdateChunkFeedbackCounters(ctx, tenantID, chunkID, like, dislike); err != nil {
		return err
	}

	cfg, err := s.repo.GetFeedbackConfig(ctx, tenantID)
	if err != nil {
		return err
	}

	chunk, err := s.chunkRepo.GetChunkByID(ctx, tenantID, chunkID)
	if err != nil {
		// ErrChunkNotFound aliases the repository sentinel, so a missing
		// chunk is a benign no-op (it may have been deleted after linking).
		if errors.Is(err, ErrChunkNotFound) {
			return nil
		}
		return err
	}

	rate := 0.0
	if total > 0 {
		rate = float64(like) / float64(total)
	}

	newWeight := chunk.RecallWeight
	if total >= cfg.MinVotes {
		switch {
		case rate >= cfg.BoostThreshold:
			newWeight = math.Min(cfg.MaxWeight, chunk.RecallWeight+cfg.WeightStep)
		case rate < cfg.DegradeThreshold:
			newWeight = math.Max(cfg.MinWeight, chunk.RecallWeight-cfg.WeightStep)
		}
	}
	needsOptimization := total >= cfg.MinVotes && rate < cfg.OptimizeThreshold

	weightChanged := math.Abs(newWeight-chunk.RecallWeight) > 1e-9
	flagChanged := needsOptimization != chunk.NeedsOptimization
	if !weightChanged && !flagChanged {
		return nil
	}

	if err := s.repo.UpdateChunkRecallWeight(ctx, tenantID, chunkID, newWeight, needsOptimization); err != nil {
		return err
	}
	if weightChanged {
		reason := ""
		if rate >= cfg.BoostThreshold {
			reason = fmt.Sprintf("approval rate %.1f%% >= boost threshold %.1f%%", rate*100, cfg.BoostThreshold*100)
		} else if rate < cfg.DegradeThreshold {
			reason = fmt.Sprintf("approval rate %.1f%% < degrade threshold %.1f%%", rate*100, cfg.DegradeThreshold*100)
		}
		if err := s.repo.CreateWeightLog(ctx, &types.ChunkWeightLog{
			TenantID:        tenantID,
			ChunkID:         chunkID,
			KnowledgeBaseID: chunk.KnowledgeBaseID,
			OldWeight:       chunk.RecallWeight,
			NewWeight:       newWeight,
			Source:          string(types.ChunkWeightLogSourceFeedback),
			MessageID:       messageID,
			UserID:          userID,
			Reason:          reason,
		}); err != nil {
			logger.Warnf(ctx, "create weight log failed for chunk %s: %v", chunkID, err)
		}
	}
	return nil
}

// GetChunkFeedbackStats returns paged per-chunk feedback stats.
func (s *chunkFeedbackService) GetChunkFeedbackStats(ctx context.Context, params *interfaces.ChunkFeedbackStatsParams) ([]*types.ChunkFeedbackStat, int64, error) {
	return s.repo.GetChunkFeedbackStats(ctx, params)
}

// GetChunkFeedbackDetail returns the detail view for one chunk.
func (s *chunkFeedbackService) GetChunkFeedbackDetail(ctx context.Context, tenantID uint64, chunkID string) (*types.ChunkFeedbackDetail, error) {
	return s.repo.GetChunkFeedbackDetail(ctx, tenantID, chunkID)
}

// ListWeightLogs returns paged weight-change logs.
func (s *chunkFeedbackService) ListWeightLogs(ctx context.Context, tenantID uint64, chunkID, source string, page, pageSize int) ([]*types.ChunkWeightLog, int64, error) {
	return s.repo.ListWeightLogs(ctx, tenantID, chunkID, source, page, pageSize)
}

// ResetChunkFeedback manually zeroes feedback data and restores the default
// weight for the given chunks, auditing each reset in the weight log.
func (s *chunkFeedbackService) ResetChunkFeedback(ctx context.Context, tenantID uint64, chunkIDs []string, operatorID string) error {
	chunkIDs = dedupeNonEmpty(chunkIDs)
	if len(chunkIDs) == 0 {
		return nil
	}
	chunks, err := s.chunkRepo.ListChunksByID(ctx, tenantID, chunkIDs)
	if err != nil {
		return err
	}
	oldWeights := make(map[string]float64, len(chunks))
	kbIDs := make(map[string]string, len(chunks))
	for _, c := range chunks {
		if c == nil {
			continue
		}
		oldWeights[c.ID] = c.RecallWeight
		kbIDs[c.ID] = c.KnowledgeBaseID
	}

	if err := s.repo.ResetChunkFeedback(ctx, tenantID, chunkIDs); err != nil {
		return err
	}

	now := time.Now()
	for _, id := range chunkIDs {
		oldWeight, ok := oldWeights[id]
		if !ok {
			oldWeight = 1.0
		}
		if err := s.repo.CreateWeightLog(ctx, &types.ChunkWeightLog{
			TenantID:        tenantID,
			ChunkID:         id,
			KnowledgeBaseID: kbIDs[id],
			OldWeight:       oldWeight,
			NewWeight:       1.0,
			Source:          string(types.ChunkWeightLogSourceManualReset),
			UserID:          operatorID,
			Reason:          "admin manually reset chunk feedback and recall weight",
			CreatedAt:       now,
		}); err != nil {
			logger.Warnf(ctx, "create weight log failed for chunk %s: %v", id, err)
		}
	}
	return nil
}

// GetConfig returns the tenant feedback config.
func (s *chunkFeedbackService) GetConfig(ctx context.Context, tenantID uint64) (*types.ChunkFeedbackConfig, error) {
	return s.repo.GetFeedbackConfig(ctx, tenantID)
}

// UpdateConfig upserts the tenant feedback config after validation.
func (s *chunkFeedbackService) UpdateConfig(ctx context.Context, cfg *types.ChunkFeedbackConfig) error {
	if cfg == nil {
		return errors.New("feedback config is required")
	}
	if cfg.BoostThreshold <= 0 || cfg.BoostThreshold > 1 {
		return errors.New("boost_threshold must be in (0, 1]")
	}
	if cfg.DegradeThreshold <= 0 || cfg.DegradeThreshold >= 1 {
		return errors.New("degrade_threshold must be in (0, 1)")
	}
	if cfg.OptimizeThreshold <= 0 || cfg.OptimizeThreshold >= 1 {
		return errors.New("optimize_threshold must be in (0, 1)")
	}
	if cfg.DegradeThreshold >= cfg.BoostThreshold {
		return errors.New("degrade_threshold must be lower than boost_threshold")
	}
	if cfg.MinVotes < 0 {
		return errors.New("min_votes must be >= 0")
	}
	if cfg.WeightStep <= 0 {
		return errors.New("weight_step must be positive")
	}
	if cfg.MinWeight <= 0 || cfg.MaxWeight < cfg.MinWeight {
		return errors.New("min_weight must be positive and not greater than max_weight")
	}
	return s.repo.UpdateFeedbackConfig(ctx, cfg)
}

// dedupeNonEmpty returns unique non-empty strings preserving order.
func dedupeNonEmpty(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}
