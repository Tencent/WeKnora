// Package feedbackweight applies the shared, fail-open feedback policy to
// retrieval candidates.
package feedbackweight

import (
	"context"
	"fmt"
	"math"
	"sort"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
)

// Policy outcome reason codes are bounded values suitable for logs and tests.
const (
	ReasonDisabled          = "disabled"
	ReasonWorkspaceDisabled = "workspace_disabled"
	ReasonNoScope           = "no_scope"
	ReasonNoFeedback        = "no_feedback"
	ReasonInvalidData       = "invalid_data"
	ReasonAlreadyApplied    = "already_applied"
	ReasonApplied           = "applied"
)

// StatRepository is the minimal batched query needed by retrieval weighting.
type StatRepository interface {
	ListChunkFeedbackStats(
		ctx context.Context,
		scopes []types.ChunkFeedbackScope,
	) ([]types.ChunkFeedbackStat, error)
}

// Candidate is a retrieval adapter's policy-neutral input.
type Candidate struct {
	TenantID        uint64
	KnowledgeBaseID string
	ChunkID         string
	Score           float64
	OriginalIndex   int
	WorkspaceOptIn  bool
	AlreadyApplied  bool
}

// WeightedCandidate contains derived policy values without overwriting the
// pre-feedback score.
type WeightedCandidate struct {
	Candidate
	StoredRecallWeight    float64
	EffectiveRecallWeight float64
	EffectiveScore        float64
}

// Outcome describes a complete, atomic policy application. On any runtime
// error Candidates preserve the original scores and order.
type Outcome struct {
	Candidates   []WeightedCandidate
	Applied      bool
	ChangedOrder bool
	TopKChanged  bool
	Reason       string
	Err          error
}

// LogSummary contains bounded, identifier-free evidence for policy decisions.
// It is intended for structured logs/traces, never high-cardinality labels.
type LogSummary struct {
	MinimumSampleCount int64
	HighThreshold      float64
	LowThreshold       float64
	HighWeight         float64
	NormalWeight       float64
	LowWeight          float64
	StoredWeightMin    float64
	StoredWeightMax    float64
	EffectiveWeightMin float64
	EffectiveWeightMax float64
	HighCandidates     int
	NormalCandidates   int
	LowCandidates      int
	NeutralCandidates  int
}

// LogSummary returns bounded, identifier-free policy telemetry.
func (o Outcome) LogSummary(policy *config.FeedbackConfig) LogSummary {
	summary := LogSummary{
		StoredWeightMin: 1, StoredWeightMax: 1,
		EffectiveWeightMin: 1, EffectiveWeightMax: 1,
	}
	if policy != nil {
		summary.MinimumSampleCount = policy.MinimumSampleCount
		summary.HighThreshold = policy.HighRateThreshold
		summary.LowThreshold = policy.LowRateThreshold
		summary.HighWeight = policy.HighRecallWeight
		summary.NormalWeight = policy.NormalRecallWeight
		summary.LowWeight = policy.LowRecallWeight
	}
	if len(o.Candidates) == 0 {
		return summary
	}
	summary.StoredWeightMin = o.Candidates[0].StoredRecallWeight
	summary.StoredWeightMax = o.Candidates[0].StoredRecallWeight
	summary.EffectiveWeightMin = o.Candidates[0].EffectiveRecallWeight
	summary.EffectiveWeightMax = o.Candidates[0].EffectiveRecallWeight
	for _, candidate := range o.Candidates {
		summary.StoredWeightMin = math.Min(summary.StoredWeightMin, candidate.StoredRecallWeight)
		summary.StoredWeightMax = math.Max(summary.StoredWeightMax, candidate.StoredRecallWeight)
		summary.EffectiveWeightMin = math.Min(summary.EffectiveWeightMin, candidate.EffectiveRecallWeight)
		summary.EffectiveWeightMax = math.Max(summary.EffectiveWeightMax, candidate.EffectiveRecallWeight)
		switch candidate.EffectiveRecallWeight {
		case summary.HighWeight:
			summary.HighCandidates++
		case summary.LowWeight:
			summary.LowCandidates++
		case summary.NormalWeight:
			summary.NormalCandidates++
		default:
			summary.NeutralCandidates++
		}
	}
	return summary
}

type scopeKey struct {
	tenantID        uint64
	knowledgeBaseID string
	chunkID         string
}

func makeScopeKey(scope types.ChunkFeedbackScope) scopeKey {
	return scopeKey{
		tenantID:        scope.TenantID,
		knowledgeBaseID: scope.KnowledgeBaseID,
		chunkID:         scope.ChunkID,
	}
}

func originalOutcome(candidates []Candidate, reason string, err error) Outcome {
	result := make([]WeightedCandidate, len(candidates))
	for i, candidate := range candidates {
		result[i] = WeightedCandidate{
			Candidate:             candidate,
			StoredRecallWeight:    1,
			EffectiveRecallWeight: 1,
			EffectiveScore:        candidate.Score,
		}
	}
	return Outcome{Candidates: result, Reason: reason, Err: err}
}

// Apply performs one batched lookup and either applies the complete policy or
// returns the original candidates unchanged.
//
// Feedback-based retrieval weighting is advisory. Runtime repository or
// numerical failures must preserve the original retrieval scores, ordering,
// and top-k result.
func Apply(
	ctx context.Context,
	global *config.FeedbackConfig,
	repo StatRepository,
	candidates []Candidate,
	topK int,
) Outcome {
	if len(candidates) == 0 {
		return originalOutcome(candidates, ReasonNoScope, nil)
	}
	if global == nil || !global.Enabled || !global.RetrievalWeightEnabled {
		return originalOutcome(candidates, ReasonDisabled, nil)
	}
	if err := global.Validate(); err != nil {
		return originalOutcome(candidates, ReasonInvalidData, err)
	}

	allApplied := true
	anyApplied := false
	anyWorkspace := false
	scopes := make([]types.ChunkFeedbackScope, 0, len(candidates))
	seenScopes := make(map[scopeKey]struct{}, len(candidates))
	for _, candidate := range candidates {
		if math.IsNaN(candidate.Score) || math.IsInf(candidate.Score, 0) {
			return originalOutcome(candidates, ReasonInvalidData, fmt.Errorf("candidate score must be finite"))
		}
		allApplied = allApplied && candidate.AlreadyApplied
		anyApplied = anyApplied || candidate.AlreadyApplied
		if !candidate.WorkspaceOptIn {
			continue
		}
		anyWorkspace = true
		if candidate.TenantID == 0 || candidate.KnowledgeBaseID == "" || candidate.ChunkID == "" {
			return originalOutcome(candidates, ReasonNoScope, fmt.Errorf("candidate feedback scope is incomplete"))
		}
		scope := types.ChunkFeedbackScope{
			TenantID: candidate.TenantID, KnowledgeBaseID: candidate.KnowledgeBaseID, ChunkID: candidate.ChunkID,
		}
		key := makeScopeKey(scope)
		if _, ok := seenScopes[key]; ok {
			continue
		}
		seenScopes[key] = struct{}{}
		scopes = append(scopes, scope)
	}
	if allApplied {
		return originalOutcome(candidates, ReasonAlreadyApplied, nil)
	}
	if anyApplied {
		return originalOutcome(candidates, ReasonInvalidData, fmt.Errorf("mixed feedback application state"))
	}
	if !anyWorkspace {
		return originalOutcome(candidates, ReasonWorkspaceDisabled, nil)
	}
	if repo == nil {
		return originalOutcome(candidates, ReasonInvalidData, fmt.Errorf("feedback stat repository is nil"))
	}

	stats, err := repo.ListChunkFeedbackStats(ctx, scopes)
	if err != nil {
		return originalOutcome(candidates, ReasonInvalidData, err)
	}
	byScope := make(map[scopeKey]types.ChunkFeedbackStat, len(stats))
	for _, stat := range stats {
		key := makeScopeKey(stat.ChunkFeedbackScope)
		if _, exists := byScope[key]; exists {
			return originalOutcome(candidates, ReasonInvalidData, fmt.Errorf("duplicate feedback stat"))
		}
		if _, requested := seenScopes[key]; !requested {
			return originalOutcome(candidates, ReasonInvalidData, fmt.Errorf("feedback stat outside requested scope"))
		}
		if stat.LikeCount < 0 || stat.DislikeCount < 0 ||
			math.IsNaN(stat.StoredRecallWeight) || math.IsInf(stat.StoredRecallWeight, 0) ||
			stat.StoredRecallWeight <= 0 {
			return originalOutcome(candidates, ReasonInvalidData, fmt.Errorf("invalid feedback stat"))
		}
		byScope[key] = stat
	}

	result := make([]WeightedCandidate, len(candidates))
	usedFeedback := false
	for i, candidate := range candidates {
		weighted := WeightedCandidate{
			Candidate:             candidate,
			StoredRecallWeight:    1,
			EffectiveRecallWeight: 1,
			EffectiveScore:        candidate.Score,
		}
		if candidate.WorkspaceOptIn {
			stat, ok := byScope[scopeKey{
				tenantID: candidate.TenantID, knowledgeBaseID: candidate.KnowledgeBaseID, chunkID: candidate.ChunkID,
			}]
			if ok {
				weighted.StoredRecallWeight = stat.StoredRecallWeight
				weight, _, calcErr := EffectiveWeight(global, stat.LikeCount, stat.DislikeCount)
				if calcErr != nil {
					return originalOutcome(candidates, ReasonInvalidData, calcErr)
				}
				weighted.EffectiveRecallWeight = weight
				weighted.EffectiveScore = candidate.Score * weight
				if math.IsNaN(weighted.EffectiveScore) || math.IsInf(weighted.EffectiveScore, 0) {
					return originalOutcome(candidates, ReasonInvalidData, fmt.Errorf("effective score is not finite"))
				}
				usedFeedback = usedFeedback || stat.LikeCount+stat.DislikeCount > 0
			}
		}
		result[i] = weighted
	}

	originalTopK := topKIdentity(result, topK)
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].EffectiveScore != result[j].EffectiveScore {
			return result[i].EffectiveScore > result[j].EffectiveScore
		}
		return result[i].OriginalIndex < result[j].OriginalIndex
	})
	changedOrder := false
	for i := range result {
		if result[i].OriginalIndex != candidates[i].OriginalIndex {
			changedOrder = true
			break
		}
	}
	reason := ReasonApplied
	if !usedFeedback {
		reason = ReasonNoFeedback
	}
	return Outcome{
		Candidates:   result,
		Applied:      true,
		ChangedOrder: changedOrder,
		TopKChanged:  originalTopK != topKIdentity(result, topK),
		Reason:       reason,
	}
}

func topKIdentity(candidates []WeightedCandidate, topK int) string {
	if topK <= 0 || topK > len(candidates) {
		topK = len(candidates)
	}
	result := ""
	for i := 0; i < topK; i++ {
		result += fmt.Sprintf("%d\x00", candidates[i].OriginalIndex)
	}
	return result
}
