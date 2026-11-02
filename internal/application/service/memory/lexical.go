package memory

import (
	"sort"
	"strings"
	"unicode"

	"github.com/Tencent/WeKnora/internal/types"
)

// Situational recall is lexical rather than vector-based. One subject holds a
// few hundred one-line items, so scanning them costs less than an embedding
// round trip would, and it keeps the read path free of both a model call and a
// vector store dependency. If real usage shows lexical matching missing
// paraphrases, adding a vector index here is an isolated change: the ranking
// function is the only thing that would move.

// tokenize splits text the same way NormalizeMemoryKey does, so a query and a
// stored item are compared on the same alphabet. CJK is split per ideograph
// because it has no word separators; everything else splits on non-alphanumeric.
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

// hanSegments splits each Han run of the text at jieba word boundaries and
// keeps the multi-character words, so 「连接池」 reaches the scorer as one
// term instead of a pile of characters. A run that yields no multi-character
// word stays whole — novel names jieba cannot parse keep the in-word pair
// fallback instead of losing it. Single-character words (的了是…) are dropped:
// as features they are stopwords, and the per-character unigrams already
// carry them.
func hanSegments(text string) []string {
	var segments []string
	var run strings.Builder
	flush := func() {
		defer run.Reset()
		if run.Len() == 0 {
			return
		}
		text := run.String()
		found := len(segments)
		for _, word := range types.Jieba.Cut(text, true) {
			if len([]rune(word)) >= 2 {
				segments = append(segments, word)
			}
		}
		if len(segments) == found {
			if runes := []rune(text); len(runes) >= 2 {
				segments = append(segments, text)
			}
		}
	}
	for _, r := range text {
		if unicode.Is(unicode.Han, r) {
			run.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return segments
}

// wordPairs returns the character pairs inside one segment. Pairs never cross
// a segment boundary: 「连接池配置」 used to hand the scorer fragments like
// 池配, which match unrelated text sharing characters across different word
// boundaries.
func wordPairs(segment string) []string {
	runes := []rune(segment)
	pairs := make([]string, 0, len(runes)-1)
	for i := 0; i+1 < len(runes); i++ {
		pairs = append(pairs, string(runes[i:i+2]))
	}
	return pairs
}

func isSingleHan(token string) bool {
	runes := []rune(token)
	return len(runes) == 1 && unicode.Is(unicode.Han, runes[0])
}

// Match tiers, strongest first. A whole-word hit is the best evidence two
// texts share a term, an in-word character pair still says they spell
// something the same way, and a lone shared ideograph says very little —
// 「数」 sits in 数据, 参数 and 数量 alike — so it counts at half.
const (
	wordHitWeight   = 4.0
	bigramHitWeight = 2.0
)

func unigramWeight(token string) float64 {
	if isSingleHan(token) {
		return 0.5
	}
	return 1.0
}

type scoredItem struct {
	item  *types.MemoryItem
	score float64
}

// scoreItems ranks situational items against the current query. Scoring is
// deliberately simple: overlap of query features with the item across the
// three tiers above — whole words, in-word character pairs, lone characters —
// with importance used only to break ties.
func scoreItems(query string, items []*types.MemoryItem) []scoredItem {
	queryTokens := tokenize(query)
	if len(queryTokens) == 0 || len(items) == 0 {
		return nil
	}

	querySegments := hanSegments(query)
	queryWords := make(map[string]struct{}, len(querySegments))
	queryBigrams := make(map[string]struct{})
	for _, segment := range querySegments {
		queryWords[segment] = struct{}{}
		for _, pair := range wordPairs(segment) {
			queryBigrams[pair] = struct{}{}
		}
	}
	queryUnigrams := make(map[string]struct{}, len(queryTokens))
	for _, token := range queryTokens {
		// One-character latin tokens carry no signal and match everywhere.
		if len([]rune(token)) < 2 && !isSingleHan(token) {
			continue
		}
		queryUnigrams[token] = struct{}{}
	}
	if len(queryUnigrams) == 0 && len(queryBigrams) == 0 {
		return nil
	}

	scored := make([]scoredItem, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		// Score against the topic as well as the statement, indexed separately
		// so no word or pair spans the boundary between them. The normalized
		// key is deliberately not used: it is a sorted character soup built for
		// collision detection, so its adjacency carries no meaning.
		itemWords := make(map[string]struct{})
		itemUnigrams := make(map[string]struct{})
		itemBigrams := make(map[string]struct{})
		for _, text := range []string{item.Content, item.Topic} {
			for _, segment := range hanSegments(text) {
				itemWords[segment] = struct{}{}
				for _, pair := range wordPairs(segment) {
					itemBigrams[pair] = struct{}{}
				}
			}
			for _, token := range tokenize(text) {
				itemUnigrams[token] = struct{}{}
			}
		}
		if len(itemUnigrams) == 0 {
			continue
		}

		var hits float64
		for word := range queryWords {
			if _, ok := itemWords[word]; ok {
				hits += wordHitWeight
			}
		}
		for token := range queryUnigrams {
			if _, ok := itemUnigrams[token]; ok {
				hits += unigramWeight(token)
			}
		}
		for pair := range queryBigrams {
			if _, ok := itemBigrams[pair]; ok {
				hits += bigramHitWeight
			}
		}
		if hits == 0 {
			continue
		}
		// Normalizing by the query's total feature weight keeps a long query
		// from favouring long items purely because there was more to match
		// against.
		denominator := float64(len(queryWords))*wordHitWeight +
			float64(len(queryBigrams))*bigramHitWeight
		for token := range queryUnigrams {
			denominator += unigramWeight(token)
		}
		if denominator == 0 {
			continue
		}
		scored = append(scored, scoredItem{
			item:  item,
			score: hits/denominator + 0.01*float64(item.Importance),
		})
	}

	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		return scored[i].item.ValidFrom.After(scored[j].item.ValidFrom)
	})

	return scored
}

// minRecallScore is the relevance floor. Injecting a weakly related memory
// costs context and, worse, invites the model to use it: a stale note that
// merely shares a word with the question is more harmful than no note at all.
const minRecallScore = 0.15

// selectRecallItems takes the best matches within both the count and rune
// budgets.
func selectRecallItems(query string, items []*types.MemoryItem, maxItems, runeBudget int) []*types.MemoryItem {
	return takeWithinBudget(lexicalRanking(query, items), items, maxItems, runeBudget)
}

// lexicalRanking returns the indexes of the items that clear the lexical bar,
// best first.
//
// Indexes rather than ids because a ranking is only ever meaningful against the
// candidate slice it was produced from, and because an item is not guaranteed
// to carry an id — keying on one silently collapses every id-less item onto the
// same entry.
func lexicalRanking(query string, items []*types.MemoryItem) []int {
	index := make(map[*types.MemoryItem]int, len(items))
	for i, item := range items {
		if item != nil {
			index[item] = i
		}
	}
	scored := scoreItems(query, items)
	ranked := make([]int, 0, len(scored))
	for _, entry := range scored {
		if entry.score < minRecallScore {
			break
		}
		if i, ok := index[entry.item]; ok {
			ranked = append(ranked, i)
		}
	}
	return ranked
}

// takeWithinBudget materialises a ranking into items, stopping at the item cap
// and skipping anything that no longer fits the rune budget.
//
// Skipping rather than stopping on an over-budget item is deliberate: one long
// memory should not shut out the several short ones behind it.
func takeWithinBudget(
	ranking []int, items []*types.MemoryItem, maxItems, runeBudget int,
) []*types.MemoryItem {
	selected := make([]*types.MemoryItem, 0, maxItems)
	used := 0
	for _, index := range ranking {
		if len(selected) >= maxItems {
			break
		}
		if index < 0 || index >= len(items) {
			continue
		}
		item := items[index]
		if item == nil {
			continue
		}
		cost := len([]rune(item.Content)) + 3
		if used+cost > runeBudget {
			continue
		}
		selected = append(selected, item)
		used += cost
	}
	return selected
}
