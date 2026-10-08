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
	LexicalHits int
	VectorHits  int
	// VectorOutsidePool counts semantic matches the lexical pool did not hold.
	// Every one of these was unreachable while candidate selection happened
	// before the query was looked at.
	VectorOutsidePool int
	VectorSkipReason  string
	FusedCandidates   int
	Matched           int
	Mode              string
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
		"vector_outside":   rankTrace.VectorOutsidePool,
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

// recallSelection is one ranking request.
//
// A struct rather than a parameter list because the two callers differ in more
// than budgets: an on-demand search may return any kind and has nothing to
// exclude, while a turn's recall is restricted to situational kinds and must
// not repeat what the resident block already printed. Both of those now have
// to reach the vector stage, which no longer inherits them from a
// pre-filtered candidate list.
type recallSelection struct {
	// Query is what the memories are ranked against.
	Query string
	// Candidates is the lexically searchable pool, already loaded.
	Candidates []*types.MemoryItem
	// Kinds bounds what the semantic search may surface. Empty means any kind.
	Kinds []string
	// ExcludeIDs are memories the caller has already used.
	ExcludeIDs map[string]struct{}
	// MaxItems and RuneBudget bound the result.
	MaxItems   int
	RuneBudget int
}

// vectorFanout is how many semantic hits to ask the store for, relative to
// how many items may be returned.
//
// Wider than the output because fusion needs something to fuse: a hit that
// only the vector side likes has to compete with the lexical ranking, and the
// rune budget can skip over several long items before it finds ones that fit.
// Four times the output with a floor keeps that headroom without turning a
// five-item recall into a hundred-row read.
func vectorFanout(maxItems int) int {
	limit := maxItems * 4
	if limit < 20 {
		limit = 20
	}
	return limit
}

// mergeVectorHits turns semantic hits into a ranking over the candidate pool,
// growing that pool with the matches it did not already contain.
//
// The pool has to grow, or the whole point of searching the store is lost:
// the rankings that fusion combines must address the same slice, so a match
// that only the vector side found needs a place in it. Returns the ranking,
// the pool it addresses, and how many items the search added — the last one
// only so the trace can show it.
func mergeVectorHits(
	candidates []*types.MemoryItem,
	hits []interfaces.MemoryVectorHit,
	excludeIDs map[string]struct{},
) ([]int, []*types.MemoryItem, int) {
	if len(hits) == 0 {
		return nil, candidates, 0
	}
	indexByID := make(map[string]int, len(candidates))
	for i, item := range candidates {
		if item != nil && item.ID != "" {
			indexByID[item.ID] = i
		}
	}
	// Copy before appending. The caller's slice may share a backing array with
	// the list it was filtered out of, and growing into that would rewrite
	// rows nobody asked us to touch.
	pool := candidates
	copied := false
	added := 0

	ranking := make([]int, 0, len(hits))
	for _, hit := range hits {
		if hit.Item == nil || hit.Item.ID == "" {
			continue
		}
		if _, skip := excludeIDs[hit.Item.ID]; skip {
			continue
		}
		if index, ok := indexByID[hit.Item.ID]; ok {
			ranking = append(ranking, index)
			continue
		}
		if !copied {
			pool = append([]*types.MemoryItem(nil), candidates...)
			copied = true
		}
		pool = append(pool, hit.Item)
		index := len(pool) - 1
		indexByID[hit.Item.ID] = index
		ranking = append(ranking, index)
		added++
	}
	return ranking, pool, added
}

// selectRecallWithTrace ranks memories against the query and returns the best
// ones that fit the request's budgets.
//
// Two rankings are fused. The lexical one scores the supplied pool; the
// semantic one is a search over every vector the subject has, so it can pull
// in a memory the pool never contained. That asymmetry is deliberate: exact
// wording is cheap to match in process, while "which of this person's
// memories is this question about" is the question a vector store exists to
// answer and must not be asked of an arbitrary subset.
func (s *Service) selectRecallWithTrace(
	ctx context.Context,
	scope interfaces.MemoryScope,
	cfg *types.MemoryConfig,
	req recallSelection,
) ([]*types.MemoryItem, recallRankingTrace) {
	trace := recallRankingTrace{}
	query := req.Query
	candidates := req.Candidates
	maxItems, runeBudget := req.MaxItems, req.RuneBudget

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
	hits, vectorSkip := s.vectorSearch(
		vecCtx, scope, cfg, query, req.Kinds, vectorFanout(maxItems))
	vector, candidates, fetched := mergeVectorHits(candidates, hits, req.ExcludeIDs)
	trace.VectorHits = len(vector)
	trace.VectorOutsidePool = fetched
	trace.VectorSkipReason = vectorSkip
	vecOut := map[string]interface{}{
		"hits": trace.VectorHits,
		// How many matches the lexical pool did not contain. This is the
		// number that used to be forced to zero, so it is the one to watch
		// when asking whether semantic recall is doing anything.
		"outside_pool": fetched,
	}
	if vectorSkip != "" {
		vecOut["skip_reason"] = vectorSkip
	}
	vecSpan.Finish(vecOut, nil, nil)

	if len(vector) == 0 {
		trace.Mode = "lexical_only"
		if len(lexical) == 0 {
			trace.Mode = "no_matches"
		}
		matched := takeWithinBudget(lexical, candidates, maxItems, runeBudget)
		trace.Matched = len(matched)
		return matched, trace
	}

	fused := fuseRankings(lexical, vector)
	trace.FusedCandidates = len(fused)
	trace.Mode = "hybrid"
	matched := takeWithinBudget(fused, candidates, maxItems, runeBudget)
	trace.Matched = len(matched)
	return matched, trace
}
