package agent

import (
	"strings"
	"testing"
	"text/template"
)

func TestWikiGranularityGuidance_RoutesByKey(t *testing.T) {
	cases := map[string]string{
		"focused":    WikiGranularityGuidanceFocused,
		"standard":   WikiGranularityGuidanceStandard,
		"exhaustive": WikiGranularityGuidanceExhaustive,
	}
	for key, want := range cases {
		if got := WikiGranularityGuidance(key); got != want {
			t.Errorf("WikiGranularityGuidance(%q) returned unexpected block", key)
			_ = got
		}
	}
}

func TestWikiGranularityGuidance_UnknownDefaultsToStandard(t *testing.T) {
	unknowns := []string{"", "FOCUSED", "detailed", "minimal", "full", "unknown"}
	for _, k := range unknowns {
		if WikiGranularityGuidance(k) != WikiGranularityGuidanceStandard {
			t.Errorf("WikiGranularityGuidance(%q) should fall back to STANDARD block", k)
		}
	}
}

// Sanity check that the three guidance blocks are meaningfully different.
// A regression (e.g. two constants accidentally pointing at the same string)
// would silently disable the user-facing level control.
func TestWikiGranularityGuidance_BlocksAreDistinct(t *testing.T) {
	blocks := []string{
		WikiGranularityGuidanceFocused,
		WikiGranularityGuidanceStandard,
		WikiGranularityGuidanceExhaustive,
	}
	seen := make(map[string]bool, len(blocks))
	for _, b := range blocks {
		if b == "" {
			t.Error("granularity guidance block must not be empty")
			continue
		}
		if seen[b] {
			t.Error("granularity guidance blocks must be distinct")
		}
		seen[b] = true
	}

	// Each block should name its mode, so the LLM can't silently get the
	// wrong guidance without us noticing in review.
	if !strings.Contains(WikiGranularityGuidanceFocused, "FOCUSED") {
		t.Error("focused block should self-identify")
	}
	if !strings.Contains(WikiGranularityGuidanceStandard, "STANDARD") {
		t.Error("standard block should self-identify")
	}
	if !strings.Contains(WikiGranularityGuidanceExhaustive, "EXHAUSTIVE") {
		t.Error("exhaustive block should self-identify")
	}
}

func renderWikiChunkCitation(t *testing.T, candidateSlugs, chunksXML, lang string) string {
	t.Helper()
	tmpl, err := template.New("cite").Parse(WikiChunkCitationPrompt)
	if err != nil {
		t.Fatalf("parse template: %v", err)
	}
	var b strings.Builder
	if err := tmpl.Execute(&b, map[string]string{
		"CandidateSlugs": candidateSlugs,
		"ChunksXML":      chunksXML,
		"Language":       lang,
	}); err != nil {
		t.Fatalf("execute template: %v", err)
	}
	return b.String()
}

// TestWikiChunkCitationPrompt_StablePrefixAcrossBatches verifies the property
// that enables provider prefix caching (issue #1687): within one document the
// candidate slugs and the static rules are constant and only the per-batch
// <chunks> block changes, so everything up to <chunks> must be byte-identical
// across batches. If the static rules trailed <chunks> they would be re-billed
// on every batch and this prefix would diverge.
func TestWikiChunkCitationPrompt_StablePrefixAcrossBatches(t *testing.T) {
	const slugs = "entity/acme = Acme Corp\nconcept/rag = Retrieval-Augmented Generation"

	a := renderWikiChunkCitation(t, slugs, `<c id="c001">first batch text</c>`, "English")
	b := renderWikiChunkCitation(t, slugs, `<c id="c099">a completely different second batch</c>`, "English")

	// Match the standalone <chunks> tag line (the per-batch data block), not the
	// inline "<chunks> block" references inside the instructions prose.
	const marker = "\n<chunks>\n"
	ia := strings.Index(a, marker)
	ib := strings.Index(b, marker)
	if ia < 0 || ib < 0 {
		t.Fatalf("rendered prompt missing %q block", marker)
	}

	if a[:ia] != b[:ib] {
		t.Errorf("prompt prefix before <chunks> differs across batches — provider prefix cache will miss.\nA-prefix:\n%s\n---\nB-prefix:\n%s", a[:ia], b[:ib])
	}

	// The static rules and per-document candidate-slug block must live inside
	// that shared prefix, not after the varying chunks.
	for _, must := range []string{"### Primary task", "### JSON Formatting Rules", "\n<candidate_slugs>\n"} {
		if idx := strings.Index(a, must); idx < 0 || idx > ia {
			t.Errorf("%q must appear before <chunks> to be part of the cached prefix (idx=%d, chunks=%d)", must, idx, ia)
		}
	}
}

// TestWikiChunkCitationPrompt_PreservesPlaceholders guards against accidental
// loss of a template field during future reorders.
func TestWikiChunkCitationPrompt_PreservesPlaceholders(t *testing.T) {
	for _, field := range []string{"{{.Language}}", "{{.CandidateSlugs}}", "{{.ChunksXML}}"} {
		if !strings.Contains(WikiChunkCitationPrompt, field) {
			t.Errorf("WikiChunkCitationPrompt lost template field %q", field)
		}
	}
}

func TestWikiPageModifyPrompt_HidesInternalChunkAliases(t *testing.T) {
	for _, guidance := range []string{
		"NEVER output them in the page body or summary",
		"Source associations are stored separately by the system",
		"clean Markdown without inline chunk IDs",
	} {
		if !strings.Contains(WikiPageModifyPrompt, guidance) {
			t.Errorf("WikiPageModifyPrompt missing chunk-alias guidance %q", guidance)
		}
	}

	for _, obsolete := range []string{"Preserve Citations", "followed by an inline citation"} {
		if strings.Contains(WikiPageModifyPrompt, obsolete) {
			t.Errorf("WikiPageModifyPrompt still contains obsolete inline-citation rule %q", obsolete)
		}
	}
}
