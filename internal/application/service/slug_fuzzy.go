package service

import (
	"strings"

	"github.com/Tencent/WeKnora/internal/searchutil"
)

// Fuzzy slug resolution: rescue LLM-generated `[[slug|display]]` links
// where the slug is *almost* right but not exactly the one in the KB.
//
// Background: the wiki ingest LLM is given an `<available_wiki_pages>`
// block listing exact `[[slug]] = title` entries. Empirically — even
// when the prompt explicitly says "copy-paste the slug verbatim" —
// open-source models such as deepseek-v3 routinely munge slugs in
// predictable ways:
//
//   - inserting hyphens that happen to look like pinyin word breaks
//     ("shanghaitower" → "shang-hai-tower"),
//   - dropping or duplicating hyphens,
//   - normalizing the case of an ASCII slug,
//   - latinizing names that should have stayed CJK.
//
// The display text is almost always still correct (the model just
// copy-pastes the title), so we have two levers to find the *real*
// slug: a tolerant string compare on the slug itself, and a reverse
// lookup from display text. Combined, these recover the vast majority
// of broken links without any LLM cost — the alternative being
// stripDeadWikiLinks, which gives up and emits plain text.
//
// Cleanup callers (stripDeadWikiLinks, cleanDeadLinks) consult
// resolveDeadSlug FIRST and only fall back to "strip to plain text"
// when no live candidate is close enough to be safe.

// slugResolveBigramThreshold is the minimum char-bigram Jaccard
// similarity required for the bigram fallback to accept a candidate.
// 0.8 is intentionally conservative: at this level "shang-hai-tower"
// and "shanghai-tower" still match (most bigrams overlap), but
// "user-profile" and "user-permissions" do NOT (different stems even
// though the prefix matches). Anything weaker risks linking to the
// wrong page, which is worse than emitting plain text.
const slugResolveBigramThreshold = 0.8

// normalizeSlugForCompare collapses cosmetic slug variations so two
// slugs that differ only in hyphenation or case compare equal.
//
// Specifically:
//   - lowercases ASCII letters (slugify already does this, so most
//     real slugs are already lowercase, but the LLM occasionally
//     produces capitalized forms),
//   - removes every hyphen and underscore.
//
// Hyphens in wiki slugs are purely visual separators — slugify uses
// them between word fragments. Removing them lets us treat
// "shang-hai-tower" and "shanghai-tower" as the same logical token bag.
//
// CJK runes are preserved verbatim: they ARE the meaningful identity
// of CJK slugs, so collapsing them would over-merge.
func normalizeSlugForCompare(slug string) string {
	slug = strings.ToLower(slug)
	slug = strings.ReplaceAll(slug, "-", "")
	slug = strings.ReplaceAll(slug, "_", "")
	return slug
}

// resolveDeadSlug attempts to map a dead `[[slug]]` reference back to
// a live KB slug, using progressively more permissive heuristics:
//
//  1. Display-text reverse lookup. If the LLM emitted "[[bad-slug|上海
//     中心大厦]]" and there's a live page whose Title or alias is
//     "上海中心大厦", return its slug. This is the most common case
//     by far (the model copies the title correctly even when it
//     mangles the slug).
//
//  2. Hyphen/case-normalized slug equality. Two slugs that compare
//     equal under normalizeSlugForCompare are treated as the same
//     logical page. Catches the LLM's "insert pinyin breaks" pattern.
//
//  3. Bigram Jaccard similarity ≥ slugResolveBigramThreshold (0.8).
//     Catches typos and minor character substitutions that the
//     normalize step doesn't fix. Computed via searchutil.Jaccard
//     against character bigrams of each candidate slug.
//
// Returns the resolved live slug and true on success; "" and false
// when no candidate is close enough to be safe.
//
// The candidate pool is supplied by the caller as `liveSlugs`. The
// caller is also responsible for passing a `titleToSlug` map keyed
// by exact (case-sensitive) page title and alias surface forms. Both
// maps are consulted only — never mutated.
func resolveDeadSlug(
	deadSlug string,
	displayText string,
	liveSlugs map[string]struct{},
	titleToSlug map[string]string,
) (string, bool) {
	if deadSlug == "" {
		return "", false
	}
	// Already live? No-op success.
	if _, ok := liveSlugs[deadSlug]; ok {
		return deadSlug, true
	}

	// (1) Display-text reverse lookup. Trim whitespace because LLM
	// occasionally emits `[[slug| display ]]` with stray spaces, and
	// the user-side title would have been written without them.
	if dt := strings.TrimSpace(displayText); dt != "" {
		if slug, ok := titleToSlug[dt]; ok && slug != "" {
			if _, live := liveSlugs[slug]; live {
				return slug, true
			}
		}
	}

	// (2) Normalized-equality check. Compute the dead slug's normal
	// form once and compare against every live candidate's normal
	// form. O(N) but N is bounded — this only fires on the dead-link
	// path, and `liveSlugs` is the per-batch affected set, not the
	// full KB.
	deadNorm := normalizeSlugForCompare(deadSlug)
	if deadNorm == "" {
		// Slug was nothing but hyphens / underscores after normalize
		// — nothing meaningful to compare against.
		return "", false
	}
	for cand := range liveSlugs {
		if normalizeSlugForCompare(cand) == deadNorm {
			return cand, true
		}
	}

	// (3) Bigram Jaccard fallback. Use searchutil.Jaccard against
	// the character bigrams of each slug. We restrict the bigram
	// alphabet to the slug's normalized form (lowercase, no hyphens)
	// so a hyphenation difference doesn't dominate the bigram set
	// the way it would for raw slug strings.
	deadGrams := slugCharBigrams(deadNorm)
	if len(deadGrams) == 0 {
		return "", false
	}
	var (
		bestSlug  string
		bestScore float64
	)
	for cand := range liveSlugs {
		candNorm := normalizeSlugForCompare(cand)
		if candNorm == "" {
			continue
		}
		candGrams := slugCharBigrams(candNorm)
		if len(candGrams) == 0 {
			continue
		}
		score := searchutil.Jaccard(deadGrams, candGrams)
		if score > bestScore {
			bestScore = score
			bestSlug = cand
		}
	}
	if bestScore >= slugResolveBigramThreshold {
		return bestSlug, true
	}
	return "", false
}

// slugCharBigrams returns a character-bigram set for the given (already
// normalized) slug. Single-rune slugs degrade to a 1-gram so they still
// contribute a comparable signal.
//
// Mirrors surfaceGrams in wiki_ingest_dedup.go but operates on a slug
// (which is already lowercased / hyphen-stripped) rather than a raw
// surface form, so we don't redo the lowercasing or punctuation
// stripping work.
func slugCharBigrams(s string) map[string]struct{} {
	if s == "" {
		return nil
	}
	runes := []rune(s)
	if len(runes) == 1 {
		return map[string]struct{}{string(runes): {}}
	}
	out := make(map[string]struct{}, len(runes))
	for i := 0; i < len(runes)-1; i++ {
		out[string(runes[i:i+2])] = struct{}{}
	}
	return out
}
