package service

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// TestMergeCitationsIntoItems_PopulatesSourceChunksOnCandidates verifies that
// citations returned by the chunk-classification pass are attached back onto
// the matching candidate items while non-cited candidates are left untouched.
func TestMergeCitationsIntoItems_PopulatesSourceChunksOnCandidates(t *testing.T) {
	entities := []extractedItem{
		{Name: "Acme", Slug: "entity/acme"},
		{Name: "Beta", Slug: "entity/beta"},
	}
	concepts := []extractedItem{
		{Name: "RAG", Slug: "concept/rag"},
	}
	citations := map[string][]string{
		"entity/acme": {"chunk-1", "chunk-3"},
		"concept/rag": {"chunk-2"},
	}

	gotE, gotC, uncited := mergeCitationsIntoItems(entities, concepts, citations, nil)

	if len(gotE) != 2 || len(gotC) != 1 {
		t.Fatalf("expected 2 entities + 1 concept, got %d + %d", len(gotE), len(gotC))
	}
	acme := findBySlug(gotE, "entity/acme")
	if acme == nil {
		t.Fatalf("entity/acme missing from result")
	}
	if !equalStrings(acme.SourceChunks, []string{"chunk-1", "chunk-3"}) {
		t.Errorf("entity/acme source_chunks = %v, want [chunk-1 chunk-3]", acme.SourceChunks)
	}
	beta := findBySlug(gotE, "entity/beta")
	if beta == nil {
		t.Fatalf("entity/beta missing from result")
	}
	if len(beta.SourceChunks) != 0 {
		t.Errorf("entity/beta should have no citations, got %v", beta.SourceChunks)
	}
	rag := findBySlug(gotC, "concept/rag")
	if rag == nil {
		t.Fatalf("concept/rag missing")
	}
	if !equalStrings(rag.SourceChunks, []string{"chunk-2"}) {
		t.Errorf("concept/rag source_chunks = %v, want [chunk-2]", rag.SourceChunks)
	}
	if uncited != 1 {
		t.Errorf("uncited = %d, want 1", uncited)
	}
}

// TestMergeCitationsIntoItems_AddsNewSlugsAndUnionsChunksAcrossBatches checks
// that genuinely new slugs (ones Pass 0 missed) are appended to the right
// type slice, and that a slug surfacing in two batches ends up with the union
// of its source chunks.
func TestMergeCitationsIntoItems_AddsNewSlugsAndUnionsChunksAcrossBatches(t *testing.T) {
	entities := []extractedItem{
		{Name: "Known", Slug: "entity/known"},
	}
	concepts := []extractedItem{}

	newSlugs := []newSlugFromCitation{
		{
			Type:         "entity",
			Name:         "Fresh Entity",
			Slug:         "entity/fresh",
			Description:  "desc",
			Details:      "details",
			SourceChunks: []string{"c001", "c002"},
		},
		{
			// Same slug as above, appears in another batch — must union.
			Type:         "entity",
			Name:         "Fresh Entity",
			Slug:         "entity/fresh",
			SourceChunks: []string{"c002", "c003"},
		},
		{
			Type:         "concept",
			Name:         "New Concept",
			Slug:         "concept/new-concept",
			SourceChunks: []string{"c010"},
		},
		{
			// Duplicate of an existing candidate — should NOT produce a
			// duplicate entry (Known already exists in `entities`).
			Type:         "entity",
			Name:         "Known",
			Slug:         "entity/known",
			SourceChunks: []string{"c020"},
		},
	}

	gotE, gotC, _ := mergeCitationsIntoItems(entities, concepts, nil, newSlugs)

	if len(gotE) != 2 {
		t.Fatalf("expected 2 entities, got %d (%+v)", len(gotE), gotE)
	}
	if len(gotC) != 1 {
		t.Fatalf("expected 1 concept, got %d (%+v)", len(gotC), gotC)
	}
	fresh := findBySlug(gotE, "entity/fresh")
	if fresh == nil {
		t.Fatalf("entity/fresh missing")
	}
	sort.Strings(fresh.SourceChunks)
	if !equalStrings(fresh.SourceChunks, []string{"c001", "c002", "c003"}) {
		t.Errorf("entity/fresh source_chunks = %v, want union [c001 c002 c003]", fresh.SourceChunks)
	}
	newC := findBySlug(gotC, "concept/new-concept")
	if newC == nil || !equalStrings(newC.SourceChunks, []string{"c010"}) {
		t.Errorf("concept/new-concept missing or wrong chunks: %+v", newC)
	}
}

// TestSplitChunksIntoCitationBatches_RespectsBudgetAndOrder verifies that the
// batcher never puts too many runes in one batch, preserves document order,
// and that an oversized chunk gets its own batch.
func TestSplitChunksIntoCitationBatches_RespectsBudgetAndOrder(t *testing.T) {
	// Each small chunk is 5k runes → 3 of them should fit in one batch
	// (15k > 12k limit would spill to a second batch).
	mk := func(idx int, runes int, id string) *types.Chunk {
		return &types.Chunk{
			ID:         id,
			ChunkIndex: idx,
			Content:    repeatRune('a', runes),
			ChunkType:  types.ChunkTypeText,
		}
	}
	chunks := []*types.Chunk{
		mk(0, 5000, "id-0"),
		mk(1, 5000, "id-1"),
		mk(2, 5000, "id-2"), // this should start a new batch (15k > 12k)
		// An oversized chunk gets a dedicated batch.
		mk(3, 20000, "id-big"),
		mk(4, 1000, "id-small"),
	}
	batches := splitChunksIntoCitationBatches(chunks)
	if len(batches) < 3 {
		t.Fatalf("expected at least 3 batches, got %d", len(batches))
	}
	// All input IDs should show up in some batch, exactly once, in order.
	seen := []string{}
	for _, b := range batches {
		for _, c := range b.chunks {
			seen = append(seen, c.ID)
		}
	}
	wantOrder := []string{"id-0", "id-1", "id-2", "id-big", "id-small"}
	if !equalStrings(seen, wantOrder) {
		t.Errorf("batch order = %v, want %v", seen, wantOrder)
	}

	// Verify the typed handle table is populated per batch.
	for bi, b := range batches {
		if b.handles.Len() != len(b.chunks) {
			t.Errorf("batch %d handle count %d != chunk count %d", bi, b.handles.Len(), len(b.chunks))
		}
	}
}

// --- helpers ---

func findBySlug(items []extractedItem, slug string) *extractedItem {
	for i := range items {
		if items[i].Slug == slug {
			return &items[i]
		}
	}
	return nil
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func repeatRune(r rune, n int) string {
	out := make([]rune, n)
	for i := range out {
		out[i] = r
	}
	return string(out)
}

// --- Pass 0 batching (issue #5719) ---

// A long document must reach Pass 0 in full: the batches have to concatenate
// back to the original text, otherwise entities in the tail never get a page.
func TestSplitContentIntoExtractionBatchesCoversWholeDocument(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 1200; i++ {
		fmt.Fprintf(&sb, "paragraph %d carries some prose about the topic.\n\n", i)
	}
	content := sb.String()
	budget := 48000
	if len([]rune(content)) <= budget {
		t.Fatalf("fixture too short: %d runes", len([]rune(content)))
	}

	batches := splitContentIntoExtractionBatches(content, budget)

	if len(batches) < 2 {
		t.Fatalf("expected the document to be split, got %d batch(es)", len(batches))
	}
	if got := strings.Join(batches, ""); got != content {
		t.Fatalf("batches do not reconstruct the document: got %d runes, want %d",
			len([]rune(got)), len([]rune(content)))
	}
	for i, batch := range batches {
		if n := len([]rune(batch)); n > budget {
			t.Fatalf("batch %d is %d runes, over the %d budget", i, n, budget)
		}
	}
}

// A batch should end on a paragraph break rather than mid-sentence when the
// window contains one.
func TestSplitContentIntoExtractionBatchesPrefersParagraphBoundary(t *testing.T) {
	content := strings.Repeat("a", 100) + "\n\n" + strings.Repeat("b", 100)
	budget := 150

	batches := splitContentIntoExtractionBatches(content, budget)

	if len(batches) != 2 {
		t.Fatalf("expected 2 batches, got %d", len(batches))
	}
	if n := len([]rune(batches[0])); n >= budget {
		t.Fatalf("first batch is %d runes: it took the hard cut instead of the paragraph break", n)
	}
	if !strings.HasSuffix(batches[0], "\n") {
		t.Fatalf("first batch does not end on the paragraph break: %q", batches[0])
	}
	if got := strings.Join(batches, ""); got != content {
		t.Fatal("batches do not reconstruct the document")
	}
}

// A paragraph longer than the budget has no break to cut on, so it must be
// split by the hard cut instead of being dropped.
func TestSplitContentIntoExtractionBatchesKeepsOversizeParagraph(t *testing.T) {
	content := strings.Repeat("x", 250)
	budget := 100

	batches := splitContentIntoExtractionBatches(content, budget)

	if len(batches) != 3 {
		t.Fatalf("expected 3 batches, got %d", len(batches))
	}
	if got := strings.Join(batches, ""); got != content {
		t.Fatal("batches do not reconstruct the document")
	}
}

// The same entity extracted from two batches must collapse into one item with
// the aliases unioned, not two competing pages.
func TestMergeExtractedBySlugFoldsDuplicates(t *testing.T) {
	batches := [][]extractedItem{
		{{Name: "Acme", Slug: "entity/acme", Aliases: []string{"Acme Corp"}, Description: "first"}},
		{
			{Name: "Acme", Slug: "entity/acme", Aliases: []string{"ACME"}, Description: "second"},
			{Name: "Beta", Slug: "entity/beta"},
		},
	}

	got := mergeExtractedBySlug(batches)

	if len(got) != 2 {
		t.Fatalf("expected 2 merged items, got %d", len(got))
	}
	if got[0].Slug != "entity/acme" || got[1].Slug != "entity/beta" {
		t.Fatalf("order not preserved: %v", []string{got[0].Slug, got[1].Slug})
	}
	if got[0].Description != "first" {
		t.Fatalf("first occurrence should win, got %q", got[0].Description)
	}
	if !equalStrings(got[0].Aliases, []string{"Acme Corp", "ACME"}) {
		t.Fatalf("aliases not unioned: %v", got[0].Aliases)
	}
}
