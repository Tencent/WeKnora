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

// The subset note is conditional: only a counting or exhaustive-list question
// has to report the scope of the view. It must also carry the counts it was
// given, so the model reads the real shown / candidate numbers rather than a
// placeholder or a knowledge-base total.
func TestAnnotateSearchNotesReportsTruncatedRetrieval(t *testing.T) {
	t.Parallel()
	base := "<retrieval mode=\"hybrid\">\n</retrieval>"
	got := annotateSearchNotes(base, map[string]interface{}{
		"retrieval_shown":      5,
		"retrieval_candidates": 146,
	})
	want := `  <subset shown="5" candidates="146">If the question asks for a count or an exhaustive list, ` +
		`state that the provided context contains only the top 5 of 146 candidate passages at this ` +
		`filtering stage, and do not treat these passage counts as the requested total; otherwise ` +
		`answer normally without adding a disclaimer solely because of this truncation.</subset>`
	if !strings.Contains(got, want) {
		t.Fatalf("annotated = %q\nwant subset %q", got, want)
	}
	if strings.Contains(got, "say the answer may be incomplete") {
		t.Fatalf("the unconditional disclaimer must be gone: %q", got)
	}
	if !strings.HasSuffix(got, "</retrieval>") {
		t.Fatalf("subset note must stay inside the retrieval block: %q", got)
	}
	// A prompt that saw every candidate, or was given no usable counts,
	// carries no caveat at all.
	for name, data := range map[string]map[string]interface{}{
		"complete":         {"retrieval_shown": 5, "retrieval_candidates": 5},
		"no counts":        {},
		"shown only":       {"retrieval_shown": 5},
		"candidates only":  {"retrieval_candidates": 146},
		"more than pool":   {"retrieval_shown": 7, "retrieval_candidates": 5},
		"no shown passage": {"retrieval_shown": 0, "retrieval_candidates": 146},
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
