package tools

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// TestRenderIndexOverviewForAgent_IncludesIntroAndTruncationHint verifies
// that when a group's total exceeds its item count, the synthesized
// markdown surfaces both numbers and the "showing top N" banner so the
// model knows further exploration requires a separate tool call.
func TestRenderIndexOverviewForAgent_IncludesIntroAndTruncationHint(t *testing.T) {
	resp := &types.WikiIndexResponse{
		Intro: "# Wiki Index\n\nWelcome to the wiki.",
		Groups: []types.WikiIndexGroup{
			{
				Type:  types.WikiPageTypeEntity,
				Total: 4500,
				Items: []types.WikiIndexEntry{
					{Slug: "entity/acme", Title: "Acme", Summary: "A company"},
					{Slug: "entity/beta", Title: "Beta", Summary: "Another company"},
				},
			},
			{
				Type:  types.WikiPageTypeConcept,
				Total: 2,
				Items: []types.WikiIndexEntry{
					{Slug: "concept/rag", Title: "RAG", Summary: "Retrieval-augmented generation"},
					{Slug: "concept/emb", Title: "Embedding", Summary: ""},
				},
			},
		},
	}

	got := renderIndexOverviewForAgent(resp)

	assertContains(t, got, "# Wiki Index")
	// Truncation banner for the big group: Total != len(Items).
	assertContains(t, got, "4500 total, showing top 2")
	// Non-truncated group uses the simple "(N)" form.
	assertContains(t, got, "## Concept (2)")
	// Entry formatting — with and without summary. Entries emit the
	// [[slug|title]] form so the LLM sees the human-readable name.
	assertContains(t, got, "[[entity/acme|Acme]] — A company")
	// When the entry has a title but no summary, slug|title still
	// renders; the dash + summary portion is elided.
	assertContains(t, got, "[[concept/emb|Embedding]]")
	// Exploration hint.
	assertContains(t, got, "wiki_search")
}

// TestRenderIndexOverviewForAgent_EmptyKB explains to the model that the
// wiki has no pages yet — otherwise it might hallucinate slugs to read.
func TestRenderIndexOverviewForAgent_EmptyKB(t *testing.T) {
	resp := &types.WikiIndexResponse{
		Intro: "# Empty\n",
		Groups: []types.WikiIndexGroup{
			{Type: types.WikiPageTypeEntity, Total: 0, Items: nil},
		},
	}

	got := renderIndexOverviewForAgent(resp)
	assertContains(t, got, "No wiki pages yet")
	if strings.Contains(got, "## Entity") {
		t.Fatalf("empty groups should not surface a heading: %q", got)
	}
}

// TestRenderIndexOverviewForAgent_UnknownType falls through to the raw
// type string — covers forward compatibility for LLM-created page types
// the frontend label map does not yet know about.
func TestRenderIndexOverviewForAgent_UnknownType(t *testing.T) {
	resp := &types.WikiIndexResponse{
		Intro: "# Wiki\n",
		Groups: []types.WikiIndexGroup{
			{
				Type:  "custom-type",
				Total: 1,
				Items: []types.WikiIndexEntry{{Slug: "custom-type/x", Title: "X"}},
			},
		},
	}

	got := renderIndexOverviewForAgent(resp)
	assertContains(t, got, "## custom-type (1)")
}

// TestRenderIndexOverviewForAgent_StripsLegacyInlineDirectory covers
// the forward-compat path: KBs that haven't been re-ingested since the
// index refactor still carry an "intro + ## Summary..." payload on the
// index row. The agent synthesizer must clip the legacy directory so
// the model doesn't see it next to the live top-K block the group
// loop emits below.
func TestRenderIndexOverviewForAgent_StripsLegacyInlineDirectory(t *testing.T) {
	resp := &types.WikiIndexResponse{
		Intro: "# Wiki Index\n\nWelcome.\n\n## Summary (3)\n\n[[summary/legacy]] — old",
		Groups: []types.WikiIndexGroup{
			{
				Type:  types.WikiPageTypeEntity,
				Total: 1,
				Items: []types.WikiIndexEntry{{Slug: "entity/live", Title: "Live", Summary: "fresh"}},
			},
		},
	}

	got := renderIndexOverviewForAgent(resp)
	if strings.Contains(got, "summary/legacy") {
		t.Fatalf("legacy inline directory should have been stripped, got:\n%s", got)
	}
	assertContains(t, got, "Welcome.")
	assertContains(t, got, "[[entity/live|Live]] — fresh")
}

func assertContains(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Fatalf("expected output to contain %q, got:\n%s", needle, haystack)
	}
}
