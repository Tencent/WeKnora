package memory

import (
	"context"
	"sort"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	// embedTimeout bounds the query-side embedding call.
	//
	// Recall sits in front of every answer, and before this it made no model
	// call at all. Semantic matching is worth a fraction of a turn; it is not
	// worth a turn that hangs because an embedding endpoint is wedged. On
	// timeout recall silently falls back to lexical matching, which is exactly
	// the behaviour that existed before.
	embedTimeout = 2 * time.Second
	// embedWriteTimeout bounds the write-side call. Writes are already off the
	// response path, so this can be more generous.
	embedWriteTimeout = 10 * time.Second
	// rrfK is the reciprocal-rank-fusion constant. 60 is the value from the
	// original TREC work and the one most systems use; Graphiti uses 1, which
	// sharpens the top of the list at the cost of ignoring almost everything
	// below it. With candidate sets this small, the standard value keeps
	// agreement between the two rankings meaningful.
	rrfK = 60.0
	// minCosine is the floor below which a vector match is not a match.
	//
	// Without it every memory that has a vector enters the ranking, including
	// the ones scoring zero, and fusion then pulls them into the prompt — the
	// feature would go from "cannot find a re-worded memory" straight to
	// "recalls everything". Graphiti holds its equivalent at 0.6; this sits
	// slightly lower because the lexical ranking is fused in alongside and can
	// still rescue an exact-term match the model embedded poorly.
	minCosine = 0.5
	// vectorCandidateCap bounds how many stored vectors one recall loads.
	vectorCandidateCap = 400
	// backfillPerRun is how many missing vectors one maintenance pass fills.
	backfillPerRun = 50
)

// embedder resolves the embedding model pinned on this workspace.
//
// Memory is one vector space per workspace. Knowledge bases each bind their
// own embedding model, so there is no "the workspace embedding model" to fall
// back to — picking the first listed one would silently mix incomparable
// spaces as models are added or deleted. Blank means semantic recall is off.
func (s *Service) embedder(_ context.Context, cfg *types.MemoryConfig) (string, bool) {
	if cfg == nil || !cfg.VectorRecallEnabled() || s.modelService == nil {
		return "", false
	}
	if cfg.EmbeddingModelID == "" {
		return "", false
	}
	return cfg.EmbeddingModelID, true
}

// embedText produces one vector, bounded and non-fatal.
func (s *Service) embedText(
	ctx context.Context, modelID, text string, timeout time.Duration,
) []float32 {
	if modelID == "" || text == "" || s.modelService == nil {
		return nil
	}
	embedder, err := s.modelService.GetEmbeddingModel(ctx, modelID)
	if err != nil || embedder == nil {
		logger.Warnf(ctx, "memory: embedding model %s unavailable: %v", modelID, err)
		return nil
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	vector, err := embedder.Embed(callCtx, text)
	if err != nil {
		logger.Warnf(ctx, "memory: embed failed: %v", err)
		return nil
	}
	return vector
}

// storeItemEmbedding records the vector for one memory. Best effort: a memory
// without a vector is still a memory, it is just invisible to semantic recall
// until the backfill catches it.
func (s *Service) storeItemEmbedding(
	ctx context.Context, scope interfaces.MemoryScope, cfg *types.MemoryConfig, item *types.MemoryItem,
) {
	if item == nil {
		return
	}
	modelID, ok := s.embedder(ctx, cfg)
	if !ok {
		return
	}
	text := embeddableText(item, s.embedAliases(ctx, scope, item))
	vector := s.embedText(ctx, modelID, text, embedWriteTimeout)
	if len(vector) == 0 {
		return
	}
	err := s.repo.UpsertItemEmbedding(ctx, scope, &types.MemoryItemEmbedding{
		ItemID:  item.ID,
		ModelID: modelID,
		Dims:    len(vector),
		Vector:  types.EncodeEmbedding(vector),
	})
	if err != nil {
		logger.Warnf(ctx, "memory: store embedding failed: %v", err)
	}
}

// embeddableText is what gets embedded for a memory.
//
// Topic and content together, because the topic carries the subject the
// statement is about and the statement alone is often too terse to place —
// "PostgreSQL 17" means little without "生产数据库".
//
// An interest is promoted from a subject label, so its topic and content are
// the same string. Joining them would embed "X：X", which is not the sentence
// any question resembles.
//
// aliases are the other wordings this person has used for the same subject.
// They widen what a question can match without widening what the model is
// told: they exist only in the vector, never in the injected block.
func embeddableText(item *types.MemoryItem, aliases []string) string {
	if item == nil {
		return ""
	}
	topic := types.SanitizeMemoryTopic(item.Topic)
	content := types.SanitizeMemoryContent(item.Content)
	text := content
	if topic != "" && topic != content {
		text = topic + "：" + content
	}
	if text == "" {
		return ""
	}
	seen := map[string]bool{text: true, content: true, topic: true}
	for _, alias := range aliases {
		alias = types.SanitizeMemoryTopic(alias)
		if alias == "" || seen[alias] {
			continue
		}
		seen[alias] = true
		text += "；" + alias
	}
	return text
}

// embedAliases returns the other wordings this person has used for an
// interest's subject.
//
// Only interests: every other kind already carries a sentence of its own, and
// its topic is a heading rather than a subject the topic tracker follows. Best
// effort — a lookup failure costs a slightly narrower vector, nothing else.
func (s *Service) embedAliases(
	ctx context.Context, scope interfaces.MemoryScope, item *types.MemoryItem,
) []string {
	if item == nil || item.Kind != types.MemoryKindInterest {
		return nil
	}
	key := types.NormalizeTopicKey(item.Topic)
	if key == "" {
		return nil
	}
	stat, err := s.repo.TopicByKey(ctx, scope, key)
	if err != nil {
		logger.Warnf(ctx, "memory: load topic aliases failed: %v", err)
		return nil
	}
	if stat == nil {
		return nil
	}
	return stat.Aliases
}

// vectorRanking scores candidates against a query by cosine similarity and
// returns them best-first. An empty result means semantic scoring was
// unavailable, not that nothing matched — callers fall back rather than
// treating it as an empty match set. skipReason is set when vector recall was
// not attempted or could not run.
func (s *Service) vectorRanking(
	ctx context.Context,
	scope interfaces.MemoryScope,
	cfg *types.MemoryConfig,
	query string,
	candidates []*types.MemoryItem,
) ([]int, string) {
	if len(candidates) == 0 {
		return nil, "no_candidates"
	}
	modelID, ok := s.embedder(ctx, cfg)
	if !ok {
		return nil, "vector_disabled"
	}
	queryVector := s.embedText(ctx, modelID, query, embedTimeout)
	if len(queryVector) == 0 {
		return nil, "embed_failed"
	}

	ids := make([]string, 0, len(candidates))
	indexByID := make(map[string]int, len(candidates))
	for i, item := range candidates {
		if item == nil || item.ID == "" {
			continue
		}
		ids = append(ids, item.ID)
		indexByID[item.ID] = i
		if len(ids) >= vectorCandidateCap {
			break
		}
	}
	vectors, err := s.repo.ItemEmbeddings(ctx, scope, ids, modelID)
	if err != nil {
		logger.Warnf(ctx, "memory: load embeddings failed: %v", err)
		return nil, "load_embeddings_failed"
	}
	if len(vectors) == 0 {
		return nil, "no_stored_vectors"
	}

	type scored struct {
		index int
		score float64
	}
	ranked := make([]scored, 0, len(vectors))
	for _, id := range ids {
		vector, ok := vectors[id]
		if !ok {
			continue
		}
		similarity := types.CosineSimilarity(queryVector, vector)
		if similarity < minCosine {
			continue
		}
		ranked = append(ranked, scored{index: indexByID[id], score: similarity})
	}
	sortScoredDesc(ranked, func(i int) float64 { return ranked[i].score })

	out := make([]int, 0, len(ranked))
	for _, entry := range ranked {
		out = append(out, entry.index)
	}
	if len(out) == 0 {
		return nil, "below_similarity_threshold"
	}
	return out, ""
}

// fuseRankings combines two ranked id lists by reciprocal rank fusion.
//
// RRF rather than a weighted score sum because the two signals are not on a
// comparable scale: cosine is bounded and calibrated, the lexical score is a
// bag-of-ngrams overlap count that means nothing in absolute terms. Fusing
// ranks sidesteps the question entirely, and an item both signals agree on
// beats one that only a single signal likes.
func fuseRankings(lexical, vector []int) []int {
	scores := make(map[int]float64, len(lexical)+len(vector))
	order := make([]int, 0, len(lexical)+len(vector))
	seen := make(map[int]struct{}, len(lexical)+len(vector))

	for _, list := range [][]int{lexical, vector} {
		for rank, index := range list {
			scores[index] += 1.0 / (rrfK + float64(rank))
			if _, dup := seen[index]; !dup {
				seen[index] = struct{}{}
				order = append(order, index)
			}
		}
	}

	sortStableByIndexScore(order, func(index int) float64 { return scores[index] })
	return order
}

// backfillEmbeddings fills in vectors for memories written before an embedding
// model was available. Bounded per run; the daily maintenance pass calls it, so
// a large backlog drains over days rather than in one burst.
func (s *Service) backfillEmbeddings(
	ctx context.Context, scope interfaces.MemoryScope, cfg *types.MemoryConfig,
) int {
	modelID, ok := s.embedder(ctx, cfg)
	if !ok {
		return 0
	}
	items, err := s.repo.ItemsMissingEmbeddings(ctx, scope, modelID, backfillPerRun)
	if err != nil {
		logger.Warnf(ctx, "memory: find items missing embeddings failed: %v", err)
		return 0
	}
	filled := 0
	for _, item := range items {
		text := embeddableText(item, s.embedAliases(ctx, scope, item))
		vector := s.embedText(ctx, modelID, text, embedWriteTimeout)
		if len(vector) == 0 {
			// The model just failed; the rest of this batch will fail too.
			break
		}
		err := s.repo.UpsertItemEmbedding(ctx, scope, &types.MemoryItemEmbedding{
			ItemID:  item.ID,
			ModelID: modelID,
			Dims:    len(vector),
			Vector:  types.EncodeEmbedding(vector),
		})
		if err != nil {
			logger.Warnf(ctx, "memory: backfill embedding failed: %v", err)
			continue
		}
		filled++
	}
	if filled > 0 {
		logger.Infof(ctx, "memory: backfilled %d embeddings for %s", filled, scope.SubjectID)
	}
	return filled
}

// sortScoredDesc sorts in place, highest score first.
func sortScoredDesc[T any](items []T, score func(int) float64) {
	sort.SliceStable(items, func(i, j int) bool { return score(i) > score(j) })
}

// sortStableByIndexScore sorts in place, highest score first, preserving the
// original order among ties so a stable input produces a stable output.
func sortStableByIndexScore(indexes []int, score func(int) float64) {
	sort.SliceStable(indexes, func(i, j int) bool {
		return score(indexes[i]) > score(indexes[j])
	})
}
