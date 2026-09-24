// Package reranking is the single rerank stage shared by the chat pipeline,
// the agent search_knowledge tool and the retrieval APIs: passage building,
// model scoring, threshold filtering with degradation, composite scoring and
// MMR selection.
package reranking

import (
	"context"
	"maps"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/rerank"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	// DefaultThreshold is the minimum rerank score used when no configuration
	// supplies one. It matches RetrievalConfig.GetEffectiveRerankThreshold.
	DefaultThreshold = 0.2
	// DefaultFallbackMinScore is the score the best candidate needs to be kept
	// when nothing passes the threshold. Below it, returning nothing beats
	// forcing an irrelevant result on the caller.
	DefaultFallbackMinScore = 0.15

	// A threshold above degradeFloor that rejects every candidate is retried
	// at degradeFactor times itself, but never below degradeFloor.
	degradeFloor  = 0.3
	degradeFactor = 0.7
)

// FallbackMinScore returns the score the best candidate needs to survive an
// all-rejecting threshold. When the caller explicitly scoped the search to a
// tag or document set, its best candidate is kept regardless, rather than
// letting a global threshold erase the whole authoritative scope. Model
// scores can be negative, so "regardless" is -Inf rather than 0.
func FallbackMinScore(explicitScope bool) float64 {
	if explicitScope {
		return math.Inf(-1)
	}
	return DefaultFallbackMinScore
}

// Options configures one rerank run.
type Options struct {
	// Threshold is the minimum model score a candidate needs.
	Threshold float64
	// TopK, when positive, MMR-selects at most TopK results. Otherwise every
	// candidate that passed the threshold is returned, best first.
	TopK int
	// FallbackMinScore is the score the best candidate needs to be kept when
	// nothing passes the threshold. See FallbackMinScore().
	FallbackMinScore float64
	// FAQScoreBoost, when above 1, multiplies the composite score of FAQ
	// entries (capped at 1).
	FAQScoreBoost float64
}

// Result is the outcome of a rerank run.
type Result struct {
	// Results are the returned rows, best first. They are copies carrying the
	// composite score and base_score / model_score metadata; the input rows
	// are never modified. On a model error they are the input rows unchanged.
	Results []*types.SearchResult
	// Indices[i] is the position of Results[i] in the input slice.
	Indices []int
	// Diagnostics summarises the run for API callers and logs.
	Diagnostics types.RerankDiagnostics

	// Candidates are the input rows with a non-empty passage, in input
	// order; Passages are what the model scored for them. ModelScores index
	// into Candidates. Scored are the rows that passed the threshold, with
	// composite scores, before MMR. Kept for tracing.
	Candidates  []*types.SearchResult
	Passages    []string
	ModelScores []rerank.RankResult
	Scored      []*types.SearchResult
}

// Rerank scores results against query with model and returns the rows that
// pass opts, best first. A failed model call is not an error: the result
// carries the input rows unchanged and a model_error outcome, so callers can
// degrade to the retrieval order.
func Rerank(
	ctx context.Context,
	model rerank.Reranker,
	query string,
	results []*types.SearchResult,
	opts Options,
) *Result {
	res := &Result{
		Diagnostics: types.RerankDiagnostics{
			Threshold:          opts.Threshold,
			EffectiveThreshold: opts.Threshold,
		},
	}

	candidateIdx := make([]int, 0, len(results))
	for i, r := range results {
		if r == nil {
			continue
		}
		passage := ModelPassage(ctx, r)
		if strings.TrimSpace(passage) == "" {
			continue
		}
		candidateIdx = append(candidateIdx, i)
		res.Candidates = append(res.Candidates, r)
		res.Passages = append(res.Passages, passage)
	}
	res.Diagnostics.CandidateCount = len(res.Candidates)
	if len(res.Candidates) == 0 {
		res.Diagnostics.Outcome = types.RerankOutcomeNoCandidates
		return res
	}

	scores, err := model.Rerank(ctx, query, res.Passages)
	if err != nil {
		logger.Warnf(ctx, "[Rerank] Model call failed, keeping retrieval order: %v", err)
		res.Diagnostics.Outcome = types.RerankOutcomeModelError
		res.Diagnostics.Error = err.Error()
		res.Results = results
		res.Indices = make([]int, len(results))
		for i := range results {
			res.Indices[i] = i
		}
		res.Diagnostics.ResultCount = len(results)
		return res
	}
	// Drop out-of-range indices once so nothing below has to re-check them.
	valid := scores[:0:0]
	for _, s := range scores {
		if s.Index >= 0 && s.Index < len(res.Candidates) {
			valid = append(valid, s)
		}
	}
	res.ModelScores = valid
	res.Diagnostics.Applied = true
	top, hasTop := bestScore(valid)
	if hasTop {
		res.Diagnostics.TopScore = top.RelevanceScore
	}

	passing, outcome, effective := applyThreshold(valid, opts)
	res.Diagnostics.Outcome = outcome
	res.Diagnostics.EffectiveThreshold = effective

	scored := make([]*types.SearchResult, 0, len(passing))
	scoredIdx := make([]int, 0, len(passing))
	for _, rr := range passing {
		scored = append(scored, scoredCopy(res.Candidates[rr.Index], rr.RelevanceScore, opts.FAQScoreBoost))
		scoredIdx = append(scoredIdx, candidateIdx[rr.Index])
	}
	order := make([]int, len(scored))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return scored[order[a]].Score > scored[order[b]].Score })
	res.Scored = make([]*types.SearchResult, len(order))
	sortedIdx := make([]int, len(order))
	for i, o := range order {
		res.Scored[i] = scored[o]
		sortedIdx[i] = scoredIdx[o]
	}

	picks := make([]int, len(res.Scored))
	for i := range picks {
		picks[i] = i
	}
	if opts.TopK > 0 {
		picks = SelectMMR(ctx, res.Scored, min(opts.TopK, len(res.Scored)), DefaultMMRLambda)
	}
	res.Results = make([]*types.SearchResult, 0, len(picks))
	res.Indices = make([]int, 0, len(picks))
	for _, p := range picks {
		res.Results = append(res.Results, res.Scored[p])
		res.Indices = append(res.Indices, sortedIdx[p])
	}
	res.Diagnostics.ResultCount = len(res.Results)

	logger.Infof(ctx, "[Rerank] %d candidates -> %d results, outcome=%s threshold=%.3f effective=%.3f top=%.4f",
		len(res.Candidates), len(res.Results), outcome, opts.Threshold, effective, res.Diagnostics.TopScore)
	return res
}

// applyThreshold keeps the scores at or above opts.Threshold. When none
// pass, a threshold above degradeFloor is lowered once; when still none
// pass, the best candidate is kept if it reaches opts.FallbackMinScore.
func applyThreshold(
	scores []rerank.RankResult,
	opts Options,
) ([]rerank.RankResult, types.RerankOutcome, float64) {
	threshold := opts.Threshold
	if passing := filterScores(scores, threshold); len(passing) > 0 {
		return passing, types.RerankOutcomeOK, threshold
	}
	if threshold > degradeFloor {
		threshold = math.Max(threshold*degradeFactor, degradeFloor)
		if passing := filterScores(scores, threshold); len(passing) > 0 {
			return passing, types.RerankOutcomeThresholdDegraded, threshold
		}
	}
	if top, ok := bestScore(scores); ok && top.RelevanceScore >= opts.FallbackMinScore {
		return []rerank.RankResult{top}, types.RerankOutcomeFallbackTop1, threshold
	}
	return nil, types.RerankOutcomeAllBelowThreshold, threshold
}

func filterScores(scores []rerank.RankResult, threshold float64) []rerank.RankResult {
	var out []rerank.RankResult
	for _, s := range scores {
		if s.RelevanceScore >= threshold {
			out = append(out, s)
		}
	}
	return out
}

// bestScore returns the highest score. Providers usually return scores
// sorted, but nothing guarantees it.
func bestScore(scores []rerank.RankResult) (rerank.RankResult, bool) {
	if len(scores) == 0 {
		return rerank.RankResult{}, false
	}
	top := scores[0]
	for _, s := range scores[1:] {
		if s.RelevanceScore > top.RelevanceScore {
			top = s
		}
	}
	return top, true
}

// scoredCopy returns a copy of r whose Score is the composite of the model
// score and its retrieval score, recording both in Metadata.
func scoredCopy(r *types.SearchResult, modelScore, faqBoost float64) *types.SearchResult {
	c := *r
	c.Metadata = maps.Clone(r.Metadata)
	if c.Metadata == nil {
		c.Metadata = make(map[string]string, 2)
	}
	base := r.Score
	c.Metadata["base_score"] = strconv.FormatFloat(base, 'f', 4, 64)
	c.Metadata["model_score"] = strconv.FormatFloat(modelScore, 'f', 4, 64)
	c.Score = CompositeScore(&c, modelScore, base)
	if faqBoost > 1.0 && c.ChunkType == string(types.ChunkTypeFAQ) {
		c.Metadata["faq_boosted"] = "true"
		c.Metadata["faq_original_score"] = strconv.FormatFloat(c.Score, 'f', 4, 64)
		c.Score = math.Min(c.Score*faqBoost, 1.0)
	}
	return &c
}

// CompositeScore blends the rerank model score with the retrieval score and a
// source weight, clamped to [0, 1].
func CompositeScore(r *types.SearchResult, modelScore, baseScore float64) float64 {
	sourceWeight := 1.0
	if strings.EqualFold(r.KnowledgeSource, "web_search") {
		sourceWeight = 0.95
	}
	composite := 0.6*modelScore + 0.3*baseScore + 0.1*sourceWeight
	return math.Min(math.Max(composite, 0), 1)
}
