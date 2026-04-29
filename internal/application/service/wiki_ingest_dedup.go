package service

import (
	"sort"
	"strings"
	"unicode"

	"github.com/Tencent/WeKnora/internal/searchutil"
	"github.com/Tencent/WeKnora/internal/types"
)

// Pre-filtering candidate existing pages before the dedup LLM call.
//
// Without pre-filtering, the dedup prompt packs the entire entity+concept
// page corpus into <existing_pages>. On knowledge bases with 100+ pages
// this inflates input tokens and — more importantly — gives weaker LLMs
// enough rope to hallucinate merges between totally unrelated slugs just
// because the output looked plausible. Observed cases include
// "城镇登记失业人员" → "中华优秀传统文化" (zero shared characters).
//
// The filter below keeps only pages that share at least some cheap
// surface-level signal with one of the new items. Fast to compute, no
// external calls, and it only ever *removes* candidates from the prompt —
// the downstream validMerge check still guards the final write.
const (
	// dedupCandidateTopK is how many existing pages we keep per new item
	// even if none pass the score floor. Guarantees the LLM sees a small
	// ordered shortlist rather than an empty list when nothing matches
	// strongly.
	dedupCandidateTopK = 20

	// dedupCandidateScoreFloor is the Jaccard floor. Pairs at or above
	// this similarity are always included regardless of the top-K cap.
	// Tuned so that "城镇登记失业人员" vs "中华优秀传统文化" (Jaccard 0)
	// is excluded while "Acme Corp" vs "Acme Corporation" (Jaccard ≈ 0.5)
	// clearly passes.
	dedupCandidateScoreFloor = 0.08

	// dedupSmallCorpusBypass skips pre-filtering entirely when the
	// existing-page corpus is already small enough to fit in the prompt
	// without degrading the LLM. The filter only earns its keep on large
	// KBs; on small ones it risks cutting legitimate matches with no
	// real token savings.
	dedupSmallCorpusBypass = 25
)

// dedupSurface is the pre-computed similarity feature set for one side of
// a (new item, existing page) comparison.
type dedupSurface struct {
	// slugTokens are the kebab-case tokens from the slug base (after "/").
	// Slugs are an orthogonal signal to the surface names — Chinese pages
	// carry their pinyin here, which keeps the filter useful on purely
	// Latin-script new items too.
	slugTokens map[string]struct{}

	// nameGramSets holds one char-bigram set per surface form (name and
	// each alias). We keep them separate so the pair score is max-over-
	// surfaces — a rare alias match shouldn't be diluted by the primary
	// name disagreeing.
	nameGramSets []map[string]struct{}
}

// countEntityConceptPages returns how many of the given pages are
// entity- or concept-typed. Used only for logging the prefilter's
// reduction ratio.
func countEntityConceptPages(pages []*types.WikiPage) int {
	n := 0
	for _, p := range pages {
		if p == nil {
			continue
		}
		if p.PageType == types.WikiPageTypeEntity || p.PageType == types.WikiPageTypeConcept {
			n++
		}
	}
	return n
}

// selectDedupCandidatePages returns the subset of allPages plausibly
// related to at least one of newItems. Non-entity/concept pages are
// dropped unconditionally. The returned slice preserves the input order
// so the downstream prompt stays stable across runs.
//
// On small corpora (<= dedupSmallCorpusBypass entries) this is a no-op
// aside from the page-type filter.
func selectDedupCandidatePages(
	newItems []extractedItem,
	allPages []*types.WikiPage,
) []*types.WikiPage {
	pages := make([]*types.WikiPage, 0, len(allPages))
	for _, p := range allPages {
		if p == nil {
			continue
		}
		if p.PageType != types.WikiPageTypeEntity && p.PageType != types.WikiPageTypeConcept {
			continue
		}
		pages = append(pages, p)
	}
	if len(pages) == 0 {
		return pages
	}
	if len(newItems) == 0 || len(pages) <= dedupSmallCorpusBypass {
		return pages
	}

	pageFeats := make([]dedupSurface, len(pages))
	for i, p := range pages {
		surfaces := make([]string, 0, 1+len(p.Aliases))
		surfaces = append(surfaces, p.Title)
		surfaces = append(surfaces, []string(p.Aliases)...)
		pageFeats[i] = dedupSurface{
			slugTokens:   slugBaseTokens(p.Slug),
			nameGramSets: gramsPerSurface(surfaces),
		}
	}

	selected := make(map[int]bool, len(pages))
	for _, it := range newItems {
		surfaces := make([]string, 0, 1+len(it.Aliases))
		surfaces = append(surfaces, it.Name)
		surfaces = append(surfaces, it.Aliases...)
		itemFeat := dedupSurface{
			slugTokens:   slugBaseTokens(it.Slug),
			nameGramSets: gramsPerSurface(surfaces),
		}
		if len(itemFeat.slugTokens) == 0 && len(itemFeat.nameGramSets) == 0 {
			continue
		}

		scores := make([]struct {
			idx   int
			score float64
		}, len(pageFeats))
		for i := range pageFeats {
			scores[i].idx = i
			scores[i].score = dedupPairScore(itemFeat, pageFeats[i])
		}
		// Stable sort so ties break deterministically by original index.
		sort.SliceStable(scores, func(i, j int) bool {
			return scores[i].score > scores[j].score
		})

		topKRemaining := dedupCandidateTopK
		for _, s := range scores {
			if s.score >= dedupCandidateScoreFloor {
				selected[s.idx] = true
				continue
			}
			// Below the floor but we still owe the LLM some candidates so
			// it can decline cleanly — fill the top-K budget with the
			// highest-scoring remaining pages, as long as the score isn't
			// flatly zero (a zero score means we have nothing in common
			// with this page and including it just invites hallucination).
			if topKRemaining > 0 && s.score > 0 {
				selected[s.idx] = true
				topKRemaining--
				continue
			}
			break
		}
	}

	out := make([]*types.WikiPage, 0, len(selected))
	for i, p := range pages {
		if selected[i] {
			out = append(out, p)
		}
	}
	return out
}

// dedupPairScore is the max similarity between any surface form of a and
// b (plus the slug-token similarity). Slug and name signals live in
// different symbol spaces (ASCII pinyin vs raw surface form) so we take
// the max rather than e.g. their average.
func dedupPairScore(a, b dedupSurface) float64 {
	best := searchutil.Jaccard(a.slugTokens, b.slugTokens)
	for _, ag := range a.nameGramSets {
		for _, bg := range b.nameGramSets {
			if v := searchutil.Jaccard(ag, bg); v > best {
				best = v
			}
		}
	}
	return best
}

// slugBaseTokens returns the kebab-case tokens of a slug's base component.
// "entity/beijing-nongshang-yinxing" → {"beijing", "nongshang", "yinxing"}.
func slugBaseTokens(slug string) map[string]struct{} {
	if slug == "" {
		return nil
	}
	base := slug
	if i := strings.Index(slug, "/"); i >= 0 {
		base = slug[i+1:]
	}
	base = strings.ToLower(base)
	fields := strings.FieldsFunc(base, func(r rune) bool {
		return r == '-' || r == '_' || r == '.' || unicode.IsSpace(r)
	})
	if len(fields) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(fields))
	for _, tok := range fields {
		if tok == "" {
			continue
		}
		out[tok] = struct{}{}
	}
	return out
}

// gramsPerSurface computes a gram set per non-empty surface form.
func gramsPerSurface(surfaces []string) []map[string]struct{} {
	out := make([]map[string]struct{}, 0, len(surfaces))
	for _, s := range surfaces {
		g := surfaceGrams(s)
		if len(g) > 0 {
			out = append(out, g)
		}
	}
	return out
}

// surfaceGrams returns a character-bigram set for a surface form after
// lowercasing and stripping non-letter/digit runes. Bigrams work well
// across both CJK (where each bigram approximates a word) and Latin
// (where they catch stem overlap like "corporation" ↔ "corp"). Single-
// rune strings fall back to a 1-gram so they still contribute a signal.
func surfaceGrams(s string) map[string]struct{} {
	if s == "" {
		return nil
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	runes := []rune(b.String())
	if len(runes) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(runes))
	if len(runes) == 1 {
		out[string(runes)] = struct{}{}
		return out
	}
	for i := 0; i < len(runes)-1; i++ {
		out[string(runes[i:i+2])] = struct{}{}
	}
	return out
}
