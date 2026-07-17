package tools

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type stubWikiNavigateService struct {
	interfaces.WikiPageService
	pages    []*types.WikiPage
	requests []struct {
		kbID  string
		query string
		limit int
	}
}

func (s *stubWikiNavigateService) GetIndexView(ctx context.Context, kbID string, pageTypes []string, limit int, cursor string) (*types.WikiIndexResponse, error) {
	typeAllowed := make(map[string]bool, len(pageTypes))
	for _, pageType := range pageTypes {
		typeAllowed[pageType] = true
	}
	grouped := make(map[string][]types.WikiIndexEntry)
	for _, page := range s.pages {
		if page.KnowledgeBaseID != kbID {
			continue
		}
		if len(typeAllowed) > 0 && !typeAllowed[page.PageType] {
			continue
		}
		grouped[page.PageType] = append(grouped[page.PageType], types.WikiIndexEntry{
			Slug:    page.Slug,
			Title:   page.Title,
			Summary: page.Summary,
		})
	}
	groups := make([]types.WikiIndexGroup, 0, len(grouped))
	for pageType, items := range grouped {
		groups = append(groups, types.WikiIndexGroup{
			Type:  pageType,
			Total: int64(len(items)),
			Items: items,
		})
	}
	return &types.WikiIndexResponse{Groups: groups}, nil
}

func (s *stubWikiNavigateService) SearchPages(ctx context.Context, kbID string, query string, limit int) ([]*types.WikiPage, error) {
	s.requests = append(s.requests, struct {
		kbID  string
		query string
		limit int
	}{kbID: kbID, query: query, limit: limit})

	query = strings.Trim(strings.ToLower(query), `\Q\E`)
	query = strings.ReplaceAll(query, `\+`, "+")
	query = strings.ReplaceAll(query, `\|`, "|")
	alternatives := strings.Split(query, "|")
	var results []*types.WikiPage
	for _, page := range s.pages {
		if page.KnowledgeBaseID != kbID {
			continue
		}
		haystack := strings.ToLower(strings.Join([]string{
			page.Slug,
			page.Title,
			page.Summary,
			page.Content,
			strings.Join(page.Aliases, " "),
		}, " "))
		for _, alt := range alternatives {
			alt = strings.TrimSpace(alt)
			if alt == "" || strings.Contains(haystack, alt) {
				results = append(results, page)
				break
			}
		}
	}
	return results, nil
}

func (s *stubWikiNavigateService) GetPageBySlug(ctx context.Context, kbID string, slug string) (*types.WikiPage, error) {
	for _, page := range s.pages {
		if page.KnowledgeBaseID == kbID && page.Slug == slug {
			return page, nil
		}
	}
	return nil, nil
}

func TestWikiNavigateAcceptsKBLocalCustomPageType(t *testing.T) {
	service := &stubWikiNavigateService{pages: []*types.WikiPage{
		{
			KnowledgeBaseID: "kb-1",
			Slug:            "decision/cache-strategy",
			Title:           "Cache Strategy",
			PageType:        "decision-record",
			Summary:         "Architecture decision for cache invalidation.",
		},
	}}
	tool := NewWikiNavigateTool(service, nil, []WikiScope{{KnowledgeBaseID: "kb-1"}})

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"Cache","page_types":["decision-record"],"knowledge_base_id":"kb-1"}`))
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute failed: %s", result.Error)
	}
	output := result.Output
	if !strings.Contains(output, `<category type="decision-record" total="1" shown="1">`) {
		t.Fatalf("expected custom KB-local page type in overview, got: %s", output)
	}
	if !strings.Contains(output, `[[decision/cache-strategy|Cache Strategy]]`) {
		t.Fatalf("expected custom type page link, got: %s", output)
	}
}

func TestWikiNavigateQueryModeRegexPreservesPatternAndDefaultEscapes(t *testing.T) {
	service := &stubWikiNavigateService{}
	tool := NewWikiNavigateTool(service, nil, []WikiScope{{KnowledgeBaseID: "kb-1"}})

	if result, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"TB10|TB30","knowledge_base_id":"kb-1"}`)); err != nil || !result.Success {
		t.Fatalf("default Execute failed: result=%+v err=%v", result, err)
	}
	if len(service.requests) != 1 {
		t.Fatalf("expected one default search request, got %d", len(service.requests))
	}
	if got, want := service.requests[0].query, `TB10\|TB30`; got != want {
		t.Fatalf("default query mode should escape regex metacharacters, got %q want %q", got, want)
	}

	service.requests = nil
	if result, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"TB10|TB30","query_mode":"regex","knowledge_base_id":"kb-1"}`)); err != nil || !result.Success {
		t.Fatalf("regex Execute failed: result=%+v err=%v", result, err)
	}
	if len(service.requests) != 1 {
		t.Fatalf("expected one regex search request, got %d", len(service.requests))
	}
	if got, want := service.requests[0].query, `TB10|TB30`; got != want {
		t.Fatalf("regex query mode should preserve pattern, got %q want %q", got, want)
	}
}

func TestWikiNavigateRanksTitleAliasAndReturnsNextHops(t *testing.T) {
	service := &stubWikiNavigateService{pages: []*types.WikiPage{
		{
			KnowledgeBaseID: "kb-1",
			Slug:            "concept/ghosting",
			Title:           "Ghosting",
			PageType:        types.WikiPageTypeConcept,
			Summary:         "Display afterimage phenomenon.",
			Aliases:         types.StringArray{"afterimage"},
			Content:         "Ghosting can relate to refresh and driving.",
			OutLinks:        types.StringArray{"concept/refresh-rate"},
		},
		{
			KnowledgeBaseID: "kb-1",
			Slug:            "concept/refresh-rate",
			Title:           "Refresh Rate",
			PageType:        types.WikiPageTypeConcept,
			Summary:         "Refresh rate affects display afterimage.",
			InLinks:         types.StringArray{"concept/ghosting"},
		},
		{
			KnowledgeBaseID: "kb-1",
			Slug:            "summary/random",
			Title:           "Random Document",
			PageType:        types.WikiPageTypeSummary,
			Summary:         "Mentions ghosting in passing.",
			Content:         "A passing note about ghosting.",
		},
	}}
	tool := NewWikiNavigateTool(service, nil, []WikiScope{{KnowledgeBaseID: "kb-1"}})

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"Ghosting","limit":2}`))
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute failed: %s", result.Error)
	}
	output := result.Output
	if !strings.Contains(output, `[[concept/ghosting|Ghosting]]`) {
		t.Fatalf("expected title match candidate, got: %s", output)
	}
	if !strings.Contains(output, "<next_hops>") || !strings.Contains(output, `[[concept/refresh-rate|Refresh Rate]]`) {
		t.Fatalf("expected linked next hop, got: %s", output)
	}
	recommendedStart := strings.Index(output, "<recommended_reads>")
	if recommendedStart < 0 {
		t.Fatalf("expected recommended reads, got: %s", output)
	}
	recommended := output[recommendedStart:]
	if strings.Index(recommended, "concept/ghosting") > strings.Index(recommended, "summary/random") {
		t.Fatalf("title match should rank before content-only hit in recommendations, got: %s", output)
	}
}

func TestWikiNavigateIndexOverviewShowsAllCategoriesBeforeLargeConceptList(t *testing.T) {
	pages := make([]*types.WikiPage, 0, 144)
	for i := 0; i < 141; i++ {
		pages = append(pages, &types.WikiPage{
			KnowledgeBaseID: "kb-1",
			Slug:            "concept/item-" + strconv.Itoa(i),
			Title:           "Concept " + strconv.Itoa(i),
			PageType:        types.WikiPageTypeConcept,
			Summary:         "Concept summary.",
		})
	}
	pages = append(pages,
		&types.WikiPage{
			KnowledgeBaseID: "kb-1",
			Slug:            "entity/tb10",
			Title:           "TB10",
			PageType:        types.WikiPageTypeEntity,
			Summary:         "TB10 entity page.",
		},
		&types.WikiPage{
			KnowledgeBaseID: "kb-1",
			Slug:            "summary/manual",
			Title:           "Manual",
			PageType:        types.WikiPageTypeSummary,
			Summary:         "Source manual.",
		},
	)
	service := &stubWikiNavigateService{pages: pages}
	tool := NewWikiNavigateTool(service, nil, []WikiScope{{KnowledgeBaseID: "kb-1"}})

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"TB10","knowledge_base_id":"kb-1"}`))
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	output := result.Output
	first16K := output
	if len(first16K) > 16000 {
		first16K = first16K[:16000]
	}
	for _, want := range []string{
		`<category type="concept" total="141" shown="6" />`,
		`<category type="entity" total="1" shown="1" />`,
		`<category type="summary" total="1" shown="1" />`,
	} {
		if !strings.Contains(first16K, want) {
			t.Fatalf("expected %q before a 16KB history truncation, got: %s", want, first16K)
		}
	}
	if strings.Count(output, `[[concept/item-`) > wikiNavigateIndexDefault {
		t.Fatalf("concept index links should be capped before recommendations, got: %s", output)
	}
}
