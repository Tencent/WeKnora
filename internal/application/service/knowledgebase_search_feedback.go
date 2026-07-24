package service

import (
	"context"
	"fmt"
	"sort"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// Metadata keys stamped on SearchResult when a feedback recall weight was
// applied. The rerank stage reads them to compute its composite from the
// unweighted score and re-apply the factor exactly once.
const (
	feedbackFactorMetaKey        = "feedback_factor"
	feedbackOriginalScoreMetaKey = "feedback_original_score"
)

// applyFeedbackWeights multiplies each candidate's score by its stored
// feedback recall weight and re-sorts (stable) so the weights influence which
// candidates survive the MatchCount truncation that follows. No-op unless the
// tenant enabled feedback ranking. Weights come from the chunks table in one
// batched query; candidates without a stored non-neutral weight are untouched.
func (s *knowledgeBaseService) applyFeedbackWeights(
	ctx context.Context,
	retrievalCfg *types.RetrievalConfig,
	candidates []*types.IndexWithScore,
) {
	if !retrievalCfg.GetEffectiveFeedbackRankingEnabled() || len(candidates) == 0 {
		return
	}
	chunkIDs := make([]string, 0, len(candidates))
	for _, c := range candidates {
		if c.ChunkID != "" {
			chunkIDs = append(chunkIDs, c.ChunkID)
		}
	}
	weights, err := s.feedbackRepo.ListChunkWeights(ctx, chunkIDs)
	if err != nil {
		logger.Warnf(ctx, "Failed to load feedback weights, skipping adjustment: %v", err)
		return
	}
	if len(weights) == 0 {
		return
	}
	adjusted := 0
	for _, c := range candidates {
		weight, ok := weights[c.ChunkID]
		if !ok || weight <= 0 {
			continue
		}
		c.OriginalScore = c.Score
		c.FeedbackFactor = weight
		c.Score *= weight
		adjusted++
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].Score > candidates[j].Score
	})
	logger.Infof(ctx, "Applied feedback recall weights to %d/%d candidates", adjusted, len(candidates))
}

// stampFeedbackMetadata records the applied factor and the unweighted score
// on a SearchResult. The keys are always cleared first: knowledge-level
// metadata is user-controlled and must not be able to smuggle in a factor.
func stampFeedbackMetadata(sr *types.SearchResult, factor, originalScore float64) {
	if sr.Metadata != nil {
		delete(sr.Metadata, feedbackFactorMetaKey)
		delete(sr.Metadata, feedbackOriginalScoreMetaKey)
	}
	if factor == 0 || factor == 1 {
		return
	}
	if sr.Metadata == nil {
		sr.Metadata = map[string]string{}
	}
	sr.Metadata[feedbackFactorMetaKey] = fmt.Sprintf("%.4f", factor)
	sr.Metadata[feedbackOriginalScoreMetaKey] = fmt.Sprintf("%.6f", originalScore)
}
