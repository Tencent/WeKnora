package service

import (
	"context"
	"sort"
	"testing"

	"github.com/Tencent/WeKnora/internal/artifact"
	"github.com/Tencent/WeKnora/internal/config"
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
	logicalCitations := map[string][]LogicalChunkRef{
		"entity/acme": {
			{LogicalChunkKey: "lk-1", ContentDigest: "digest-1"},
			{LogicalChunkKey: "lk-3", ContentDigest: "digest-3"},
		},
	}

	gotE, gotC, uncited := mergeCitationsIntoItems(entities, concepts, citations, logicalCitations, nil)

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
	if len(acme.SourceChunkRefs) != 2 || acme.SourceChunkRefs[0].LogicalChunkKey != "lk-1" {
		t.Errorf("entity/acme source_chunk_refs = %+v, want logical refs", acme.SourceChunkRefs)
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

	gotE, gotC, _ := mergeCitationsIntoItems(entities, concepts, nil, nil, newSlugs)

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

func TestClassifyChunkCitationsRebindsCachedHandlesToCurrentGenerationChunks(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	store := newMultimodalArtifactStore()
	svc := &wikiIngestService{
		config: &config.Config{ArtifactCache: &config.ArtifactCacheConfig{Stages: map[string]bool{
			"wiki_map": true,
		}}},
		artifactRuntime: artifact.NewRuntime(store, artifact.RuntimeOptions{
			ReadEnabled: true, WriteEnabled: true, MaxInlineBytes: 4096,
		}),
	}
	model := &fakeArtifactChat{out: []string{`{
		"citations": {"entity/acme": ["c000"]},
		"new_slugs": [{
			"type": "entity",
			"name": "Beta",
			"slug": "entity/beta",
			"description": "desc",
			"details": "details",
			"source_chunks": ["c001"]
		}]
	}`}}
	candidatesXML := "- slug: entity/acme, type: entity, name: \"Acme\", description: desc"
	chunksForGeneration := func(generationID string) []*types.Chunk {
		return []*types.Chunk{
			{
				ID:              generationID + "-physical-a",
				GenerationID:    generationID,
				LogicalChunkKey: "logical-a",
				ArtifactDigest:  "digest-a",
				ChunkIndex:      0,
				ChunkType:       types.ChunkTypeText,
				Content:         "Acme builds retrieval systems.",
			},
			{
				ID:              generationID + "-physical-b",
				GenerationID:    generationID,
				LogicalChunkKey: "logical-b",
				ArtifactDigest:  "digest-b",
				ChunkIndex:      1,
				ChunkType:       types.ChunkTypeText,
				Content:         "Beta appears in the same document.",
			},
		}
	}

	firstCitations, firstLogical, firstNew, firstBatches := svc.classifyChunkCitations(
		ctx, model, candidatesXML, chunksForGeneration("generation-1"), "en", nil,
	)
	secondCitations, secondLogical, secondNew, secondBatches := svc.classifyChunkCitations(
		ctx, model, candidatesXML, chunksForGeneration("generation-2"), "en", nil,
	)

	if firstBatches != 1 || secondBatches != 1 {
		t.Fatalf("batch counts = %d/%d, want one batch per generation", firstBatches, secondBatches)
	}
	if model.calls != 1 {
		t.Fatalf("wiki_map provider calls = %d, want 1 total and cache hit on generation retry", model.calls)
	}
	if !equalStrings(firstCitations["entity/acme"], []string{"generation-1-physical-a"}) {
		t.Fatalf("first physical citations = %v", firstCitations["entity/acme"])
	}
	if !equalStrings(secondCitations["entity/acme"], []string{"generation-2-physical-a"}) {
		t.Fatalf("cached citation handles rebound to %v, want generation-2 physical chunk", secondCitations["entity/acme"])
	}
	wantLogical := []LogicalChunkRef{{LogicalChunkKey: "logical-a", ContentDigest: "digest-a"}}
	if len(firstLogical["entity/acme"]) != 1 || firstLogical["entity/acme"][0] != wantLogical[0] {
		t.Fatalf("first logical refs = %+v, want %+v", firstLogical["entity/acme"], wantLogical)
	}
	if len(secondLogical["entity/acme"]) != 1 || secondLogical["entity/acme"][0] != wantLogical[0] {
		t.Fatalf("cached logical refs = %+v, want %+v", secondLogical["entity/acme"], wantLogical)
	}
	if len(firstNew) != 1 || !equalStrings(firstNew[0].SourceChunks, []string{"generation-1-physical-b"}) {
		t.Fatalf("first new slug source chunks = %+v", firstNew)
	}
	if len(secondNew) != 1 || !equalStrings(secondNew[0].SourceChunks, []string{"generation-2-physical-b"}) {
		t.Fatalf("cached new slug source chunks = %+v, want generation-2 physical chunk", secondNew)
	}
	if len(secondNew[0].SourceChunkRefs) != 1 ||
		secondNew[0].SourceChunkRefs[0] != (LogicalChunkRef{LogicalChunkKey: "logical-b", ContentDigest: "digest-b"}) {
		t.Fatalf("cached new slug logical refs = %+v", secondNew[0].SourceChunkRefs)
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
