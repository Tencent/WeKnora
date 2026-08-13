package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	// consolidateInterval is the minimum wait between whole-store reviews. This
	// is maintenance, not a feature the user is waiting on, and every run costs
	// a model call, so it is deliberately infrequent.
	consolidateInterval = 24 * time.Hour
	// forcedConsolidateInterval is the floor between two reviews one person
	// asks for. Short enough that a real retry — fix the model, press again —
	// is not blocked, long enough that the button cannot be scripted into a
	// stream of model calls.
	forcedConsolidateInterval = time.Minute
	// consolidateMinItems is the store size below which there is nothing worth
	// reviewing: a handful of memories cannot have drifted into contradiction.
	consolidateMinItems = 6
	// consolidateMaxClusters bounds the model calls one review makes. A person
	// who asked for the review is waiting on it and can afford a few more.
	consolidateMaxClusters = 3
	forcedMaxClusters      = 8
	// consolidateMinOverlap is how much wording two memories must share before
	// the daily pass spends a model call comparing them. Unattended runs are
	// budget-limited rather than careful: being wrong here costs nothing but a
	// call, because the model still decides.
	consolidateMinOverlap = 0.55
	// forcedMinOverlap is the same bar for a review someone asked for.
	//
	// Low on purpose. Candidate selection is recall, not judgement — every
	// group goes to the model, which answers with an empty statement when the
	// records turn out to be different things. A strict bar only hides pairs
	// from the one component able to tell them apart: "我叫wizard，我是一个画家"
	// against "职业：我叫wizardchen，我是一个作家" shares 0.50 of its tokens and
	// so never reached the model, yet resolving exactly that contradiction is
	// why someone presses the button.
	forcedMinOverlap = 0.3
	// consolidateMinCosine and forcedMinCosine are the same two bars for
	// memories that were embedded. Wording overlap cannot see that "喜欢用 Go"
	// and "偏好 Golang 开发" are one preference; the vectors already stored for
	// recall can, at no extra model call.
	consolidateMinCosine = 0.86
	forcedMinCosine      = 0.75
	// staleTaskAge is how long a task can go unmentioned before it stops
	// competing for space. "I'm refactoring payments this week" is worth
	// recalling this week and misleading three months later.
	staleTaskAge = 45 * 24 * time.Hour
)

// consolidateIfDue reviews the whole store for one subject, at most once a day.
//
// Distillation only ever looks at the newest conversation, which is the right
// scope for a single turn and the wrong scope for noticing that five turns
// across three weeks have recorded the same preference five slightly different
// ways, or that a task from last quarter is now just noise. Both Generative
// Agents' reflection step and MemoryOS's segmented store treat this offline
// pass as a separate stage for the same reason: no per-turn call can see it.
//
// It never runs on the request path.
func (s *Service) consolidateIfDue(
	ctx context.Context, scope interfaces.MemoryScope, cfg *types.MemoryConfig, modelID string,
) {
	s.reviewStore(ctx, scope, cfg, modelID, false)
}

// ConsolidateNow reviews the caller's store immediately, without waiting for
// the daily maintenance pass that rides along on distillation.
func (s *Service) ConsolidateNow(ctx context.Context) (*types.MemoryConsolidationResult, error) {
	scope, cfg, ok := s.enabledScope(ctx)
	if !ok {
		return nil, ErrMemoryDisabled
	}
	if _, err := s.repo.EnsureSubject(ctx, scope); err != nil {
		return nil, err
	}
	modelID := s.extractionModelID(ctx, cfg, types.MemoryExtractPayload{})
	return s.reviewStore(ctx, scope, cfg, modelID, true), nil
}

func (s *Service) reviewStore(
	ctx context.Context,
	scope interfaces.MemoryScope,
	cfg *types.MemoryConfig,
	modelID string,
	force bool,
) *types.MemoryConsolidationResult {
	result := &types.MemoryConsolidationResult{}
	subject, err := s.repo.GetSubject(ctx, scope)
	if err != nil || subject == nil {
		if err != nil {
			logger.Warnf(ctx, "memory: load subject for consolidation failed: %v", err)
		}
		return result
	}
	// Two clocks, because the two callers are rate limited for different
	// reasons and must not silence each other. The daily pass is maintenance
	// nobody asked for, so it waits a day. A review someone pressed a button
	// for waits only long enough that the button cannot be held down: the
	// endpoint is Viewer-level and each press is worth up to forcedMaxClusters
	// model calls, which a store the model keeps declining would repeat
	// indefinitely.
	last, interval := subject.ConsolidatedAt, consolidateInterval
	if force {
		last, interval = subject.ForcedConsolidatedAt, forcedConsolidateInterval
	}
	if last != nil && time.Since(*last) < interval {
		if force {
			result.Skipped = types.MemoryConsolidationSkipTooSoon
		}
		return result
	}
	if force {
		if err := s.repo.MarkForcedConsolidated(ctx, scope); err != nil {
			logger.Warnf(ctx, "memory: mark forced consolidation failed: %v", err)
		}
	}

	// Expiry first: an expired task should not be a merge candidate.
	if archived, err := s.repo.ExpireOverdue(ctx, scope); err != nil {
		logger.Warnf(ctx, "memory: expire overdue failed: %v", err)
	} else {
		result.Expired = int(archived)
		if archived > 0 {
			logger.Infof(ctx, "memory: archived %d expired memories for %s", archived, scope.SubjectID)
		}
	}

	// The whole store, not a page of it. A fixed 200 used to bound this, which
	// meant two duplicates that were both old could never meet: the pass only
	// ever saw the newest rows, and consolidation is the one stage whose job is
	// to notice things that drifted apart over weeks.
	//
	// Reading more costs no model calls. Candidate selection is local — token
	// overlap plus the vectors recall already stored — and the number of groups
	// actually put to a model is capped separately by maxClusters.
	items, _, err := s.repo.ListItems(ctx, scope, types.MemoryStatusActive, cfg.EffectiveMaxItems(), 0)
	if err != nil {
		logger.Warnf(ctx, "memory: consolidation list failed: %v", err)
		return result
	}

	result.Reviewed = len(items)
	result.Demoted = s.demoteStaleTasks(ctx, scope, items)
	if force || len(items) >= consolidateMinItems {
		result.Merged, result.Candidates, result.Skipped =
			s.mergeRedundant(ctx, scope, cfg, modelID, items, force)
	} else {
		result.Skipped = types.MemoryConsolidationSkipTooFewItems
	}

	// Vectors for anything written before an embedding model existed, or while
	// it was unreachable. Bounded per run, so a large backlog drains over days
	// instead of stalling one maintenance pass.
	s.backfillEmbeddings(ctx, scope, cfg)

	if err := s.repo.MarkConsolidated(ctx, scope); err != nil {
		logger.Warnf(ctx, "memory: mark consolidated failed: %v", err)
	}
	if result.Merged > 0 || result.Demoted > 0 || result.Expired > 0 {
		s.rebuildBlock(ctx, scope)
	}
	// A review that does nothing is the common case, and until it says why it
	// is indistinguishable from one that is broken.
	if force || result.Merged > 0 || result.Demoted > 0 || result.Expired > 0 {
		logger.Infof(ctx,
			"memory: consolidation reviewed %d, candidates %d, merged %d, demoted %d, expired %d, skipped=%q for %s",
			result.Reviewed, result.Candidates, result.Merged, result.Demoted, result.Expired,
			result.Skipped, scope.SubjectID)
	}
	return result
}

// demoteStaleTasks lowers the importance of tasks nobody has mentioned in
// months.
//
// Deleting them would be wrong — the user never said they finished, and we do
// not delete what we were told. Lowering importance is enough: it drops them
// out of the resident block and makes them the first to go when the store hits
// its cap, while leaving them visible and explainable in the memory manager.
func (s *Service) demoteStaleTasks(
	ctx context.Context, scope interfaces.MemoryScope, items []*types.MemoryItem,
) int {
	cutoff := time.Now().Add(-staleTaskAge)
	demoted := 0
	for _, item := range items {
		if item == nil || item.Kind != types.MemoryKindTask || item.Importance <= 1 {
			continue
		}
		last := item.ValidFrom
		if item.LastUsedAt != nil && item.LastUsedAt.After(last) {
			last = *item.LastUsedAt
		}
		if last.After(cutoff) {
			continue
		}
		err := s.repo.UpdateItemContent(ctx, scope, item.ID, item.Content, item.NormalizedKey, 1)
		if err != nil {
			logger.Warnf(ctx, "memory: demote stale task failed: %v", err)
			continue
		}
		demoted++
	}
	return demoted
}

// mergeRedundant folds groups of near-duplicate memories into one statement.
//
// candidates is how many groups were found, which is not len(clusters) after
// capping — it is what tells a caller whether an empty result means "nothing
// looked alike" or "the model said no".
func (s *Service) mergeRedundant(
	ctx context.Context,
	scope interfaces.MemoryScope,
	cfg *types.MemoryConfig,
	modelID string,
	items []*types.MemoryItem,
	force bool,
) (merged int, candidates int, skipped string) {
	minOverlap, minCosine := consolidateMinOverlap, consolidateMinCosine
	maxClusters := consolidateMaxClusters
	if force {
		minOverlap, minCosine = forcedMinOverlap, forcedMinCosine
		maxClusters = forcedMaxClusters
	}

	clusters := s.mergeCandidates(ctx, scope, cfg, items, minOverlap, minCosine)
	candidates = len(clusters)
	if candidates == 0 {
		return 0, 0, types.MemoryConsolidationSkipNoCandidates
	}
	if len(clusters) > maxClusters {
		clusters = clusters[:maxClusters]
	}

	declined := 0
	for _, cluster := range clusters {
		statement, unavailable := s.callConsolidationModel(ctx, modelID, cluster)
		if unavailable {
			// Only the model may decide that two memories say the same thing.
			// Merging on token overlap alone would supersede wordings the user
			// gave us on the strength of a heuristic that was never meant to
			// judge, so the review stops here and reports why.
			return merged, candidates, types.MemoryConsolidationSkipModelUnavailable
		}
		statement = types.SanitizeMemoryContent(statement)
		if statement == "" {
			declined++
			continue
		}
		primary := cluster[0]
		replacement, err := s.write(ctx, scope, cfg, types.MemoryItem{
			Kind:            primary.Kind,
			Topic:           primary.Topic,
			Content:         statement,
			Importance:      primary.Importance,
			Origin:          primary.Origin,
			SourceSessionID: primary.SourceSessionID,
			SourceMessageID: primary.SourceMessageID,
		})
		if err != nil || replacement == nil {
			if err != nil {
				logger.Warnf(ctx, "memory: consolidation write failed: %v", err)
			}
			continue
		}
		// Supersede rather than delete: the old wording keeps its dates, so the
		// memory manager can still explain what this statement used to be and
		// when it changed.
		for _, item := range cluster {
			if item.ID == replacement.ID {
				continue
			}
			if err := s.repo.SupersedeItem(ctx, scope, item.ID, replacement.ID); err != nil {
				logger.Warnf(ctx, "memory: supersede during consolidation failed: %v", err)
			}
		}
		merged++
	}
	if merged == 0 && declined > 0 {
		return 0, candidates, types.MemoryConsolidationSkipModelDeclined
	}
	return merged, candidates, ""
}

// mergeCandidates groups memories that might be saying the same thing.
//
// Two signals, either of which is enough. Wording overlap catches restatements
// of the same sentence; cosine over the vectors already stored for recall
// catches the same claim said differently, which no amount of token counting
// can. Neither decides anything — every group is put to the model.
func (s *Service) mergeCandidates(
	ctx context.Context,
	scope interfaces.MemoryScope,
	cfg *types.MemoryConfig,
	items []*types.MemoryItem,
	minOverlap, minCosine float64,
) [][]*types.MemoryItem {
	// Sets rather than slices: clustering compares every surviving pair, so a
	// store at the 2000-item cap runs this predicate a couple of million times
	// and rebuilding both token sets inside each call is what would make
	// reviewing the whole store too slow to do on a request.
	tokens := make(map[string]map[string]struct{}, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		tokens[item.ID] = tokenSet(tokenize(item.Topic + " " + item.Content))
	}
	vectors := s.storedVectors(ctx, scope, cfg, items)

	return clusterBy(items, func(a, b *types.MemoryItem) bool {
		if jaccardSets(tokens[a.ID], tokens[b.ID]) >= minOverlap {
			return true
		}
		va, vb := vectors[a.ID], vectors[b.ID]
		if len(va) == 0 || len(vb) == 0 {
			return false
		}
		return types.CosineSimilarity(va, vb) >= minCosine
	})
}

// storedVectors loads the vectors these memories already have.
//
// It never embeds anything: a review walks the whole store, and embedding it
// on every pass would cost far more than the merges are worth. A memory
// without a vector is simply matched on wording alone until the backfill at
// the end of the review catches it.
func (s *Service) storedVectors(
	ctx context.Context,
	scope interfaces.MemoryScope,
	cfg *types.MemoryConfig,
	items []*types.MemoryItem,
) map[string][]float32 {
	modelID, ok := s.embedder(ctx, cfg)
	if !ok {
		return nil
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		if item != nil && item.ID != "" {
			ids = append(ids, item.ID)
		}
	}
	vectors, err := s.repo.ItemEmbeddings(ctx, scope, ids, modelID)
	if err != nil {
		logger.Warnf(ctx, "memory: load embeddings for consolidation failed: %v", err)
		return nil
	}
	return vectors
}

// clusterSimilar groups memories of the same kind whose wording says nearly the
// same thing, at the bar an unattended pass uses.
func clusterSimilar(items []*types.MemoryItem) [][]*types.MemoryItem {
	return clusterBy(items, func(a, b *types.MemoryItem) bool {
		return jaccard(
			tokenize(a.Topic+" "+a.Content),
			tokenize(b.Topic+" "+b.Content),
		) >= consolidateMinOverlap
	})
}

// clusterBy groups memories of the same kind that same reports as one thing.
// Groups of one are not returned.
func clusterBy(
	items []*types.MemoryItem, same func(a, b *types.MemoryItem) bool,
) [][]*types.MemoryItem {
	var clusters [][]*types.MemoryItem
	taken := make(map[string]bool, len(items))

	for i, item := range items {
		if item == nil || taken[item.ID] {
			continue
		}
		group := []*types.MemoryItem{item}
		for _, other := range items[i+1:] {
			if other == nil || taken[other.ID] || other.Kind != item.Kind {
				continue
			}
			if !same(item, other) {
				continue
			}
			group = append(group, other)
			taken[other.ID] = true
		}
		if len(group) < 2 {
			continue
		}
		taken[item.ID] = true
		clusters = append(clusters, group)
	}
	return clusters
}

// jaccard is the overlap between two token lists.
func jaccard(a, b []string) float64 {
	return jaccardSets(tokenSet(a), tokenSet(b))
}

// tokenSet deduplicates tokens so repeated comparisons of the same memory do
// not rebuild its side of the overlap each time.
func tokenSet(tokens []string) map[string]struct{} {
	set := make(map[string]struct{}, len(tokens))
	for _, token := range tokens {
		set[token] = struct{}{}
	}
	return set
}

// jaccardSets is the overlap between two prepared token sets.
func jaccardSets(a, b map[string]struct{}) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	// Walk the smaller side; the result is symmetric either way.
	if len(b) < len(a) {
		a, b = b, a
	}
	shared := 0
	for token := range a {
		if _, ok := b[token]; ok {
			shared++
		}
	}
	union := len(a) + len(b) - shared
	if union == 0 {
		return 0
	}
	return float64(shared) / float64(union)
}

const consolidationSystemPrompt = `你在整理一个人的长期记忆。下面几条记录说的是同一件事，请合并成一条。

规则：
- 只用这些记录里已有的信息，不要补充、不要推测。
- 如果它们互相矛盾，以日期最新的一条为准。
- 保留最具体的细节（具体的名称、数字、版本），丢掉重复的说法。
- 用记录本身的语言，一句话，不超过 60 字。
- 只输出 JSON：{"statement":"合并后的一句话"}
- 如果这些记录其实不是同一件事，输出 {"statement":""}。`

var consolidationSchema = json.RawMessage(`{
  "type": "object",
  "properties": {"statement": {"type": "string"}},
  "required": ["statement"]
}`)

// callConsolidationModel asks the model to merge one cluster.
func (s *Service) callConsolidationModel(
	ctx context.Context, modelID string, cluster []*types.MemoryItem,
) (statement string, unavailable bool) {
	if modelID == "" || s.modelService == nil {
		return "", true
	}
	chatModel, err := s.modelService.GetChatModel(ctx, modelID)
	if err != nil || chatModel == nil {
		logger.Warnf(ctx, "memory: consolidation model unavailable: %v", err)
		return "", true
	}

	var b strings.Builder
	for _, item := range cluster {
		b.WriteString(fmt.Sprintf("- (%s) %s\n",
			item.ValidFrom.Format("2006-01-02"), types.SanitizeMemoryContent(item.Content)))
	}

	// Thinking off, for the reason given on completeExtraction: a reasoning
	// model spends this whole budget on its own deliberation and returns
	// nothing, which here would silently skip every merge.
	thinking := false
	response, err := chatModel.Chat(ctx, []chat.Message{
		{Role: "system", Content: consolidationSystemPrompt},
		{Role: "user", Content: b.String()},
	}, &chat.ChatOptions{
		Temperature:         0,
		MaxCompletionTokens: 600,
		Thinking:            &thinking,
		Format:              consolidationSchema,
	})
	if err != nil || response == nil {
		logger.Warnf(ctx, "memory: consolidation call failed: %v", err)
		return "", true
	}

	content := strings.TrimSpace(response.Content)
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start < 0 || end <= start {
		return "", false
	}
	var parsed struct {
		Statement string `json:"statement"`
	}
	if err := json.Unmarshal([]byte(content[start:end+1]), &parsed); err != nil {
		return "", false
	}
	return strings.TrimSpace(parsed.Statement), false
}
