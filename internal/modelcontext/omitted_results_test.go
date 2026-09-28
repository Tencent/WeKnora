package modelcontext

import (
	"strings"
	"testing"
)

func TestAnnotateSearchNotes(t *testing.T) {
	t.Parallel()
	base := "<retrieval mode=\"hybrid\">\n</retrieval>"
	got := annotateSearchNotes(base, map[string]interface{}{
		"omitted_for_budget": 4,
		"partial_failures":   []string{"[kb-2]: vector store unavailable"},
	})
	if !strings.Contains(got, `<omitted count="4" reason="output_budget">`) ||
		!strings.Contains(got, "<partial_failure>[kb-2]: vector store unavailable") ||
		!strings.HasSuffix(got, "</retrieval>") {
		t.Fatalf("annotated = %q", got)
	}
	if annotateSearchNotes(base, map[string]interface{}{}) != base {
		t.Fatal("nothing to add must leave the output unchanged")
	}
}

func TestAnnotateSearchNotesReportsTruncatedRetrieval(t *testing.T) {
	t.Parallel()
	base := "<retrieval mode=\"hybrid\">\n</retrieval>"
	got := annotateSearchNotes(base, map[string]interface{}{
		"retrieval_shown":      5,
		"retrieval_candidates": 146,
	})
	if !strings.Contains(got, `<subset shown="5" candidates="146">`) ||
		!strings.Contains(got, "a complete list cannot be derived from this view") ||
		!strings.HasSuffix(got, "</retrieval>") {
		t.Fatalf("annotated = %q", got)
	}
	for name, data := range map[string]map[string]interface{}{
		"complete":        {"retrieval_shown": 5, "retrieval_candidates": 5},
		"no counts":       {},
		"shown only":      {"retrieval_shown": 5},
		"candidates only": {"retrieval_candidates": 146},
	} {
		if annotateSearchNotes(base, data) != base {
			t.Fatalf("%s: a non-truncated retrieval must leave the output unchanged", name)
		}
	}
}

func TestAnnotateGraphResult(t *testing.T) {
	t.Parallel()
	base := "<retrieval mode=\"graph\">\n</retrieval>"
	got := annotateGraphResult(base, map[string]interface{}{
		"relations": []map[string]interface{}{{"source": "Kubernetes", "type": "orchestrates", "target": "Docker"}},
		"errors":    []string{"KB b2: graph extraction not configured"},
	})
	for _, want := range []string{
		`<relation source="Kubernetes" type="orchestrates" target="Docker" />`,
		"<error>KB b2: graph extraction not configured</error>",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
	if annotateGraphResult(base, map[string]interface{}{}) != base {
		t.Fatal("nothing to add must leave the output unchanged")
	}
}

func TestMatchAddsToContent(t *testing.T) {
	t.Parallel()
	content := "Install the engine, then configure psionic drive parameters."
	if matchAddsToContent("... configure psionic drive ...", content) {
		t.Fatal("an excerpt of the content adds nothing")
	}
	if !matchAddsToContent("How do I tune the drive?", content) {
		t.Fatal("a matched question absent from the content adds information")
	}
	if !matchAddsToContent("anything", "") {
		t.Fatal("a snippet-only row keeps its snippet")
	}
}
