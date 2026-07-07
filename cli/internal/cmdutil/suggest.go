package cmdutil

import "sort"

// levenshtein returns the rune-aware edit distance between a and b. Operates
// on runes (not bytes) so multi-byte names compare correctly.
func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	la, lb := len(ra), len(rb)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			del := prev[j] + 1
			ins := curr[j-1] + 1
			sub := prev[j-1] + cost
			m := del
			if ins < m {
				m = ins
			}
			if sub < m {
				m = sub
			}
			curr[j] = m
		}
		prev, curr = curr, prev
	}
	return prev[lb]
}

// SuggestOne returns the single candidate closest to target by edit distance
// when it is a plausible typo (distance ≤ 2), or "" when none qualifies. Ties
// are broken lexicographically so the result is deterministic regardless of
// candidate ordering (Go randomizes map-range, so callers passing map keys
// would otherwise get flaky "did you mean" output). Use when a single
// "did you mean: X?" is wanted (e.g. profile lookup); SuggestClosest returns
// the ranked list instead.
func SuggestOne(target string, candidates []string) string {
	if target == "" {
		return ""
	}
	sorted := append([]string(nil), candidates...)
	sort.Strings(sorted)
	best := ""
	bestD := 3
	for _, c := range sorted {
		if d := levenshtein(target, c); d < bestD {
			bestD = d
			best = c
		}
	}
	if bestD > 2 {
		return ""
	}
	return best
}

// SuggestClosest returns the candidates nearest to target by edit distance,
// closest first, limited to genuinely-plausible typos. Used to turn an
// "unknown subcommand"/"unknown name" error into an actionable "did you
// mean: X?" instead of dumping the whole list. Returns nil when nothing is
// close enough.
//
// Threshold scales with the target length (max(2, len/3)) so short names
// need an exact-ish match while longer names tolerate more slips. At most 3
// suggestions are returned.
func SuggestClosest(target string, candidates []string) []string {
	if target == "" {
		return nil
	}
	threshold := len([]rune(target)) / 3
	if threshold < 2 {
		threshold = 2
	}
	type scored struct {
		name string
		dist int
	}
	var matches []scored
	for _, c := range candidates {
		d := levenshtein(target, c)
		if d <= threshold {
			matches = append(matches, scored{c, d})
		}
	}
	if len(matches) == 0 {
		return nil
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].dist != matches[j].dist {
			return matches[i].dist < matches[j].dist
		}
		return matches[i].name < matches[j].name
	})
	var out []string
	for i, m := range matches {
		if i >= 3 {
			break
		}
		out = append(out, m.name)
	}
	return out
}
