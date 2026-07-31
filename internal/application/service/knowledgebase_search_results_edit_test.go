package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestIsSearchableChunkSkipsUnsynchronizedEdits(t *testing.T) {
	service := &knowledgeBaseService{}
	for _, status := range []string{"processing", "failed"} {
		chunk := &types.Chunk{ChunkType: types.ChunkTypeText, IndexStatus: status, IsEnabled: true}
		if service.isSearchableChunk(chunk) {
			t.Fatalf("chunk with index status %q should not be searchable", status)
		}
	}
	for _, status := range []string{"", "ready"} {
		chunk := &types.Chunk{ChunkType: types.ChunkTypeText, IndexStatus: status, IsEnabled: true}
		if !service.isSearchableChunk(chunk) {
			t.Fatalf("chunk with index status %q should be searchable", status)
		}
	}
}

func TestIsSearchableChunkSkipsDisabledChunk(t *testing.T) {
	service := &knowledgeBaseService{}
	chunk := &types.Chunk{
		ChunkType:   types.ChunkTypeFAQ,
		IndexStatus: "ready",
		IsEnabled:   false,
	}
	if service.isSearchableChunk(chunk) {
		t.Fatal("disabled FAQ chunk should never be searchable")
	}
}

func TestFilterActiveGenerationChunks(t *testing.T) {
	chunks := []*types.Chunk{
		{ID: "active", KnowledgeID: "k-active", GenerationID: "gen-2"},
		{ID: "hidden", KnowledgeID: "k-active", GenerationID: "gen-1"},
		{ID: "legacy", KnowledgeID: "k-legacy"},
		{ID: "legacy-hidden", KnowledgeID: "k-legacy", GenerationID: "gen-old"},
		{ID: "unknown", KnowledgeID: "k-missing", GenerationID: "gen-any"},
	}
	knowledgeMap := map[string]*types.Knowledge{
		"k-active": {ID: "k-active", ActiveGenerationID: "gen-2"},
		"k-legacy": {ID: "k-legacy"},
	}

	filtered := filterActiveGenerationChunks(chunks, knowledgeMap)

	got := make([]string, 0, len(filtered))
	for _, chunk := range filtered {
		got = append(got, chunk.ID)
	}
	want := []string{"active", "legacy", "unknown"}
	if len(got) != len(want) {
		t.Fatalf("filtered chunks = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("filtered chunks = %v, want %v", got, want)
		}
	}
}

func TestCandidatesForSearchHydrationUsesOverretrievedCandidatesBeforeFiltering(t *testing.T) {
	chunks := []*types.IndexWithScore{
		{ChunkID: "stale-high"},
		{ChunkID: "stale-next"},
		{ChunkID: "active-later"},
	}

	plain := candidatesForSearchHydration(chunks, types.SearchParams{MatchCount: 1})
	if len(plain) != len(chunks) {
		t.Fatalf("hydration candidates = %d, want %d", len(plain), len(chunks))
	}

	empty := candidatesForSearchHydration(chunks, types.SearchParams{MatchCount: 0})
	if len(empty) != 0 {
		t.Fatalf("zero match count candidates = %d, want 0", len(empty))
	}
}

func TestLimitSearchResultsAppliesMatchCountAfterHydration(t *testing.T) {
	results := []*types.SearchResult{
		{ID: "active-1"},
		{ID: "active-2"},
		{ID: "active-3"},
	}

	limited := limitSearchResults(results, 2)
	if len(limited) != 2 {
		t.Fatalf("limited results = %d, want 2", len(limited))
	}
	if limited[0].ID != "active-1" || limited[1].ID != "active-2" {
		t.Fatalf("limited result order changed: %#v", limited)
	}

	empty := limitSearchResults(results, 0)
	if len(empty) != 0 {
		t.Fatalf("zero match count results = %d, want 0", len(empty))
	}
}

func TestSearchRefillDecision(t *testing.T) {
	if !shouldRefillSearchResults(2, 3, 50, 50, 2000) {
		t.Fatal("expected refill when post-filtered results are short and backend filled TopK")
	}
	if shouldRefillSearchResults(3, 3, 50, 50, 2000) {
		t.Fatal("must not refill once target result count is reached")
	}
	if shouldRefillSearchResults(2, 3, 20, 50, 2000) {
		t.Fatal("must not refill when backend returned fewer results than requested")
	}
	if shouldRefillSearchResults(2, 3, 2000, 2000, 2000) {
		t.Fatal("must not refill past the configured cap")
	}
}

func TestNextSearchRefillTopK(t *testing.T) {
	next, ok := nextSearchRefillTopK(50, 2000)
	if !ok || next != 100 {
		t.Fatalf("next topK = %d/%v, want 100/true", next, ok)
	}
	next, ok = nextSearchRefillTopK(1500, 2000)
	if !ok || next != 2000 {
		t.Fatalf("capped next topK = %d/%v, want 2000/true", next, ok)
	}
	next, ok = nextSearchRefillTopK(2000, 2000)
	if ok || next != 2000 {
		t.Fatalf("at-cap next topK = %d/%v, want 2000/false", next, ok)
	}
}

func TestSearchRefillIterationLimitReachesTopKCap(t *testing.T) {
	if got := searchRefillIterationLimit(50, 2000); got != 7 {
		t.Fatalf("iteration limit = %d, want 7 for 50->100->200->400->800->1600->2000", got)
	}
	if got := searchRefillIterationLimit(2000, 2000); got != 1 {
		t.Fatalf("iteration limit at cap = %d, want 1", got)
	}
}

func TestPopulateGenerationFiltersIncludesSharedKnowledge(t *testing.T) {
	db := setupKnowledgeSharedAccessDB(t)
	seedKnowledge(t, db, &types.Knowledge{
		ID:                 "k-shared",
		TenantID:           2,
		KnowledgeBaseID:    "kb-shared",
		ActiveGenerationID: "gen-shared",
	})
	svc := &knowledgeBaseService{
		kgRepo: repository.NewKnowledgeRepository(db),
		kbShareService: &fakeKBShareService{
			allowedKBs: map[string]bool{"kb-shared": true},
		},
	}
	params := types.SearchParams{KnowledgeIDs: []string{"k-shared"}}

	svc.populateGenerationFilters(newSharedAccessContext(), 1, nil, &params)

	if len(params.GenerationIDs) != 1 || params.GenerationIDs[0] != "gen-shared" {
		t.Fatalf("generation filters = %v, want [gen-shared]", params.GenerationIDs)
	}
	if len(params.VisibilityKeys) != 1 || params.VisibilityKeys[0] != "k-shared:gen-shared" {
		t.Fatalf("visibility filters = %v, want [k-shared:gen-shared]", params.VisibilityKeys)
	}
}

func TestPopulateGenerationFiltersForWholeKB(t *testing.T) {
	db := setupKnowledgeSharedAccessDB(t)
	seedKnowledge(t, db, &types.Knowledge{
		ID:                 "k-1",
		TenantID:           1,
		KnowledgeBaseID:    "kb-1",
		ActiveGenerationID: "gen-1",
	})
	seedKnowledge(t, db, &types.Knowledge{
		ID:                 "k-2",
		TenantID:           1,
		KnowledgeBaseID:    "kb-1",
		ActiveGenerationID: "gen-2",
	})
	svc := &knowledgeBaseService{kgRepo: repository.NewKnowledgeRepository(db)}
	params := types.SearchParams{}

	svc.populateGenerationFilters(newSharedAccessContext(), 1, []*types.KnowledgeBase{{ID: "kb-1", TenantID: 1}}, &params)

	if len(params.GenerationIDs) != 2 {
		t.Fatalf("generation filters = %v, want 2 active generations", params.GenerationIDs)
	}
	if len(params.VisibilityKeys) != 2 {
		t.Fatalf("visibility filters = %v, want 2 active visibility keys", params.VisibilityKeys)
	}
}

func TestPopulateGenerationFiltersForWholeKBPreservesLegacyFallback(t *testing.T) {
	db := setupKnowledgeSharedAccessDB(t)
	seedKnowledge(t, db, &types.Knowledge{
		ID:                 "k-generation",
		TenantID:           1,
		KnowledgeBaseID:    "kb-1",
		ActiveGenerationID: "gen-1",
	})
	seedKnowledge(t, db, &types.Knowledge{
		ID:              "k-legacy",
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
	})
	svc := &knowledgeBaseService{kgRepo: repository.NewKnowledgeRepository(db)}
	params := types.SearchParams{}

	svc.populateGenerationFilters(newSharedAccessContext(), 1, []*types.KnowledgeBase{{ID: "kb-1", TenantID: 1}}, &params)

	if len(params.GenerationIDs) != 0 || len(params.VisibilityKeys) != 0 {
		t.Fatalf("mixed legacy/generation KB must not apply backend-only filters, got generations=%v visibility=%v",
			params.GenerationIDs, params.VisibilityKeys)
	}
}
