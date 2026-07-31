package types

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
)

// RetrievalConfig holds the global retrieval/search configuration for a tenant.
// This replaces the retrieval-related fields previously scattered in ConversationConfig
// and ChatHistoryConfig. Both knowledge search and message search share these parameters.
//
// Stored as a JSONB column on the tenants table, managed via the settings UI
// at /tenants/kv/retrieval-config.
type RetrievalConfig struct {
	// EmbeddingTopK is the maximum number of chunks returned by vector search (default: 50)
	EmbeddingTopK int `json:"embedding_top_k"`
	// VectorThreshold is the minimum vector similarity score (0-1, default: 0.15)
	VectorThreshold float64 `json:"vector_threshold"`
	// KeywordThreshold is the minimum keyword match score (0-1, default: 0.3)
	KeywordThreshold float64 `json:"keyword_threshold"`
	// RerankTopK is the maximum number of results after reranking (default: 10)
	RerankTopK int `json:"rerank_top_k"`
	// RerankThreshold is the minimum rerank score (-10 to 10, default: 0.2)
	RerankThreshold float64 `json:"rerank_threshold"`
	// RerankModelID is the ID of the rerank model to use (required for search)
	RerankModelID string `json:"rerank_model_id"`

	// RRFK is the smoothing constant of Reciprocal Rank Fusion. Larger values
	// flatten the curve, reducing the bias towards top-1 results.
	// Default: 60. Sensible range: 30..100 depending on corpus size.
	RRFK int `json:"rrf_k,omitempty"`
	// RRFVectorWeight is the weight applied to the vector retriever inside RRF.
	// RRFVectorWeight + RRFKeywordWeight should usually sum to 1.0 but the math
	// works for any positive weights. Default: 0.7.
	RRFVectorWeight float64 `json:"rrf_vector_weight,omitempty"`
	// RRFKeywordWeight is the keyword counterpart. Default: 0.3.
	RRFKeywordWeight float64 `json:"rrf_keyword_weight,omitempty"`

	// FeedbackRankingEnabled gates whether stored chunk recall weights (from
	// answer like/dislike feedback) are applied to retrieval candidate
	// ordering. Weights are always maintained; this only controls application,
	// so enabling later takes effect immediately. Default: false.
	FeedbackRankingEnabled bool `json:"feedback_ranking_enabled,omitempty"`
	// FeedbackBoostThreshold is the positive rate at or above which a chunk's
	// recall weight is boosted (0-1, default: 0.8).
	FeedbackBoostThreshold float64 `json:"feedback_boost_threshold,omitempty"`
	// FeedbackPenaltyThreshold is the positive rate below which a chunk's
	// recall weight is penalized (0-1, default: 0.5).
	FeedbackPenaltyThreshold float64 `json:"feedback_penalty_threshold,omitempty"`
	// FeedbackBoostFactor multiplies candidate scores of boosted chunks
	// (>= 1, default: 1.2).
	FeedbackBoostFactor float64 `json:"feedback_boost_factor,omitempty"`
	// FeedbackPenaltyFactor multiplies candidate scores of penalized chunks
	// (< 1, default: 0.8).
	FeedbackPenaltyFactor float64 `json:"feedback_penalty_factor,omitempty"`
	// FeedbackMinSamples is the minimum number of ratings a chunk needs
	// before its weight deviates from neutral (default: 3). Without this
	// floor a single early rating could swing retrieval.
	FeedbackMinSamples int `json:"feedback_min_samples,omitempty"`
	// FeedbackNeedsOptimizationThreshold is the positive rate below which a
	// chunk is flagged as needing optimization in the admin stats panel
	// (0-1, default: 0.2). Must not exceed FeedbackPenaltyThreshold.
	FeedbackNeedsOptimizationThreshold float64 `json:"feedback_needs_optimization_threshold,omitempty"`
}

// GetEffectiveEmbeddingTopK returns EmbeddingTopK with a fallback default.
func (c *RetrievalConfig) GetEffectiveEmbeddingTopK() int {
	if c == nil || c.EmbeddingTopK <= 0 {
		return 50
	}
	return c.EmbeddingTopK
}

// GetEffectiveVectorThreshold returns VectorThreshold with a fallback default.
func (c *RetrievalConfig) GetEffectiveVectorThreshold() float64 {
	if c == nil || c.VectorThreshold <= 0 {
		return 0.15
	}
	return c.VectorThreshold
}

// GetEffectiveKeywordThreshold returns KeywordThreshold with a fallback default.
func (c *RetrievalConfig) GetEffectiveKeywordThreshold() float64 {
	if c == nil || c.KeywordThreshold <= 0 {
		return 0.3
	}
	return c.KeywordThreshold
}

// GetEffectiveRerankTopK returns RerankTopK with a fallback default.
func (c *RetrievalConfig) GetEffectiveRerankTopK() int {
	if c == nil || c.RerankTopK <= 0 {
		return 10
	}
	return c.RerankTopK
}

// GetEffectiveRerankThreshold returns RerankThreshold with a fallback default.
func (c *RetrievalConfig) GetEffectiveRerankThreshold() float64 {
	if c == nil {
		return 0.2
	}
	return c.RerankThreshold
}

// GetEffectiveRRFK returns the RRF smoothing constant with a fallback default.
func (c *RetrievalConfig) GetEffectiveRRFK() int {
	if c == nil || c.RRFK <= 0 {
		return 60
	}
	return c.RRFK
}

// GetEffectiveRRFWeights returns vector / keyword weights with sensible defaults.
// When neither weight is set explicitly, returns 0.7 / 0.3.
func (c *RetrievalConfig) GetEffectiveRRFWeights() (vector, keyword float64) {
	if c == nil || (c.RRFVectorWeight == 0 && c.RRFKeywordWeight == 0) {
		return 0.7, 0.3
	}
	v := c.RRFVectorWeight
	k := c.RRFKeywordWeight
	if v <= 0 {
		v = 0.7
	}
	if k <= 0 {
		k = 0.3
	}
	return v, k
}

// GetEffectiveFeedbackRankingEnabled reports whether feedback-based ranking
// adjustments are applied at retrieval time. Defaults to false so existing
// tenants see no behavior change until they explicitly opt in.
func (c *RetrievalConfig) GetEffectiveFeedbackRankingEnabled() bool {
	return c != nil && c.FeedbackRankingEnabled
}

// GetEffectiveFeedbackBoostThreshold returns the boost threshold with a
// fallback default. Must lie in [0, 1]; out-of-range values are clamped.
func (c *RetrievalConfig) GetEffectiveFeedbackBoostThreshold() float64 {
	if c == nil || c.FeedbackBoostThreshold <= 0 {
		return 0.8
	}
	if c.FeedbackBoostThreshold > 1 {
		return 1
	}
	return c.FeedbackBoostThreshold
}

// GetEffectiveFeedbackPenaltyThreshold returns the penalty threshold with a
// fallback default. Must lie in (0, 1]; out-of-range values are clamped.
func (c *RetrievalConfig) GetEffectiveFeedbackPenaltyThreshold() float64 {
	if c == nil || c.FeedbackPenaltyThreshold <= 0 {
		return 0.5
	}
	if c.FeedbackPenaltyThreshold > 1 {
		return 1
	}
	return c.FeedbackPenaltyThreshold
}

// GetEffectiveFeedbackBoostFactor returns the boost factor with a fallback
// default. The factor multiplies candidate scores; it should be >= 1.
func (c *RetrievalConfig) GetEffectiveFeedbackBoostFactor() float64 {
	if c == nil || c.FeedbackBoostFactor < 1 {
		return 1.2
	}
	return c.FeedbackBoostFactor
}

// GetEffectiveFeedbackPenaltyFactor returns the penalty factor with a fallback
// default. The factor multiplies candidate scores; it must be in (0, 1].
func (c *RetrievalConfig) GetEffectiveFeedbackPenaltyFactor() float64 {
	if c == nil || c.FeedbackPenaltyFactor <= 0 || c.FeedbackPenaltyFactor > 1 {
		return 0.8
	}
	return c.FeedbackPenaltyFactor
}

// GetEffectiveFeedbackMinSamples returns the minimum sample count with a
// fallback default. Counters below this floor keep the neutral 1.0 weight.
func (c *RetrievalConfig) GetEffectiveFeedbackMinSamples() int {
	if c == nil || c.FeedbackMinSamples <= 0 {
		return 3
	}
	return c.FeedbackMinSamples
}

// GetEffectiveFeedbackNeedsOptimizationThreshold returns the needs-optimization
// threshold with a fallback default. Must not exceed the penalty threshold
// (otherwise the two states disagree).
func (c *RetrievalConfig) GetEffectiveFeedbackNeedsOptimizationThreshold() float64 {
	if c == nil || c.FeedbackNeedsOptimizationThreshold <= 0 {
		return 0.2
	}
	penalty := c.GetEffectiveFeedbackPenaltyThreshold()
	if c.FeedbackNeedsOptimizationThreshold > penalty {
		return penalty
	}
	return c.FeedbackNeedsOptimizationThreshold
}

// ValidateFeedbackPolicy checks the feedback policy for consistency. Returns
// an error if boost threshold is below the penalty threshold, the boost
// factor is below 1, the penalty factor is above 1, or any rate threshold is
// out of [0, 1]. A nil config is valid (all defaults).
func (c *RetrievalConfig) ValidateFeedbackPolicy() error {
	if c == nil {
		return nil
	}
	boost := c.GetEffectiveFeedbackBoostThreshold()
	penalty := c.GetEffectiveFeedbackPenaltyThreshold()
	if boost < penalty {
		return fmt.Errorf("feedback_boost_threshold (%v) must be >= feedback_penalty_threshold (%v)", boost, penalty)
	}
	if c.FeedbackBoostThreshold != 0 && (c.FeedbackBoostThreshold < 0 || c.FeedbackBoostThreshold > 1) {
		return fmt.Errorf("feedback_boost_threshold must be in [0, 1], got %v", c.FeedbackBoostThreshold)
	}
	if c.FeedbackPenaltyThreshold != 0 && (c.FeedbackPenaltyThreshold < 0 || c.FeedbackPenaltyThreshold > 1) {
		return fmt.Errorf("feedback_penalty_threshold must be in [0, 1], got %v", c.FeedbackPenaltyThreshold)
	}
	if c.FeedbackMinSamples != 0 && c.FeedbackMinSamples < 1 {
		return fmt.Errorf("feedback_min_samples must be >= 1, got %v", c.FeedbackMinSamples)
	}
	if c.FeedbackBoostFactor != 0 && c.FeedbackBoostFactor < 1 {
		return fmt.Errorf("feedback_boost_factor must be >= 1, got %v", c.FeedbackBoostFactor)
	}
	if c.FeedbackPenaltyFactor != 0 && (c.FeedbackPenaltyFactor <= 0 || c.FeedbackPenaltyFactor > 1) {
		return fmt.Errorf("feedback_penalty_factor must be in (0, 1], got %v", c.FeedbackPenaltyFactor)
	}
	if c.FeedbackNeedsOptimizationThreshold != 0 &&
		(c.FeedbackNeedsOptimizationThreshold <= 0 || c.FeedbackNeedsOptimizationThreshold > 1) {
		return fmt.Errorf("feedback_needs_optimization_threshold must be in (0, 1], got %v", c.FeedbackNeedsOptimizationThreshold)
	}
	return nil
}

// Value implements the driver.Valuer interface for database serialization
func (c RetrievalConfig) Value() (driver.Value, error) {
	return json.Marshal(c)
}

// Scan implements the sql.Scanner interface for database deserialization.
// The JSONB column comes back as []byte on PostgreSQL but as string on
// SQLite (Lite mode), so both forms are handled — otherwise a tenant's
// retrieval config would silently deserialize to all-defaults on SQLite.
func (c *RetrievalConfig) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	var b []byte
	switch v := value.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return nil
	}
	return json.Unmarshal(b, c)
}
