package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// limitingWikiService returns at most limit pages, like the real store.
type limitingWikiService struct {
	*fakeWikiPageService
	limits []int
}

func (s *limitingWikiService) SearchPages(
	ctx context.Context, kbID, query string, limit int,
) ([]*types.WikiPage, error) {
	s.limits = append(s.limits, limit)
	pages, err := s.fakeWikiPageService.SearchPages(ctx, kbID, query, limit)
	if len(pages) > limit {
		pages = pages[:limit]
	}
	return pages, err
}

// Under a document scope, out-of-scope pages ranked first used to fill the
// limit and hide the in-scope page ranked just below them.
func TestWikiSearchScopedOverfetchesBeforeFiltering(t *testing.T) {
	var pages []*types.WikiPage
	for i := 0; i < 3; i++ {
		pages = append(pages, &types.WikiPage{
			KnowledgeBaseID: "kb-1", Slug: fmt.Sprintf("other-%d", i), Title: "Other", Content: "engine",
			PageType: "entity", SourceRefs: types.StringArray{"doc-other|Other"},
		})
	}
	pages = append(pages, &types.WikiPage{
		KnowledgeBaseID: "kb-1", Slug: "wanted", Title: "Wanted", Content: "engine",
		PageType: "entity", SourceRefs: types.StringArray{"doc-wanted|Wanted"},
	})
	service := &limitingWikiService{fakeWikiPageService: &fakeWikiPageService{
		searchResults: map[string][]*types.WikiPage{"kb-1": pages},
	}}
	tool := NewWikiSearchTool(service, nil,
		[]WikiScope{{KnowledgeBaseID: "kb-1", KnowledgeIDs: []string{"doc-wanted"}}}, NewWikiRouteResolver())

	args, _ := json.Marshal(map[string]any{"query": "engine", "limit": 2})
	res, err := tool.Execute(context.Background(), args)
	if err != nil || res == nil || !res.Success {
		t.Fatalf("Execute: res=%+v err=%v", res, err)
	}
	if !strings.Contains(res.Output, "wanted") {
		t.Fatalf("in-scope page below the limit was not found: %s", res.Output)
	}
	if len(service.limits) == 0 || service.limits[0] <= 2 {
		t.Fatalf("scoped search did not over-fetch: limits=%v", service.limits)
	}
}
