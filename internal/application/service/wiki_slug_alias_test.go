package service

import (
	"strings"
	"testing"
)

func TestSlugAliaser_RoundTripEscapesUUIDSlugs(t *testing.T) {
	summarySlug := "summary/07a20bb1-a662-47cf-9929-06fb5d5b5b5e"
	entitySlug := "entity/mongodb"
	known := map[string]struct{}{summarySlug: {}, entitySlug: {}}

	a := newSlugAliaser()
	// Pre-assign tokens the way the ingest listing loop does.
	tSummary := a.token(summarySlug)
	tEntity := a.token(entitySlug)
	if tSummary == summarySlug || tEntity == entitySlug {
		t.Fatalf("tokens must differ from real slugs: %q %q", tSummary, tEntity)
	}
	if strings.Contains(tSummary, "/") {
		t.Fatalf("token %q must not contain '/'", tSummary)
	}

	body := "See [[" + summarySlug + "|Weknora 试错记录.md - Summary]] and [[" + entitySlug + "]]."
	aliased := a.aliasContent(body, known)

	// The high-entropy UUID must no longer be present in what the model sees.
	if strings.Contains(aliased, "06fb5d5b5b5e") {
		t.Fatalf("aliased content still exposes the UUID: %q", aliased)
	}
	if !strings.Contains(aliased, "[["+tSummary+"|Weknora 试错记录.md - Summary]]") {
		t.Fatalf("summary link not aliased: %q", aliased)
	}
	if !strings.Contains(aliased, "[["+tEntity+"]]") {
		t.Fatalf("entity link not aliased: %q", aliased)
	}

	// Round-trip must restore the original body exactly.
	if got := a.dealiasContent(aliased); got != body {
		t.Fatalf("dealias round-trip mismatch:\n got  %q\n want %q", got, body)
	}
}

func TestSlugAliaser_UnknownTokensAndSlugsUntouched(t *testing.T) {
	a := newSlugAliaser()
	a.token("summary/real-uuid")

	// A slug not in `known` must not be aliased.
	known := map[string]struct{}{"summary/real-uuid": {}}
	body := "keep [[entity/invented-by-model]] as-is"
	if got := a.aliasContent(body, known); got != body {
		t.Fatalf("unknown slug should be left untouched, got %q", got)
	}

	// An unmapped token in output must be left untouched (falls through to the
	// normal dead-link cleanup path).
	out := "stray [[ref-999|X]] token"
	if got := a.dealiasContent(out); got != out {
		t.Fatalf("unmapped token should be left untouched, got %q", got)
	}
}

func TestSlugAliaser_EmptyIsNoop(t *testing.T) {
	a := newSlugAliaser()
	if !a.empty() {
		t.Fatal("fresh aliaser must be empty")
	}
	body := "[[summary/x]] unchanged"
	if got := a.dealiasContent(body); got != body {
		t.Fatalf("empty aliaser dealias must be a no-op, got %q", got)
	}
}
