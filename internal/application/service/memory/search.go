package memory

import (
	"context"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

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

// SearchMemory answers an on-demand lookup into this user's memory.
//
// This is the path the architecture leans on hardest. The profile injected
// into every turn is small and mostly pointers, so the detail behind a pointer
// has to be reachable — and reachable on the question the model has now, not
// the one the conversation opened with. An agent ten iterations into a task is
// working on something that shares no wording with the opening query, and that
// is exactly when "what happened last time we tried this" is worth having.
//
// Whole accounts come back, not excerpts. A tool call is the model deliberately
// asking, and the reason it asked is the detail: trimming an account to a
// sentence here would answer the question with the same thing the pointer in
// the profile already said.
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
		searchSpan.Finish(map[string]interface{}{
			"outcome": "disabled",
			"reason":  reason,
		}, nil, nil)
		return interfaces.MemorySearchResult{}
	}

	// An empty query reaches here rather than short-circuiting above so that
	// "memory is off" still wins over "you asked for nothing": the caller
	// needs the disabled answer even when its own arguments were malformed.
	if query == "" {
		searchSpan.Finish(map[string]interface{}{
			"outcome": "empty",
			"reason":  "blank_query",
		}, nil, nil)
		return interfaces.MemorySearchResult{Available: true}
	}

	if limit <= 0 {
		limit = types.MemorySearchDefaultEpisodes
	}
	if limit > types.MemorySearchMaxEpisodes {
		limit = types.MemorySearchMaxEpisodes
	}

	// A slug is what the profile's index points at, so a model that read the
	// index and wants the account behind an entry has a name rather than a
	// question. Resolving it directly is both exact and free.
	if slug := types.SanitizeMemoryEpisodeSlug(query); slug != "" {
		if episode, err := s.repo.EpisodeBySlug(searchCtx, scope, slug); err == nil && episode != nil {
			s.touchEpisodesAsync(searchCtx, scope, []*types.MemoryEpisode{episode})
			searchSpan.Finish(map[string]interface{}{
				"outcome":    "ok",
				"subject_id": scope.SubjectID,
				"mode":       "slug",
				"matched":    1,
			}, nil, nil)
			return interfaces.MemorySearchResult{
				Available: true,
				Episodes:  []*types.MemoryEpisode{episode},
			}
		}
	}

	matched, rankTrace := s.searchEpisodes(searchCtx, scope, cfg, query, limit)

	// A searched account was read by the model just as surely as an injected
	// one, so it counts as used. Without this the accounts only reachable
	// through search would look permanently unused, drop out of the profile's
	// index, and eventually be pruned — the store would forget precisely what
	// was being used most deliberately.
	s.touchEpisodesAsync(searchCtx, scope, matched)

	logger.Infof(searchCtx,
		"memory: search done subject=%s matched=%d mode=%s",
		scope.SubjectID, len(matched), rankTrace.Mode)
	searchSpan.Finish(map[string]interface{}{
		"outcome":      "ok",
		"subject_id":   scope.SubjectID,
		"vector_hits":  rankTrace.VectorHits,
		"vector_skip":  rankTrace.VectorSkipReason,
		"ranking_mode": rankTrace.Mode,
		"matched":      len(matched),
	}, map[string]interface{}{
		"tenant_id": scope.TenantID,
	}, nil)

	return interfaces.MemorySearchResult{Available: true, Episodes: matched}
}
