package memory

import (
	"context"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	// episodeRecallMinScore is the cosine floor for pulling a past
	// conversation into a turn uninvited.
	//
	// Higher than the floor the search tool uses, because the two cases are
	// not symmetric. A tool call is the model asking, and a weak answer to a
	// question it asked costs it a moment's reading. An excerpt injected into
	// a turn nobody asked about is the memory feature volunteering, and a
	// loosely related conversation volunteered on every turn is exactly the
	// behaviour that makes people switch memory off.
	//
	// That argument sets the ordering, not the number. It was 0.62, which was
	// picked without measuring anything and is a high bar for an embedding
	// model asked to match "上次那个导入为什么失败" against an account titled
	// "知识库批量入库报 413" — the paraphrase case recall exists for. What a
	// wrong guess costs is bounded and asymmetric: a loose match spends at
	// most MemoryRecallMaxExcerpts × MemoryRecallExcerptRunes of a turn's
	// budget on something the model can disregard, while a missed one leaves
	// the person's own history unreachable for a question that was plainly
	// about it. Codex does not gate its read path on similarity at all.
	episodeRecallMinScore = 0.50
	// episodeSearchMinScore is the floor when the model asked for it. Lower
	// for the reason above, reversed: it can judge a weak match itself.
	episodeSearchMinScore = 0.45
	// episodeRecallCandidates is how many accounts are scored before the cap
	// is applied. A few more than are kept, so the cap chooses among real
	// alternatives rather than taking whatever came back first.
	episodeRecallCandidates = 6
	// episodeLexicalCandidates bounds the fallback scan when there is no
	// embedding model configured.
	episodeLexicalCandidates = 60
	// episodeTouchTimeout bounds the off-path usage write.
	episodeTouchTimeout = 5 * time.Second
)

// matchEpisodes finds the past conversations this question is about.
//
// Semantic first, because that is the only method that works on the question
// people actually ask — "上次那个导入为什么失败" shares no tokens with an
// account titled "知识库批量入库报 413". Lexical matching is the fallback for a
// deployment with no embedding model, and it is a fallback rather than a
// supplement: unlike the item store this replaces, there is nothing to fuse
// two rankings over, because an account is long enough that token overlap
// measures its length more than its relevance.
func (s *Service) matchEpisodes(
	ctx context.Context, scope interfaces.MemoryScope, cfg *types.MemoryConfig, query string,
) ([]*types.MemoryEpisode, recallRankingTrace) {
	trace := recallRankingTrace{}
	query = strings.TrimSpace(query)
	if query == "" {
		trace.Mode = "no_query"
		return nil, trace
	}

	modelID, ok := s.embedder(ctx, cfg)
	if !ok {
		trace.VectorSkipReason = "vector_disabled"
		matched := s.lexicalEpisodes(ctx, scope, query)
		trace.Mode = "lexical_only"
		trace.LexicalHits = len(matched)
		trace.Matched = len(matched)
		return matched, trace
	}

	vector := s.embedText(ctx, modelID, query, embedTimeout)
	if len(vector) == 0 {
		trace.VectorSkipReason = "embed_failed"
		matched := s.lexicalEpisodes(ctx, scope, query)
		trace.Mode = "lexical_only"
		trace.LexicalHits = len(matched)
		trace.Matched = len(matched)
		return matched, trace
	}

	hits, err := s.repo.SearchEpisodesByVector(ctx, scope, interfaces.MemoryVectorQuery{
		ModelID:  modelID,
		Vector:   vector,
		MinScore: episodeRecallMinScore,
		Limit:    episodeRecallCandidates,
	})
	if err != nil {
		logger.Warnf(ctx, "memory: episode search failed: %v", err)
		trace.VectorSkipReason = "search_failed"
		trace.Mode = "no_matches"
		return nil, trace
	}
	trace.VectorHits = len(hits)
	trace.Mode = "semantic"

	matched := make([]*types.MemoryEpisode, 0, types.MemoryRecallMaxExcerpts)
	for _, hit := range hits {
		if hit.Episode == nil {
			continue
		}
		matched = append(matched, hit.Episode)
		if len(matched) >= types.MemoryRecallMaxExcerpts {
			break
		}
	}
	if len(matched) == 0 {
		trace.Mode = "no_matches"
	}
	trace.Matched = len(matched)
	return matched, trace
}

// searchEpisodes answers a deliberate lookup, as opposed to matchEpisodes,
// which decides what to volunteer.
//
// The floor is lower and the cap is higher, because the model asked. A weak
// match costs it a moment's reading and it can judge relevance itself, which
// is not true of anything injected into a turn unasked.
func (s *Service) searchEpisodes(
	ctx context.Context,
	scope interfaces.MemoryScope,
	cfg *types.MemoryConfig,
	query string,
	limit int,
) ([]*types.MemoryEpisode, recallRankingTrace) {
	trace := recallRankingTrace{}
	modelID, ok := s.embedder(ctx, cfg)
	if !ok {
		trace.VectorSkipReason = "vector_disabled"
		trace.Mode = "lexical_only"
		matched := s.lexicalEpisodes(ctx, scope, query)
		trace.Matched = len(matched)
		return matched, trace
	}
	vector := s.embedText(ctx, modelID, query, embedTimeout)
	if len(vector) == 0 {
		trace.VectorSkipReason = "embed_failed"
		trace.Mode = "lexical_only"
		matched := s.lexicalEpisodes(ctx, scope, query)
		trace.Matched = len(matched)
		return matched, trace
	}
	hits, err := s.repo.SearchEpisodesByVector(ctx, scope, interfaces.MemoryVectorQuery{
		ModelID:  modelID,
		Vector:   vector,
		MinScore: episodeSearchMinScore,
		Limit:    limit,
	})
	if err != nil {
		logger.Warnf(ctx, "memory: episode search failed: %v", err)
		trace.VectorSkipReason = "search_failed"
		trace.Mode = "no_matches"
		return nil, trace
	}
	trace.VectorHits = len(hits)
	trace.Mode = "semantic"
	matched := make([]*types.MemoryEpisode, 0, len(hits))
	for _, hit := range hits {
		if hit.Episode != nil {
			matched = append(matched, hit.Episode)
		}
	}
	if len(matched) == 0 {
		trace.Mode = "no_matches"
	}
	trace.Matched = len(matched)
	return matched, trace
}

// lexicalEpisodes is the no-embedding-model fallback.
//
// Matching is on the title and the slug rather than on the account text.
// Those are the handles a later search would use; scoring against three
// paragraphs of narrative would rank the longest account first on any query.
// Keywords, when an older account still has them, ride along.
func (s *Service) lexicalEpisodes(
	ctx context.Context, scope interfaces.MemoryScope, query string,
) []*types.MemoryEpisode {
	episodes, _, err := s.repo.ListEpisodes(ctx, scope, episodeLexicalCandidates, 0)
	if err != nil {
		logger.Warnf(ctx, "memory: list accounts for lexical recall failed: %v", err)
		return nil
	}
	type scored struct {
		episode *types.MemoryEpisode
		score   int
	}
	terms := tokenize(query)
	if len(terms) == 0 {
		return nil
	}
	ranked := make([]scored, 0, len(episodes))
	for _, episode := range episodes {
		if episode == nil {
			continue
		}
		haystack := strings.ToLower(episode.Title + " " + episode.Slug + " " + strings.Join(episode.Keywords, " "))
		score := 0
		for _, term := range terms {
			if strings.Contains(haystack, term) {
				score++
			}
		}
		if score > 0 {
			ranked = append(ranked, scored{episode: episode, score: score})
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].score > ranked[j].score })
	matched := make([]*types.MemoryEpisode, 0, types.MemoryRecallMaxExcerpts)
	for _, row := range ranked {
		matched = append(matched, row.episode)
		if len(matched) >= types.MemoryRecallMaxExcerpts {
			break
		}
	}
	return matched
}

// tokenize splits text the same way NormalizeMemoryKey does, so a question and
// an account's keywords are compared on the same alphabet. CJK is split per
// ideograph because it has no word separators; everything else splits on
// non-alphanumeric.
func tokenize(text string) []string {
	var tokens []string
	var current strings.Builder
	flush := func() {
		if current.Len() > 0 {
			tokens = append(tokens, current.String())
			current.Reset()
		}
	}
	for _, r := range strings.ToLower(text) {
		switch {
		case unicode.Is(unicode.Han, r):
			flush()
			tokens = append(tokens, string(r))
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			current.WriteRune(r)
		default:
			flush()
		}
	}
	flush()
	return tokens
}

// touchEpisodesAsync records that these accounts were asked for, off the
// request path. WithoutCancel keeps it alive after the handler returns, which
// is the whole point: the counter this feeds is what the store is ranked by,
// and a read that the response beat to the exit would not count.
func (s *Service) touchEpisodesAsync(
	ctx context.Context, scope interfaces.MemoryScope, episodes []*types.MemoryEpisode,
) {
	s.recordEpisodeReadAsync(ctx, scope, episodes, s.repo.TouchEpisodes)
}

// markRecalledAsync is the same write for the weaker signal: these accounts
// were relevant enough to inject, which is not the same as anything having
// gone looking for them.
func (s *Service) markRecalledAsync(
	ctx context.Context, scope interfaces.MemoryScope, episodes []*types.MemoryEpisode,
) {
	s.recordEpisodeReadAsync(ctx, scope, episodes, s.repo.MarkEpisodesRecalled)
}

func (s *Service) recordEpisodeReadAsync(
	ctx context.Context,
	scope interfaces.MemoryScope,
	episodes []*types.MemoryEpisode,
	record func(context.Context, interfaces.MemoryScope, []string) error,
) {
	if len(episodes) == 0 {
		return
	}
	ids := make([]string, 0, len(episodes))
	for _, episode := range episodes {
		if episode != nil {
			ids = append(ids, episode.ID)
		}
	}
	go func() {
		bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), episodeTouchTimeout)
		defer cancel()
		if err := record(bg, scope, ids); err != nil {
			logger.Warnf(bg, "memory: record account usage failed: %v", err)
		}
	}()
}
