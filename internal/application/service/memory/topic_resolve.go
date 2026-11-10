package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	// topicFuzzyThreshold is where character-bigram overlap alone is enough to
	// call two labels the same subject.
	//
	// Set high on purpose. Merging two topics that are not the same thing
	// corrupts the count that decides what becomes a memory, and it is
	// invisible when it happens. A missed merge only delays a promotion, and
	// the tier below catches most of them anyway. Graphiti holds its
	// deterministic tier at a comparable level for the same reason.
	topicFuzzyThreshold = 0.80
	// topicCandidateLimit bounds how many existing topics are shown to the
	// adjudicating model. One person's topic list is small; this is a guard
	// against a pathological account, not a normal working limit.
	topicCandidateLimit = 40
	// topicMaxAliases bounds the alias list on one topic.
	topicMaxAliases = 12
)

// topicResolution is where one surface form ended up.
type topicResolution struct {
	// Canonical is the existing topic this label belongs to, or nil when it is
	// genuinely a new subject.
	Canonical *types.MemoryTopicStat
	// Surface is what the model actually said, recorded as an alias when it
	// differs from the canonical label.
	Surface string
	// Tier records which rule decided, for logs and for tests that need to
	// assert an expensive tier was not reached.
	Tier string
	// MergedLabel is a better name for the merged subject, when the model
	// offered one and it passed the guard against generalising. Empty means
	// keep the label the subject already has.
	MergedLabel string
}

// resolveTopics maps the labels one extraction run produced onto the subjects
// this person already has.
//
// The problem this solves is that a model asked to name a topic will not name
// it the same way twice: "门店排班管理" one run, "店员班次安排" the
// next. Treating the string as an identity means the same subject is counted
// under several keys and never reaches the promotion threshold — the feature
// looks enabled and learns nothing.
//
// The fix is the one both mem0 and Graphiti converged on: never trust the
// surface string, resolve it against what already exists, cheapest test first.
//
//	tier 1  normalised equality, including previously recorded aliases
//	tier 2  character-bigram overlap, gated so short labels do not match loosely
//	tier 3  one batched model call over the remaining labels
//
// Tier 3 is the only one that costs anything, and it is usually skipped: the
// extraction prompt already shows the model this person's existing topics and
// asks it to reuse a label verbatim, so most runs resolve at tier 1.
func (s *Service) resolveTopics(
	ctx context.Context,
	scope interfaces.MemoryScope,
	modelID string,
	surfaces []string,
) []topicResolution {
	if len(surfaces) == 0 {
		return nil
	}
	existing, err := s.repo.TopTopics(ctx, scope, topicCandidateLimit)
	if err != nil {
		logger.Warnf(ctx, "memory: load existing topics failed: %v", err)
		existing = nil
	}

	resolutions := make([]topicResolution, 0, len(surfaces))
	var unresolved []int

	for _, surface := range surfaces {
		resolution := topicResolution{Surface: surface}
		if match := matchTopicExactly(surface, existing); match != nil {
			resolution.Canonical = match
			// Distinguish "the extraction model echoed a tracked label" from
			// "the resolver's own normalisation matched". Both look like an
			// exact hit here, but only the first is a judgement the model made
			// — and reporting that as the cheapest, most certain tier is how an
			// over-merge hides.
			if surface == match.Topic {
				resolution.Tier = "reused"
			} else {
				resolution.Tier = "exact"
			}
		} else if match := matchTopicLoosely(surface, existing); match != nil {
			resolution.Canonical = match
			resolution.Tier = "fuzzy"
		} else {
			unresolved = append(unresolved, len(resolutions))
		}
		resolutions = append(resolutions, resolution)
	}

	if len(unresolved) > 0 && len(existing) > 0 {
		s.adjudicateTopics(ctx, modelID, existing, resolutions, unresolved)
	}

	// Two labels in the same run can be the same new subject. Without this the
	// run creates two rows that every later run then has to keep apart.
	collapseNewTopicsWithinRun(resolutions)

	return resolutions
}

// matchTopicExactly is tier 1: the normalised label, or any wording that has
// already been resolved to this topic before.
func matchTopicExactly(surface string, existing []*types.MemoryTopicStat) *types.MemoryTopicStat {
	key := types.NormalizeTopicKey(surface)
	if key == "" {
		return nil
	}
	for _, stat := range existing {
		if stat == nil {
			continue
		}
		if stat.NormalizedKey == key || stat.Aliases.Has(surface) {
			return stat
		}
	}
	return nil
}

// matchTopicLoosely is tier 2: high character-bigram overlap, and only for
// labels specific enough that the overlap means something.
func matchTopicLoosely(surface string, existing []*types.MemoryTopicStat) *types.MemoryTopicStat {
	if !types.TopicIsSpecificEnoughToMatchLoosely(surface) {
		return nil
	}
	var (
		best      *types.MemoryTopicStat
		bestScore float64
	)
	for _, stat := range existing {
		if stat == nil || !types.TopicIsSpecificEnoughToMatchLoosely(stat.Topic) {
			continue
		}
		score := types.TopicSimilarity(surface, stat.Topic)
		if score > bestScore {
			best, bestScore = stat, score
		}
	}
	if bestScore < topicFuzzyThreshold {
		return nil
	}
	return best
}

const topicAdjudicationPrompt = `你在维护一个人的关注主题列表。下面给出「已有主题」和「新出现的说法」。

对每个新说法，判断它和某个已有主题**说的是不是同一件事**——注意是同一件事，不是有关系。

判为同一件事时，如果其中一个名字明显更完整、更准确，可以在 label 里给出应该保留的那个名字：
- 「CI 流水线」和「持续集成流水线」→ label 用全称「持续集成流水线」。
- 「PostgreSQL 连接池」和「PostgreSQL 连接池调优」→ label 用更具体的那个。
- 两个名字差不多好，就不要填 label。
- **绝对不要**给一个更宽泛的名字（「门店」「系统」「数据库相关」），也不要把两个名字拼起来
  （「A与B」）。名字只能变得更准确，不能变得更笼统——否则每合并一次主题就宽一点，最后变成
  一个什么都装的桶。

算同一件事：
- 同义、换个说法、详略不同的同一件事：「店员班次安排」和「门店排班管理」。
- 加了个无关紧要的限定词：「PostgreSQL 连接池」和「PostgreSQL 连接池问题」。

不算同一件事：
- 同一领域里的不同问题：「PostgreSQL 连接池」和「PostgreSQL 备份恢复」。
- 一个是另一个范围内的**具体查询**：已有「门店排班管理」，新说法是「三号店下周三的排班表」——
  后者确实属于前者的领域，但它是一次具体查询，不是同一个长期关注点。这类要判为不同。
- 一个是另一个的下位概念：「数据库」和「PostgreSQL 连接池」。

拿不准就判不同。合并错了会把两件事的计数混在一起、事后完全看不出来；没合并只是暂时多一条。

只输出 JSON：
{"resolutions":[{"index":<新说法的序号>,"same_as":<已有主题的序号，没有则 null>,"label":<更好的名字，没有则 null>}]}`

var topicAdjudicationSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "resolutions": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "index": {"type": "integer"},
          "same_as": {"type": ["integer", "null"]},
          "label": {"type": ["string", "null"]}
        },
        "required": ["index", "same_as"]
      }
    }
  },
  "required": ["resolutions"]
}`)

// adjudicateTopics is tier 3: ask the model whether the labels nothing matched
// are really new subjects.
//
// It runs once per extraction run over every unresolved label at once, rather
// than once per label, because the cost that matters here is the round trip and
// the decision is the same shape for all of them.
func (s *Service) adjudicateTopics(
	ctx context.Context,
	modelID string,
	existing []*types.MemoryTopicStat,
	resolutions []topicResolution,
	unresolved []int,
) {
	if modelID == "" {
		// Nothing to fall back on. Every label here becomes its own subject,
		// which is the wrong answer but a visible one — as opposed to silently
		// skipping the tier, which is what happened while this checked the
		// configured extraction model directly: blank is the *default* and
		// means "use the conversation model", so on a default workspace the
		// model tier never ran and every rephrasing sat in its own row at one
		// hit, forever short of the promotion threshold.
		logger.Warnf(ctx, "memory: no model available to resolve %d new topics", len(unresolved))
		return
	}
	chatModel, err := s.modelService.GetChatModel(ctx, modelID)
	if err != nil || chatModel == nil {
		logger.Warnf(ctx, "memory: topic adjudication model unavailable: %v", err)
		return
	}

	var b strings.Builder
	b.WriteString("已有主题：\n")
	for i, stat := range existing {
		fmt.Fprintf(&b, "[%d] %s\n", i, stat.Topic)
	}
	b.WriteString("\n新出现的说法：\n")
	for _, idx := range unresolved {
		fmt.Fprintf(&b, "[%d] %s\n", idx, resolutions[idx].Surface)
	}

	// Thinking off, for the reason given on completeExtraction. Silently
	// getting nothing back here would send every rephrasing to its own row.
	thinking := false
	response, err := chatModel.Chat(ctx, []chat.Message{
		{Role: "system", Content: topicAdjudicationPrompt},
		{Role: "user", Content: b.String()},
	}, &chat.ChatOptions{
		Temperature:         0,
		MaxCompletionTokens: 800,
		Thinking:            &thinking,
		Format:              topicAdjudicationSchema,
	})
	if err != nil || response == nil {
		logger.Warnf(ctx, "memory: topic adjudication failed: %v", err)
		return
	}

	var parsed struct {
		Resolutions []struct {
			Index  int    `json:"index"`
			SameAs *int   `json:"same_as"`
			Label  string `json:"label"`
		} `json:"resolutions"`
	}
	content := strings.TrimSpace(response.Content)
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start < 0 || end <= start {
		return
	}
	if err := json.Unmarshal([]byte(content[start:end+1]), &parsed); err != nil {
		logger.Warnf(ctx, "memory: unparsable topic adjudication: %v", err)
		return
	}

	pending := make(map[int]struct{}, len(unresolved))
	for _, idx := range unresolved {
		pending[idx] = struct{}{}
	}
	for _, decision := range parsed.Resolutions {
		// Only labels this call was actually asked about may be reassigned. A
		// model that returns an index it was not given must not be able to
		// overwrite a match an earlier, more reliable tier already made.
		if _, ok := pending[decision.Index]; !ok {
			continue
		}
		if decision.SameAs == nil {
			continue
		}
		target := *decision.SameAs
		if target < 0 || target >= len(existing) {
			continue
		}
		resolutions[decision.Index].Canonical = existing[target]
		resolutions[decision.Index].Tier = "model"
		// A merge nothing lexical supported is the one most likely to be wrong,
		// so it is logged with both labels rather than only appearing as a
		// bumped counter on a subject the user never named.
		logger.Infof(ctx, "memory: model merged topic %q into %q",
			resolutions[decision.Index].Surface, existing[target].Topic)

		proposed := types.SanitizeMemoryTopic(decision.Label)
		if proposed == "" {
			continue
		}
		if !types.TopicLabelIsAnImprovement(
			existing[target].Topic, resolutions[decision.Index].Surface, proposed,
		) {
			logger.Infof(ctx, "memory: rejected proposed label %q for %q",
				proposed, existing[target].Topic)
			continue
		}
		resolutions[decision.Index].MergedLabel = proposed
	}
}

// collapseNewTopicsWithinRun points near-identical new labels from one run at
// the same surface form, so they become one row rather than two.
func collapseNewTopicsWithinRun(resolutions []topicResolution) {
	for i := range resolutions {
		if resolutions[i].Canonical != nil {
			continue
		}
		for j := 0; j < i; j++ {
			if resolutions[j].Canonical != nil {
				continue
			}
			if types.NormalizeTopicKey(resolutions[i].Surface) ==
				types.NormalizeTopicKey(resolutions[j].Surface) {
				resolutions[i].Surface = resolutions[j].Surface
				break
			}
		}
	}
}
