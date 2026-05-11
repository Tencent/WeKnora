package service

import "testing"

func TestNormalizeSlugForCompare(t *testing.T) {
	cases := map[string]string{
		"shang-hai-tower":     "shanghaitower",
		"shanghai-tower":      "shanghaitower",
		"SHANG-HAI-TOWER":     "shanghaitower",
		"under_score_slug":    "underscoreslug",
		"entity/shanghai-tower": "entity/shanghaitower",
		"":                    "",
		"---":                 "",
		"中文-slug":             "中文slug",
	}
	for input, want := range cases {
		t.Run(input, func(t *testing.T) {
			if got := normalizeSlugForCompare(input); got != want {
				t.Errorf("normalizeSlugForCompare(%q) = %q, want %q", input, got, want)
			}
		})
	}
}

func TestResolveDeadSlug_HyphenVariation(t *testing.T) {
	// The exact bug pattern reported in production: the LLM inserted a
	// pinyin word break in a CJK-derived slug ("shang-hai" instead of
	// "shanghai") even though the display text was copied verbatim from
	// the canonical title. Resolver should heal the hyphenation drift
	// using the display-text reverse lookup.
	live := map[string]struct{}{
		"entity/shanghai-tower": {},
	}
	titleToSlug := map[string]string{
		"上海中心大厦": "entity/shanghai-tower",
	}
	got, ok := resolveDeadSlug(
		"entity/shang-hai-tower",
		"上海中心大厦",
		live, titleToSlug,
	)
	if !ok {
		t.Fatal("expected resolution success on pinyin-break drift case")
	}
	if got != "entity/shanghai-tower" {
		t.Errorf("expected canonical slug, got %q", got)
	}
}

func TestResolveDeadSlug_ExactMatchPasses(t *testing.T) {
	live := map[string]struct{}{
		"entity/foo": {},
	}
	got, ok := resolveDeadSlug("entity/foo", "", live, nil)
	if !ok || got != "entity/foo" {
		t.Errorf("expected pass-through for live slug, got (%q, %v)", got, ok)
	}
}

func TestResolveDeadSlug_DisplayTextLookupPriority(t *testing.T) {
	// Even when normalized-equality would also find a candidate,
	// display-text lookup should win because it's the highest-
	// confidence signal.
	live := map[string]struct{}{
		"entity/exact-match": {},
		"entity/exactmatch":  {},
	}
	titleToSlug := map[string]string{
		"Exact Match": "entity/exactmatch",
	}
	got, ok := resolveDeadSlug(
		"entity/exact-match-typo",
		"Exact Match",
		live, titleToSlug,
	)
	if !ok {
		t.Fatal("expected resolution")
	}
	if got != "entity/exactmatch" {
		t.Errorf("display-text lookup should have won; got %q", got)
	}
}

func TestResolveDeadSlug_DisplayTextStillRequiresLive(t *testing.T) {
	// titleToSlug points at a slug that's NOT in liveSlugs; we
	// shouldn't return it.
	live := map[string]struct{}{
		"entity/something-else": {},
	}
	titleToSlug := map[string]string{
		"Title": "entity/dead-target",
	}
	if _, ok := resolveDeadSlug("entity/x", "Title", live, titleToSlug); ok {
		t.Fatal("must not return a slug that isn't in liveSlugs")
	}
}

func TestResolveDeadSlug_BigramFallback(t *testing.T) {
	// Slug differs from canonical by a single character (typo).
	// Normalized-equality won't catch this; bigram Jaccard should.
	live := map[string]struct{}{
		"entity/zhongguo-yinhang": {},
	}
	got, ok := resolveDeadSlug(
		"entity/zhongguo-yinghang", // h↔gh transposition typo
		"",
		live, nil,
	)
	if !ok {
		t.Fatal("expected bigram fallback to recover the typo")
	}
	if got != "entity/zhongguo-yinhang" {
		t.Errorf("got %q", got)
	}
}

func TestResolveDeadSlug_RejectsUnrelated(t *testing.T) {
	// Completely unrelated candidates must not match — better to
	// strip the link than to silently link to the wrong page.
	live := map[string]struct{}{
		"entity/foo": {},
		"entity/bar": {},
		"entity/baz": {},
	}
	if got, ok := resolveDeadSlug("entity/quuxasdf", "", live, nil); ok {
		t.Errorf("expected no match for unrelated slug; got %q", got)
	}
}

func TestResolveDeadSlug_EmptyInputs(t *testing.T) {
	if _, ok := resolveDeadSlug("", "any", nil, nil); ok {
		t.Error("empty deadSlug must return false")
	}
	if _, ok := resolveDeadSlug("entity/foo", "", nil, nil); ok {
		t.Error("empty liveSlugs / titleToSlug must return false")
	}
}
