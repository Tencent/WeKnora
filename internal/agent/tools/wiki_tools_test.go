package tools

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type fakeWikiPageService struct {
	interfaces.WikiPageService

	mu            sync.Mutex
	pages         map[string]*types.WikiPage
	searchResults map[string][]*types.WikiPage
	getErrors     map[string]error
	searchErrors  map[string]error
	getCalls      []string
	searchCalls   []string
}

func wikiPageKey(kbID, slug string) string {
	return kbID + "\x00" + slug
}

func (f *fakeWikiPageService) GetPageBySlug(_ context.Context, kbID, slug string) (*types.WikiPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getCalls = append(f.getCalls, wikiPageKey(kbID, slug))
	if err := f.getErrors[wikiPageKey(kbID, slug)]; err != nil {
		return nil, err
	}
	return f.pages[wikiPageKey(kbID, slug)], nil
}

func (f *fakeWikiPageService) SearchPages(_ context.Context, kbID, _ string, _ int) ([]*types.WikiPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.searchCalls = append(f.searchCalls, kbID)
	if err := f.searchErrors[kbID]; err != nil {
		return nil, err
	}
	return f.searchResults[kbID], nil
}

func (f *fakeWikiPageService) ListSourceChunksBySlug(ctx context.Context, kbID, slug string) (*types.WikiPageSourceChunksResult, error) {
	page, err := f.GetPageBySlug(ctx, kbID, slug)
	if err != nil {
		return nil, err
	}
	if page == nil {
		return nil, repository.ErrWikiPageNotFound
	}
	out := &types.WikiPageSourceChunksResult{
		KnowledgeBaseID: page.KnowledgeBaseID,
		Slug:            page.Slug,
		Title:           page.Title,
		PageType:        page.PageType,
		Chunks:          []types.WikiPageSourceChunk{},
	}
	for _, ref := range page.SourceRefs {
		id, title := ref, ""
		if i := strings.Index(ref, "|"); i >= 0 {
			id, title = ref[:i], ref[i+1:]
		}
		out.Sources = append(out.Sources, types.WikiAssocSource{KnowledgeID: id, Title: title})
	}
	seen := map[string]struct{}{}
	for _, id := range page.ChunkRefs {
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out.ChunkRefCount++
		out.Chunks = append(out.Chunks, types.WikiPageSourceChunk{
			ID:      id,
			Content: "original text for " + id,
		})
	}
	if out.ChunkRefCount == 0 {
		out.Reason = types.WikiSourceChunksReasonNoRefs
	}
	return out, nil
}

func (f *fakeWikiPageService) AssociateLeaves(ctx context.Context, kbID, query string, limit int, _ string) (*types.WikiKnowledgeAssocResult, error) {
	pages, err := f.SearchPages(ctx, kbID, query, limit)
	if err != nil {
		return nil, err
	}
	leaves := make([]*types.WikiKnowledgeAssocLeaf, 0, len(pages))
	for _, p := range pages {
		if p == nil {
			continue
		}
		leaves = append(leaves, &types.WikiKnowledgeAssocLeaf{
			Slug:         p.Slug,
			Title:        p.Title,
			PageType:     p.PageType,
			Aliases:      p.Aliases,
			Summary:      p.Summary,
			Content:      p.Content,
			CategoryPath: p.CategoryPath,
			WikiPath:     p.WikiPath,
			InLinks:      p.InLinks,
			OutLinks:     p.OutLinks,
		})
	}
	tree := []*types.WikiKnowledgeAssocNode{}
	if len(leaves) > 0 {
		tree = []*types.WikiKnowledgeAssocNode{{Leaves: leaves}}
	}
	return &types.WikiKnowledgeAssocResult{Query: query, Tree: tree, Pages: pages}, nil
}

func (f *fakeWikiPageService) resetCalls() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getCalls = nil
	f.searchCalls = nil
}

func newTestWikiPage(kbID, slug string) *types.WikiPage {
	return &types.WikiPage{
		KnowledgeBaseID: kbID,
		Slug:            slug,
		Title:           slug,
		PageType:        types.WikiPageTypeConcept,
		Content:         "content for " + slug,
	}
}

func TestTruncateForSummary(t *testing.T) {
	tests := []struct {
		name    string
		content string
		maxLen  int
		want    string
	}{
		{
			name:    "short content",
			content: "Hello world",
			maxLen:  50,
			want:    "Hello world",
		},
		{
			name:    "strips heading prefix",
			content: "# My Title\n\nSome content",
			maxLen:  50,
			want:    "My Title",
		},
		{
			name:    "strips h2 prefix",
			content: "## Section\n\nContent here",
			maxLen:  50,
			want:    "Section",
		},
		{
			name:    "truncates long content",
			content: "This is a very long paragraph that should be truncated at some point because it exceeds the maximum length",
			maxLen:  20,
			want:    "This is a very long ..."},
		{
			name:    "takes first paragraph",
			content: "First paragraph.\n\nSecond paragraph.",
			maxLen:  100,
			want:    "First paragraph.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateForSummary(tt.content, tt.maxLen)
			if got != tt.want {
				t.Errorf("truncateForSummary() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWikiToolConstants(t *testing.T) {
	// Verify all wiki tool constants are defined and unique
	names := []string{
		ToolWikiReadPage,
		ToolWikiSearch,
		ToolWikiListSourceChunks,
	}

	seen := make(map[string]bool)
	for _, name := range names {
		if name == "" {
			t.Error("Wiki tool constant is empty")
		}
		if seen[name] {
			t.Errorf("Duplicate wiki tool constant: %s", name)
		}
		seen[name] = true

		// Verify name length is within OpenAI limit
		if len(name) > maxFunctionNameLength {
			t.Errorf("Wiki tool name too long: %s (%d chars, max %d)", name, len(name), maxFunctionNameLength)
		}
	}
}

func TestWikiToolsInAvailableDefinitions(t *testing.T) {
	defs := AvailableToolDefinitions()
	wikiTools := map[string]bool{
		ToolWikiReadPage:         false,
		ToolWikiSearch:           false,
		ToolWikiListSourceChunks: false,
	}

	for _, def := range defs {
		if _, ok := wikiTools[def.Name]; ok {
			wikiTools[def.Name] = true
		}
	}

	for name, found := range wikiTools {
		if !found {
			t.Errorf("Wiki tool %s missing from AvailableToolDefinitions()", name)
		}
	}
}

func TestWikiReadPageSchemaUsesAutomaticKBRouting(t *testing.T) {
	tool := NewWikiReadPageTool(
		&fakeWikiPageService{}, nil,
		NewWikiScopesFromKBIDs([]string{"kb-1"}), NewWikiRouteResolver(),
	)
	if strings.Contains(string(tool.Parameters()), "knowledge_base_id") {
		t.Fatalf("wiki_read_page schema must not ask the model to choose a knowledge base: %s", tool.Parameters())
	}
}

func TestWikiReadPageRoutesEachSlugIndependently(t *testing.T) {
	pageA := newTestWikiPage("kb-1", "concept/a")
	pageB := newTestWikiPage("kb-2", "concept/b")
	service := &fakeWikiPageService{pages: map[string]*types.WikiPage{
		wikiPageKey("kb-1", pageA.Slug): pageA,
		wikiPageKey("kb-2", pageB.Slug): pageB,
	}}
	routes := NewWikiRouteResolver()
	routes.remember(pageA.Slug, "kb-1")
	routes.remember(pageB.Slug, "kb-2")
	tool := NewWikiReadPageTool(
		service, nil, NewWikiScopesFromKBIDs([]string{"kb-1", "kb-2"}), routes,
	)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"slugs":["concept/a","concept/b"]}`))
	if err != nil || result == nil || !result.Success {
		t.Fatalf("wiki_read_page failed: result=%+v err=%v", result, err)
	}
	wantCalls := []string{
		wikiPageKey("kb-1", "concept/a"),
		wikiPageKey("kb-2", "concept/a"),
		wikiPageKey("kb-2", "concept/b"),
		wikiPageKey("kb-1", "concept/b"),
	}
	if !reflect.DeepEqual(service.getCalls, wantCalls) {
		t.Fatalf("GetPageBySlug calls = %v, want %v", service.getCalls, wantCalls)
	}
}

func TestWikiReadPageFallsBackWhenCachedRouteIsStale(t *testing.T) {
	page := newTestWikiPage("kb-2", "concept/target")
	service := &fakeWikiPageService{pages: map[string]*types.WikiPage{
		wikiPageKey("kb-2", page.Slug): page,
	}}
	routes := NewWikiRouteResolver()
	routes.remember(page.Slug, "kb-1")
	tool := NewWikiReadPageTool(
		service, nil, NewWikiScopesFromKBIDs([]string{"kb-1", "kb-2"}), routes,
	)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"slugs":["concept/target"]}`))
	if err != nil || result == nil || !result.Success {
		t.Fatalf("wiki_read_page should recover from a stale route: result=%+v err=%v", result, err)
	}
	wantCalls := []string{
		wikiPageKey("kb-1", page.Slug),
		wikiPageKey("kb-2", page.Slug),
	}
	if !reflect.DeepEqual(service.getCalls, wantCalls) {
		t.Fatalf("GetPageBySlug calls = %v, want stale route then fallback %v", service.getCalls, wantCalls)
	}
}

func TestWikiReadPageDoesNotLetLegacyKBArgumentPinAllSlugs(t *testing.T) {
	page := newTestWikiPage("kb-2", "concept/target")
	service := &fakeWikiPageService{pages: map[string]*types.WikiPage{
		wikiPageKey("kb-2", page.Slug): page,
	}}
	tool := NewWikiReadPageTool(
		service, nil,
		NewWikiScopesFromKBIDs([]string{"kb-1", "kb-2"}), NewWikiRouteResolver(),
	)

	result, err := tool.Execute(context.Background(), json.RawMessage(
		`{"slugs":["concept/target"],"knowledge_base_id":"b2"}`,
	))
	if err != nil || result == nil || !result.Success {
		t.Fatalf("legacy KB argument must not pin wiki_read_page: result=%+v err=%v", result, err)
	}
	wantCalls := []string{
		wikiPageKey("kb-1", page.Slug),
		wikiPageKey("kb-2", page.Slug),
	}
	if !reflect.DeepEqual(service.getCalls, wantCalls) {
		t.Fatalf("GetPageBySlug calls = %v, want automatic scope scan %v", service.getCalls, wantCalls)
	}
}

func TestWikiSearchSharesRoutesWithWikiReadPage(t *testing.T) {
	page := newTestWikiPage("kb-2", "concept/target")
	service := &fakeWikiPageService{
		pages: map[string]*types.WikiPage{
			wikiPageKey("kb-2", page.Slug): page,
		},
		searchResults: map[string][]*types.WikiPage{
			"kb-2": {page},
		},
	}
	scopes := NewWikiScopesFromKBIDs([]string{"kb-1", "kb-2"})
	routes := NewWikiRouteResolver()
	searchTool := NewWikiSearchTool(service, nil, scopes, routes)
	readTool := NewWikiReadPageTool(service, nil, scopes, routes)

	searchResult, err := searchTool.Execute(context.Background(), json.RawMessage(`{"queries":["target"]}`))
	if err != nil || searchResult == nil || !searchResult.Success {
		t.Fatalf("wiki_search failed: result=%+v err=%v", searchResult, err)
	}
	if !strings.Contains(searchResult.Output, "[[concept/target|concept/target]]") {
		t.Fatalf("wiki_search output missing associated leaf link: %s", searchResult.Output)
	}
	service.resetCalls()

	readResult, err := readTool.Execute(context.Background(), json.RawMessage(`{"slugs":["concept/target"]}`))
	if err != nil || readResult == nil || !readResult.Success {
		t.Fatalf("wiki_read_page failed: result=%+v err=%v", readResult, err)
	}
	wantCalls := []string{
		wikiPageKey("kb-2", page.Slug),
		wikiPageKey("kb-1", page.Slug),
	}
	if !reflect.DeepEqual(service.getCalls, wantCalls) {
		t.Fatalf("GetPageBySlug calls = %v, want cached owner first and full ambiguity scan %v", service.getCalls, wantCalls)
	}
}

func TestWikiReadPageCachedRouteCannotHideDuplicateSlug(t *testing.T) {
	slug := "concept/shared"
	service := &fakeWikiPageService{pages: map[string]*types.WikiPage{
		wikiPageKey("kb-1", slug): newTestWikiPage("kb-1", slug),
		wikiPageKey("kb-2", slug): newTestWikiPage("kb-2", slug),
	}}
	routes := NewWikiRouteResolver()
	routes.remember(slug, "kb-1")
	tool := NewWikiReadPageTool(
		service, nil, NewWikiScopesFromKBIDs([]string{"kb-1", "kb-2"}), routes,
	)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"slugs":["concept/shared"]}`))
	if err != nil || result == nil || !result.Success {
		t.Fatalf("wiki_read_page failed: result=%+v err=%v", result, err)
	}
	if !strings.Contains(result.Output, "<knowledge_base_id>kb-1</knowledge_base_id>") ||
		!strings.Contains(result.Output, "<knowledge_base_id>kb-2</knowledge_base_id>") {
		t.Fatalf("cached provenance hid an ambiguous page: %s", result.Output)
	}
}

func TestWikiReadPageReturnsSameSlugFromAllScopesOnCacheMiss(t *testing.T) {
	slug := "concept/shared"
	pageA := newTestWikiPage("kb-1", slug)
	pageB := newTestWikiPage("kb-2", slug)
	service := &fakeWikiPageService{pages: map[string]*types.WikiPage{
		wikiPageKey("kb-1", slug): pageA,
		wikiPageKey("kb-2", slug): pageB,
	}}
	tool := NewWikiReadPageTool(
		service, nil, NewWikiScopesFromKBIDs([]string{"kb-1", "kb-2"}), NewWikiRouteResolver(),
	)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"slugs":["concept/shared"]}`))
	if err != nil || result == nil || !result.Success {
		t.Fatalf("wiki_read_page failed: result=%+v err=%v", result, err)
	}
	if !strings.Contains(result.Output, "<knowledge_base_id>kb-1</knowledge_base_id>") ||
		!strings.Contains(result.Output, "<knowledge_base_id>kb-2</knowledge_base_id>") {
		t.Fatalf("expected pages from both scopes, got: %s", result.Output)
	}
}

func TestWikiSearchRejectsKnowledgeBaseOutsideScope(t *testing.T) {
	service := &fakeWikiPageService{}
	tool := NewWikiSearchTool(
		service, nil, NewWikiScopesFromKBIDs([]string{"kb-1"}), NewWikiRouteResolver(),
	)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"queries":["target"],"knowledge_base_id":"kb-outside"}`))
	if err != nil {
		t.Fatalf("wiki_search returned unexpected error: %v", err)
	}
	if result == nil || result.Success || !strings.Contains(result.Error, "not within") {
		t.Fatalf("expected scope rejection, got: %+v", result)
	}
	if len(service.searchCalls) != 0 {
		t.Fatalf("out-of-scope search must not reach the service, calls=%v", service.searchCalls)
	}
}

func TestWikiReadPageSurfacesBackendFailureInsteadOfNotFound(t *testing.T) {
	service := &fakeWikiPageService{getErrors: map[string]error{
		wikiPageKey("kb-1", "concept/a"): errors.New("database unavailable"),
	}}
	tool := NewWikiReadPageTool(
		service, nil, NewWikiScopesFromKBIDs([]string{"kb-1"}), NewWikiRouteResolver(),
	)
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"slugs":["concept/a"]}`))
	if err != nil || result == nil || result.Success || !strings.Contains(result.Error, "database unavailable") {
		t.Fatalf("backend failure must be preserved: result=%+v err=%v", result, err)
	}
	if strings.Contains(result.Error, "not found") {
		t.Fatalf("backend failure must not be rewritten as not found: %s", result.Error)
	}
}

func TestWikiSearchFailsWhenEveryAllowedBackendLookupFails(t *testing.T) {
	service := &fakeWikiPageService{searchErrors: map[string]error{
		"kb-1": errors.New("search backend unavailable"),
	}}
	tool := NewWikiSearchTool(
		service, nil, NewWikiScopesFromKBIDs([]string{"kb-1"}), NewWikiRouteResolver(),
	)
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"queries":["target"]}`))
	if err != nil || result == nil || result.Success || !strings.Contains(result.Error, "search backend unavailable") {
		t.Fatalf("search backend failure must be preserved: result=%+v err=%v", result, err)
	}
}

func TestResolveUniqueWikiPageRejectsAmbiguousMutationTarget(t *testing.T) {
	slug := "concept/shared"
	service := &fakeWikiPageService{pages: map[string]*types.WikiPage{
		wikiPageKey("kb-1", slug): newTestWikiPage("kb-1", slug),
		wikiPageKey("kb-2", slug): newTestWikiPage("kb-2", slug),
	}}
	_, _, err := resolveUniqueWikiPage(
		context.Background(), service, slug, []string{"kb-1", "kb-2"}, NewWikiRouteResolver(),
	)
	if err == nil || !strings.Contains(err.Error(), "multiple knowledge bases") {
		t.Fatalf("expected ambiguous target rejection, got %v", err)
	}
}

func TestResolveUniqueWikiPageUsesActualOwnerWithoutModelKBArgument(t *testing.T) {
	slug := "concept/target"
	service := &fakeWikiPageService{pages: map[string]*types.WikiPage{
		wikiPageKey("kb-2", slug): newTestWikiPage("kb-2", slug),
	}}
	page, kbID, err := resolveUniqueWikiPage(
		context.Background(), service, slug, []string{"kb-1", "kb-2"}, NewWikiRouteResolver(),
	)
	if err != nil || page == nil || kbID != "kb-2" {
		t.Fatalf("target resolution failed: page=%+v kb=%s err=%v", page, kbID, err)
	}
}

func TestResolveUniqueWikiPageSkipsOnlyNotFoundErrors(t *testing.T) {
	slug := "concept/target"
	service := &fakeWikiPageService{
		pages: map[string]*types.WikiPage{
			wikiPageKey("kb-2", slug): newTestWikiPage("kb-2", slug),
		},
		getErrors: map[string]error{
			wikiPageKey("kb-1", slug): repository.ErrWikiPageNotFound,
		},
	}
	page, kbID, err := resolveUniqueWikiPage(
		context.Background(), service, slug, []string{"kb-1", "kb-2"}, NewWikiRouteResolver(),
	)
	if err != nil || page == nil || kbID != "kb-2" {
		t.Fatalf("not-found should continue to other allowed scopes: page=%+v kb=%s err=%v", page, kbID, err)
	}

	service.getErrors[wikiPageKey("kb-1", slug)] = errors.New("database unavailable")
	_, _, err = resolveUniqueWikiPage(
		context.Background(), service, slug, []string{"kb-1", "kb-2"}, NewWikiRouteResolver(),
	)
	if err == nil || !strings.Contains(err.Error(), "database unavailable") {
		t.Fatalf("non-not-found lookup errors must fail closed, got %v", err)
	}
}

func TestResolveUniqueWikiPageRejectsMismatchedServiceOwner(t *testing.T) {
	slug := "concept/target"
	service := &fakeWikiPageService{pages: map[string]*types.WikiPage{
		wikiPageKey("kb-1", slug): newTestWikiPage("kb-outside", slug),
	}}
	_, _, err := resolveUniqueWikiPage(
		context.Background(), service, slug, []string{"kb-1"}, NewWikiRouteResolver(),
	)
	if err == nil || !strings.Contains(err.Error(), "returned knowledge base kb-outside") {
		t.Fatalf("mismatched service owner must be rejected, got %v", err)
	}
}

func TestResolveWikiCreateKBRequiresUnambiguousServerContext(t *testing.T) {
	routes := NewWikiRouteResolver()
	if _, err := resolveWikiCreateKB("concept/new", []string{"kb-1", "kb-2"}, routes); err == nil {
		t.Fatal("new page creation across multiple unhinted KBs must be rejected")
	}
	routes.remember("concept/new", "kb-2")
	kbID, err := resolveWikiCreateKB("concept/new", []string{"kb-1", "kb-2"}, routes)
	if err != nil || kbID != "kb-2" {
		t.Fatalf("cached provenance should choose kb-2: kb=%s err=%v", kbID, err)
	}
}
