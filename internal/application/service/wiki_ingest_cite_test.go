package service

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
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
			IsEnabled:  true,
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

func TestWikiCitationInputsIncludeEnabledTextualSources(t *testing.T) {
	chunks := []*types.Chunk{
		{ID: "enabled", ChunkIndex: 0, Content: "current fact", ChunkType: types.ChunkTypeText, IsEnabled: true},
		{ID: "disabled", ChunkIndex: 1, Content: "removed fact", ChunkType: types.ChunkTypeText, IsEnabled: false},
		{ID: "image", ChunkIndex: 2, Content: "caption", ChunkType: types.ChunkTypeImageCaption, IsEnabled: true},
	}

	ids := wikiTextChunkIDs(chunks)
	if !equalStrings(ids, []string{"enabled", "image"}) {
		t.Fatalf("wikiTextChunkIDs() = %v, want enabled text and caption chunks", ids)
	}

	batches := splitChunksIntoCitationBatches(chunks)
	if len(batches) != 1 || len(batches[0].chunks) != 2 ||
		batches[0].chunks[0].ID != "enabled" || batches[0].chunks[1].ID != "image" {
		t.Fatalf("citation batches did not retain enabled textual sources: %+v", batches)
	}

	enabled := enabledWikiIngestChunks(chunks)
	if len(enabled) != 2 || enabled[0].ID != "enabled" || enabled[1].ID != "image" {
		t.Fatalf("enabledWikiIngestChunks() = %+v, want enabled chunks only", enabled)
	}
}

type wikiSourceInvalidationPageService struct {
	interfaces.WikiPageService
	slugs []string
}

func (s wikiSourceInvalidationPageService) ListSlugsBySourceRef(
	context.Context, string, string,
) ([]string, error) {
	return append([]string(nil), s.slugs...), nil
}

type wikiReparseSlugUnionService struct {
	interfaces.WikiPageService
	interfaces.WikiProvenanceService
	legacy        []string
	structured    []string
	legacyErr     error
	structuredErr error
}

func (s wikiReparseSlugUnionService) ListSlugsBySourceRef(
	context.Context, string, string,
) ([]string, error) {
	return append([]string(nil), s.legacy...), s.legacyErr
}

func (s wikiReparseSlugUnionService) ListPageSlugsByKnowledgeSource(
	context.Context, string, string,
) ([]string, error) {
	return append([]string(nil), s.structured...), s.structuredErr
}

func TestReparsePageSnapshotUnionsLegacyAndStructuredSources(t *testing.T) {
	svc := &wikiIngestService{wikiService: wikiReparseSlugUnionService{
		legacy:     []string{"entity/legacy", "entity/shared", "index"},
		structured: []string{"entity/shared", "concept/structured"},
	}}
	got := svc.getExistingPageSlugsForKnowledge(context.Background(), "kb-1", "knowledge-1")
	if len(got) != 3 || !got["entity/legacy"] || !got["entity/shared"] || !got["concept/structured"] {
		t.Fatalf("reparse source union = %v, want three unique non-index slugs", got)
	}
}

func TestReparsePageSnapshotFailsClosedOnEitherSourceLookupError(t *testing.T) {
	tests := []struct {
		name string
		svc  wikiReparseSlugUnionService
	}{
		{
			name: "legacy lookup",
			svc: wikiReparseSlugUnionService{
				legacyErr: errors.New("legacy lookup unavailable"),
			},
		},
		{
			name: "structured lookup",
			svc: wikiReparseSlugUnionService{
				structuredErr: errors.New("structured lookup unavailable"),
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			svc := &wikiIngestService{wikiService: test.svc}
			if _, err := svc.listExistingPageSlugsForKnowledge(
				context.Background(), "kb-1", "knowledge-1",
			); err == nil {
				t.Fatal("strict source lookup unexpectedly succeeded")
			}
		})
	}
}

type wikiStrictReparseChunkRepository struct {
	interfaces.ChunkRepository
}

func (wikiStrictReparseChunkRepository) ListChunksByKnowledgeID(
	context.Context, uint64, string,
) ([]*types.Chunk, error) {
	return []*types.Chunk{{
		ID: "chunk-current", TenantID: 7, KnowledgeBaseID: "kb-1", KnowledgeID: "knowledge-1",
		ChunkType: types.ChunkTypeText, Content: "enough current source text", IsEnabled: true,
	}}, nil
}

func (wikiStrictReparseChunkRepository) ListChunksByParentIDs(
	context.Context, uint64, []string,
) ([]*types.Chunk, error) {
	return nil, nil
}

func TestMapOneDocumentRetriesWhenExistingPageLookupIsIncomplete(t *testing.T) {
	svc := &wikiIngestService{
		chunkRepo:   wikiStrictReparseChunkRepository{},
		spanTracker: &wikiAttemptLookupTracker{SpanTracker: noopSpanTracker{}, latest: 3},
		wikiService: wikiReparseSlugUnionService{
			legacyErr: errors.New("temporary source index failure"),
		},
		knowledgeSvc: wikiSourceInvalidationKnowledgeService{},
	}
	result, updates, err := svc.mapOneDocument(
		context.Background(), nil,
		WikiIngestPayload{TenantID: 7, KnowledgeBaseID: "kb-1"},
		WikiPendingOp{KnowledgeID: "knowledge-1", Attempt: 3, Language: "en-US"},
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "list existing wiki pages") {
		t.Fatalf("mapOneDocument() error = %v, want strict reverse-lookup failure", err)
	}
	if result != nil || len(updates) != 0 {
		t.Fatalf("failed strict lookup produced output: result=%+v updates=%+v", result, updates)
	}
}

type wikiSourceInvalidationKnowledgeService struct {
	interfaces.KnowledgeService
}

func (wikiSourceInvalidationKnowledgeService) GetKnowledgeByIDOnly(
	context.Context, string,
) (*types.Knowledge, error) {
	return &types.Knowledge{
		Title: "Edited document", KnowledgeBaseID: "kb-1", ParseStatus: types.ParseStatusCompleted,
	}, nil
}

func TestMapWikiDocumentWithoutUsableContentRetractsExistingSources(t *testing.T) {
	svc := &wikiIngestService{
		wikiService: wikiSourceInvalidationPageService{
			slugs: []string{"entity/a", "index", "concept/b", "entity/a"},
		},
		knowledgeSvc: wikiSourceInvalidationKnowledgeService{},
	}
	result, updates, err := svc.mapWikiDocumentWithoutUsableContent(
		context.Background(),
		WikiIngestPayload{TenantID: 7, KnowledgeBaseID: "kb-1"},
		WikiPendingOp{KnowledgeID: "knowledge-1", Attempt: 12, Language: "en-US"},
		nil, nil, "no_enabled_chunks",
	)
	if err != nil {
		t.Fatalf("mapWikiDocumentWithoutUsableContent() error = %v", err)
	}
	if result == nil || result.KnowledgeID != "knowledge-1" || result.Attempt != 12 || result.DocTitle != "Edited document" || !result.SourceInvalidated {
		t.Fatalf("unexpected invalidation result: %+v", result)
	}
	if len(updates) != 2 {
		t.Fatalf("invalidation updates = %+v, want two unique non-index slugs", updates)
	}
	for _, update := range updates {
		if update.Type != "retract" || update.KnowledgeID != "knowledge-1" || update.KnowledgeAttempt != 12 {
			t.Fatalf("unexpected invalidation update: %+v", update)
		}
	}
}

func TestMapWikiDocumentWithoutUsableContentUnionsStructuredSources(t *testing.T) {
	svc := &wikiIngestService{
		wikiService: wikiReparseSlugUnionService{
			legacy:     []string{"entity/legacy", "entity/shared"},
			structured: []string{"entity/shared", "concept/structured"},
		},
		knowledgeSvc: wikiSourceInvalidationKnowledgeService{},
	}
	_, updates, err := svc.mapWikiDocumentWithoutUsableContent(
		context.Background(),
		WikiIngestPayload{TenantID: 7, KnowledgeBaseID: "kb-1"},
		WikiPendingOp{KnowledgeID: "knowledge-1", Attempt: 12, Language: "en-US"},
		nil, nil, "no_enabled_chunks",
	)
	if err != nil {
		t.Fatalf("mapWikiDocumentWithoutUsableContent() error = %v", err)
	}
	got := make(map[string]bool, len(updates))
	for _, update := range updates {
		got[update.Slug] = true
	}
	if len(got) != 3 || !got["entity/legacy"] || !got["entity/shared"] || !got["concept/structured"] {
		t.Fatalf("source invalidation union = %v, want legacy and structured slugs", got)
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
