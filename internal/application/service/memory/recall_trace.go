package memory

import (
	"context"

	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const recallQueryPreviewRunes = 500

// recallRankingTrace captures how situational items were ranked for one turn.
type recallRankingTrace struct {
	LexicalHits      int
	VectorHits       int
	VectorSkipReason string
	FusedCandidates  int
	Matched          int
	Mode             string
}

// scopeDisableReason explains why memory is off for this request. Only called
// when enabledScope returned false.
func (s *Service) scopeDisableReason(ctx context.Context) string {
	scope, err := ResolveScope(ctx)
	if err != nil {
		return "no_principal"
	}
	cfg := s.workspaceConfig(ctx, scope.TenantID)
	if !cfg.MemoryEnabled() {
		return "workspace_disabled"
	}
	if !types.MemoryAllowedForAgent(ctx) {
		return "agent_disabled"
	}
	subject, err := s.repo.GetSubject(ctx, scope)
	if err != nil {
		return "subject_load_failed"
	}
	if subject != nil && !subject.Enabled {
		return "user_disabled"
	}
	return "unknown"
}

// recallEmptyMeta explains why Recall produced no prompt.
func (s *Service) recallEmptyMeta(
	scope interfaces.MemoryScope,
	residentCount, candidateCount int,
	rankTrace recallRankingTrace,
) map[string]interface{} {
	return map[string]interface{}{
		"outcome":          "empty",
		"reason":           "no_injectable_memories",
		"subject_id":       scope.SubjectID,
		"resident_count":   residentCount,
		"candidate_count":  candidateCount,
		"lexical_hits":     rankTrace.LexicalHits,
		"vector_hits":      rankTrace.VectorHits,
		"vector_skip":      rankTrace.VectorSkipReason,
		"ranking_mode":     rankTrace.Mode,
		"fused_candidates": rankTrace.FusedCandidates,
	}
}

// splitResidentInterests separates interests from the rest of the resident set,
// which are treated differently: the others are unconditional, interests are
// capped.
func splitResidentInterests(items []*types.MemoryItem) (others, interests []*types.MemoryItem) {
	for _, item := range items {
		if item == nil {
			continue
		}
		if item.Kind == types.MemoryKindInterest {
			interests = append(interests, item)
			continue
		}
		others = append(others, item)
	}
	return others, interests
}

// selectResidentInterests chooses which interests go into the resident block.
//
// It returns two lists because injecting and reporting are different questions.
// Everything in `selected` is injected, up to the cap. Only `relevant` — the
// ones the current question actually matches — is reported to the chat UI: an
// interest that is present merely because there was room is standing
// background, and listing it as "recalled for this answer" would fill the
// timeline with memories that had nothing to do with what was asked.
//
// Matching is lexical only. The semantic pass needs a query embedding, and
// spending a model round trip on a list this short, whose entries are topic
// labels that a related question usually names outright, is not worth adding
// to the front of every turn.
func selectResidentInterests(
	query string, interests []*types.MemoryItem, maxItems int,
) (selected, relevant []*types.MemoryItem) {
	if len(interests) == 0 || maxItems <= 0 {
		return nil, nil
	}
	taken := make(map[int]struct{}, maxItems)
	for _, index := range lexicalRanking(query, interests) {
		if len(selected) >= maxItems {
			break
		}
		taken[index] = struct{}{}
		selected = append(selected, interests[index])
		relevant = append(relevant, interests[index])
	}
	// Fill whatever room is left in repository order (importance, then
	// recency), so a question that matches nothing still sees the subjects
	// this person keeps coming back to.
	for index, item := range interests {
		if len(selected) >= maxItems {
			break
		}
		if _, dup := taken[index]; dup {
			continue
		}
		selected = append(selected, item)
	}
	return selected, relevant
}

// selectRecallWithTrace ranks candidates against the query and returns the
// best ones that fit in maxItems and runeBudget. The budgets are arguments
// rather than constants because the same fusion serves two callers with very
// different economics: a per-turn recall that must stay small, and an
// on-demand search the model paid a tool call for.
func (s *Service) selectRecallWithTrace(
	ctx context.Context,
	scope interfaces.MemoryScope,
	cfg *types.MemoryConfig,
	query string,
	candidates []*types.MemoryItem,
	maxItems int,
	runeBudget int,
) ([]*types.MemoryItem, recallRankingTrace) {
	trace := recallRankingTrace{}
	if len(candidates) == 0 {
		trace.Mode = "no_candidates"
		return nil, trace
	}

	_, lexSpan := langfuse.GetManager().StartSpan(ctx, langfuse.SpanOptions{
		Name: "memory.recall.lexical",
		Input: map[string]interface{}{
			"query":      langfuse.TruncateRunes(query, recallQueryPreviewRunes),
			"candidates": len(candidates),
		},
	})
	lexical := lexicalRanking(query, candidates)
	trace.LexicalHits = len(lexical)
	lexSpan.Finish(map[string]interface{}{
		"hits": trace.LexicalHits,
	}, nil, nil)

	vecCtx, vecSpan := langfuse.GetManager().StartSpan(ctx, langfuse.SpanOptions{
		Name: "memory.recall.vector",
		Input: map[string]interface{}{
			"query":      langfuse.TruncateRunes(query, recallQueryPreviewRunes),
			"candidates": len(candidates),
		},
		Metadata: map[string]interface{}{
			"vector_enabled": cfg != nil && cfg.VectorRecallEnabled(),
		},
	})
	vector, vectorSkip := s.vectorRanking(vecCtx, scope, cfg, query, candidates)
	trace.VectorHits = len(vector)
	trace.VectorSkipReason = vectorSkip
	vecOut := map[string]interface{}{
		"hits": trace.VectorHits,
	}
	if vectorSkip != "" {
		vecOut["skip_reason"] = vectorSkip
	}
	vecSpan.Finish(vecOut, nil, nil)

	if len(vector) == 0 {
		trace.Mode = "lexical_only"
		matched := takeWithinBudget(lexical, candidates, maxItems, runeBudget)
		trace.Matched = len(matched)
		return matched, trace
	}

	if len(vector) > maxItems*2 {
		vector = vector[:maxItems*2]
	}
	fused := fuseRankings(lexical, vector)
	trace.FusedCandidates = len(fused)
	trace.Mode = "hybrid"
	matched := takeWithinBudget(fused, candidates, maxItems, runeBudget)
	trace.Matched = len(matched)
	return matched, trace
}
