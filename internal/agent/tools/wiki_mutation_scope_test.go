package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
)

type mutationScopeWikiService struct {
	*fakeWikiPageService
	issues []*types.WikiPageIssue
}

func (s *mutationScopeWikiService) RepairContentLinks(_ context.Context, _, _, content string) (string, bool, error) {
	return content, false, nil
}

func (s *mutationScopeWikiService) UpdatePage(_ context.Context, page *types.WikiPage) (*types.WikiPage, error) {
	s.pages[wikiPageKey(page.KnowledgeBaseID, page.Slug)] = page
	return page, nil
}

func (s *mutationScopeWikiService) CreatePage(ctx context.Context, page *types.WikiPage) (*types.WikiPage, error) {
	return s.UpdatePage(ctx, page)
}

func (s *mutationScopeWikiService) UpdateAutoLinkedContent(ctx context.Context, page *types.WikiPage) error {
	_, err := s.UpdatePage(ctx, page)
	return err
}

func (s *mutationScopeWikiService) DeletePage(_ context.Context, kbID, slug string) error {
	delete(s.pages, wikiPageKey(kbID, slug))
	return nil
}

func (s *mutationScopeWikiService) CreateIssue(
	_ context.Context, issue *types.WikiPageIssue,
) (*types.WikiPageIssue, error) {
	s.issues = append(s.issues, issue)
	return issue, nil
}

func (s *mutationScopeWikiService) InjectCrossLinks(context.Context, string, []string) {}
func (s *mutationScopeWikiService) RebuildIndexPage(context.Context, string) error     { return nil }

func TestWikiMutationsKnowledgeBaseSelection(t *testing.T) {
	const slug = "entity/da-zong-shi"
	for _, toolName := range []string{
		ToolWikiWritePage, ToolWikiReplaceText, ToolWikiRenamePage, ToolWikiDeletePage, ToolWikiFlagIssue,
	} {
		for _, scenario := range []struct {
			name        string
			selector    string
			duplicate   bool
			wantError   string
			wantLookups int
		}{
			{name: "explicit target with duplicate slug", selector: "kb-2", duplicate: true, wantLookups: 1},
			{
				name: "omitted target rejects duplicate slug", duplicate: true,
				wantError: "specify knowledge_base_id", wantLookups: 2,
			},
			{
				name: "outside writable scope", selector: "kb-outside", duplicate: true,
				wantError: "not within the current writable wiki scope",
			},
			{name: "omitted target finds unique page", wantLookups: 2},
		} {
			t.Run(toolName+"/"+scenario.name, func(t *testing.T) {
				service := &mutationScopeWikiService{fakeWikiPageService: &fakeWikiPageService{
					pages: map[string]*types.WikiPage{},
				}}
				for _, kbID := range []string{"kb-1", "kb-2"} {
					page := newTestWikiPage(kbID, slug)
					page.Content = "old content"
					page.InLinks = types.StringArray{"concept/referrer"}
					if kbID == "kb-2" || scenario.duplicate {
						service.pages[wikiPageKey(kbID, slug)] = page
					}
					referrer := newTestWikiPage(kbID, "concept/referrer")
					referrer.Content = "see [[" + slug + "|chapter]]"
					service.pages[wikiPageKey(kbID, referrer.Slug)] = referrer
				}
				before, err := json.Marshal(service.pages)
				require.NoError(t, err)
				routes := NewWikiRouteResolver()
				// Stale/cross-KB provenance must not override the explicit target.
				routes.remember(slug, "kb-1")
				kbIDs := []string{"kb-1", "kb-2"}
				var tool types.Tool
				switch toolName {
				case ToolWikiWritePage:
					tool = NewWikiWritePageTool(service, kbIDs, nil, routes)
				case ToolWikiReplaceText:
					tool = NewWikiReplaceTextTool(service, kbIDs, nil, routes)
				case ToolWikiRenamePage:
					tool = NewWikiRenamePageTool(service, kbIDs, routes)
				case ToolWikiDeletePage:
					tool = NewWikiDeletePageTool(service, kbIDs, routes)
				case ToolWikiFlagIssue:
					tool = NewWikiFlagIssueTool(service, kbIDs, routes)
				}
				args, err := json.Marshal(map[string]any{
					"slug": slug, "knowledge_base_id": scenario.selector,
					"title": "Chapter", "summary": "Summary", "content": "new content", "page_type": "entity",
					"old_text": "old", "new_text": "new", "new_slug": "entity/renamed",
					"issue_type": "other", "description": "Needs review",
				})
				require.NoError(t, err)
				result, err := tool.Execute(context.Background(), args)
				require.NoError(t, err)
				require.NotNil(t, result)
				if scenario.wantError != "" {
					require.False(t, result.Success)
					require.Contains(t, result.Error, scenario.wantError)
					after, marshalErr := json.Marshal(service.pages)
					require.NoError(t, marshalErr)
					require.JSONEq(t, string(before), string(after))
					require.Empty(t, service.issues)
					require.Len(t, service.getCalls, scenario.wantLookups)
					return
				}
				require.True(t, result.Success, result.Error)
				if scenario.selector != "" {
					for _, call := range service.getCalls {
						require.Contains(t, call, "kb-2\x00")
					}
				}
				if scenario.duplicate {
					require.Equal(t, "old content", service.pages[wikiPageKey("kb-1", slug)].Content)
				}
				require.Equal(t, "see [["+slug+"|chapter]]",
					service.pages[wikiPageKey("kb-1", "concept/referrer")].Content)
				switch toolName {
				case ToolWikiWritePage, ToolWikiReplaceText:
					require.Equal(t, "new content", service.pages[wikiPageKey("kb-2", slug)].Content)
				case ToolWikiRenamePage:
					require.NotContains(t, service.pages, wikiPageKey("kb-2", slug))
					require.Contains(t, service.pages, wikiPageKey("kb-2", "entity/renamed"))
					require.Equal(t, "see [[entity/renamed|chapter]]",
						service.pages[wikiPageKey("kb-2", "concept/referrer")].Content)
				case ToolWikiDeletePage:
					require.NotContains(t, service.pages, wikiPageKey("kb-2", slug))
					require.Equal(t, "see chapter", service.pages[wikiPageKey("kb-2", "concept/referrer")].Content)
				case ToolWikiFlagIssue:
					require.Len(t, service.issues, 1)
					require.Equal(t, "kb-2", service.issues[0].KnowledgeBaseID)
				}
			})
		}
	}
}

func TestWikiWritePageCreatesInExplicitKnowledgeBase(t *testing.T) {
	const slug = "entity/da-zong-shi"
	service := &mutationScopeWikiService{fakeWikiPageService: &fakeWikiPageService{
		pages: map[string]*types.WikiPage{wikiPageKey("kb-1", slug): newTestWikiPage("kb-1", slug)},
	}}
	routes := NewWikiRouteResolver()
	routes.remember(slug, "kb-1")
	tool := NewWikiWritePageTool(service, []string{"kb-1", "kb-2"}, nil, routes)
	result, err := tool.Execute(context.Background(), json.RawMessage(`{
		"knowledge_base_id":"kb-2","slug":"entity/da-zong-shi",
		"title":"Chapter","summary":"Summary","content":"New page","page_type":"entity"
	}`))
	require.NoError(t, err)
	require.True(t, result.Success, result.Error)
	require.Equal(t, "New page", service.pages[wikiPageKey("kb-2", slug)].Content)
	require.Equal(t, "content for "+slug, service.pages[wikiPageKey("kb-1", slug)].Content)
	require.Equal(t, []string{wikiPageKey("kb-2", slug)}, service.getCalls)
}

func TestWikiMutationTargetDoesNotFallBackToAnotherKnowledgeBase(t *testing.T) {
	const slug = "entity/da-zong-shi"
	service := &fakeWikiPageService{pages: map[string]*types.WikiPage{
		wikiPageKey("kb-1", slug): newTestWikiPage("kb-1", slug),
	}}
	tool := NewWikiReplaceTextTool(service, []string{"kb-1", "kb-2"}, nil)
	result, err := tool.Execute(context.Background(), json.RawMessage(`{
		"knowledge_base_id":"kb-2","slug":"entity/da-zong-shi","old_text":"content","new_text":"new"
	}`))
	require.NoError(t, err)
	require.False(t, result.Success)
	require.Contains(t, result.Error, "wiki page not found in current scope")
	require.Equal(t, []string{wikiPageKey("kb-2", slug)}, service.getCalls)
}
