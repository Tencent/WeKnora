package memory

import (
	"context"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// searchCandidatePool bounds how many stored items one search ranks over.
//
// It matches the recall pool deliberately. The pool was never what made recall
// miss things — four hundred candidates is more than almost any subject holds
// — the output cap of five items was. Widening the pool here would buy little
// and would put a few thousand embedding reads on a path the user is waiting
// on.
const searchCandidatePool = 400

// MemoryAvailable reports whether this request may read memory at all.
//
// It is deliberately the same predicate SearchMemory itself applies, rather
// than a second reading of the three switches. A caller that decides whether
// to offer a memory feature and the code that answers when it is used must
// never be able to disagree about whether memory is on.
func (s *Service) MemoryAvailable(ctx context.Context) bool {
	_, _, ok := s.enabledScope(ctx)
	return ok
}

// SearchMemory ranks this user's stored memories against an arbitrary query.
//
// Recall runs once per turn, against the question the user opened with, and
// what it admits is capped hard: five situational items inside a 600-rune
// budget. Two things fall outside that. An agent loop that has spent ten
// iterations moving away from the opening question is now working on something
// that shares no wording with what recall was ranked against; and a subject
// with dozens of stored facts has most of them sitting below the cut with no
// way to reach them. Neither is fixable by enlarging the per-turn budget,
// because that budget is paid on every turn including the ones that need none
// of it.
//
// Only active items are searched. Superseded and archived memories stay out of
// reach on purpose: a statement that a newer one replaced is precisely what
// the supersede machinery exists to keep out of an answer, and surfacing it
// through a side door would undo that.
func (s *Service) SearchMemory(
	ctx context.Context, query string, limit int,
) interfaces.MemorySearchResult {
	query = strings.TrimSpace(query)

	searchCtx, searchSpan := langfuse.GetManager().StartSpan(ctx, langfuse.SpanOptions{
		Name: "memory.search",
		Input: map[string]interface{}{
			"query": langfuse.TruncateRunes(query, recallQueryPreviewRunes),
			"limit": limit,
		},
	})

	scope, cfg, ok := s.enabledScope(searchCtx)
	if !ok {
		reason := s.scopeDisableReason(searchCtx)
		logger.Infof(searchCtx, "memory: search skipped (%s)", reason)
		searchSpan.Finish(langfuse.SummarizeMemoryRecallOutput(map[string]interface{}{
			"outcome": "disabled",
			"reason":  reason,
		}, nil), nil, nil)
		return interfaces.MemorySearchResult{}
	}

	// An empty query reaches here rather than short-circuiting above so that
	// "memory is off" still wins over "you asked for nothing": the caller
	// needs the disabled answer even when its own arguments were malformed.
	if query == "" {
		searchSpan.Finish(langfuse.SummarizeMemoryRecallOutput(map[string]interface{}{
			"outcome": "empty",
			"reason":  "blank_query",
		}, nil), nil, nil)
		return interfaces.MemorySearchResult{Available: true}
	}

	if limit <= 0 {
		limit = types.MemorySearchDefaultItems
	}
	if limit > types.MemorySearchMaxItems {
		limit = types.MemorySearchMaxItems
	}

	candidates, err := s.repo.ListActiveByKinds(searchCtx, scope, types.MemoryKinds, searchCandidatePool)
	if err != nil {
		logger.Warnf(searchCtx, "memory: load search candidates failed: %v", err)
		searchSpan.Finish(langfuse.SummarizeMemoryRecallOutput(map[string]interface{}{
			"outcome": "error",
			"error":   err.Error(),
		}, nil), nil, err)
		return interfaces.MemorySearchResult{Available: true}
	}

	matched, rankTrace := s.selectRecallWithTrace(
		searchCtx, scope, cfg, query, candidates, limit, types.MemorySearchRuneBudget)

	// A searched memory was read by the model just as surely as an injected
	// one, so it counts as used. Without this the items only reachable through
	// search would look permanently unused and rank lowest when the capacity
	// cap next decides what to archive.
	s.touchAsync(searchCtx, scope, matched)

	logger.Infof(searchCtx,
		"memory: search done subject=%s candidates=%d matched=%d mode=%s",
		scope.SubjectID, len(candidates), len(matched), rankTrace.Mode)
	searchSpan.Finish(langfuse.SummarizeMemoryRecallOutput(map[string]interface{}{
		"outcome":         "ok",
		"subject_id":      scope.SubjectID,
		"candidate_count": len(candidates),
		"lexical_hits":    rankTrace.LexicalHits,
		"vector_hits":     rankTrace.VectorHits,
		"vector_skip":     rankTrace.VectorSkipReason,
		"ranking_mode":    rankTrace.Mode,
		"matched_count":   len(matched),
	}, matched), map[string]interface{}{
		"tenant_id": scope.TenantID,
	}, nil)

	return interfaces.MemorySearchResult{Available: true, Items: matched}
}
