package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type renameCapableWikiService struct {
	*fakeWikiPageService
	requests []interfaces.WikiPageRenameRequest
	result   *interfaces.WikiPageRenameResult
	err      error
}

func (s *renameCapableWikiService) RenamePage(
	_ context.Context,
	req interfaces.WikiPageRenameRequest,
) (*interfaces.WikiPageRenameResult, error) {
	s.requests = append(s.requests, req)
	return s.result, s.err
}

func newRenameToolService() *renameCapableWikiService {
	page := newTestWikiPage("kb-a", "concept/old")
	page.ID, page.Version = "stable-page-uuid", 7
	renamed := *page
	renamed.Slug = "concept/new"
	return &renameCapableWikiService{
		fakeWikiPageService: &fakeWikiPageService{
			pages: map[string]*types.WikiPage{wikiPageKey("kb-a", page.Slug): page},
		},
		result: &interfaces.WikiPageRenameResult{Page: &renamed, AffectedPages: []*types.WikiPage{
			&renamed, {ID: "incoming-uuid", Slug: "concept/from"}, {ID: "outgoing-uuid", Slug: "concept/to"},
		}},
	}
}

func TestWikiRenameToolUsesOptionalAtomicCapability(t *testing.T) {
	svc := newRenameToolService()
	routes := NewWikiRouteResolver()
	routes.remember("concept/old", "kb-a")
	tool := NewWikiRenamePageTool(svc, []string{"kb-b", "kb-a"}, routes)
	result, err := tool.Execute(
		context.Background(),
		json.RawMessage(`{"slug":" Concept/Old ","new_slug":" Concept/New ","knowledge_base_id":"kb-b"}`),
	)
	require.NoError(t, err)
	require.True(t, result.Success, result.Error)
	require.Equal(
		t,
		[]interfaces.WikiPageRenameRequest{
			{KnowledgeBaseID: "kb-a", PageID: "stable-page-uuid", OldSlug: "concept/old", NewSlug: "concept/new"},
		},
		svc.requests,
	)
	data := result.Data
	require.Equal(t, "stable-page-uuid", data["page_id"])
	require.Equal(t, 2, data["updated_count"])
	require.Equal(t, []string{"concept/from", "concept/to"}, data["affected_pages"])
	require.Contains(t, result.Output, "preserving its ID and history")
	scopes := NewWikiScopesFromKBIDs([]string{"kb-a", "kb-b"})
	require.Empty(t, routes.scopesForSlug("concept/old", scopes))
	require.Equal(t, "kb-a", routes.scopesForSlug("concept/new", scopes)[0].KnowledgeBaseID)
	// CRUD/cross-link methods on this fake are nil interface methods: this
	// success path would panic if the tool still performed any fallback writes.
}

func TestWikiRenameToolFailsUnsupportedWithoutFallback(t *testing.T) {
	legacy := newRenameToolService().fakeWikiPageService
	result, err := NewWikiRenamePageTool(
		legacy,
		[]string{"kb-a"},
	).Execute(context.Background(), json.RawMessage(`{"slug":"concept/old","new_slug":"concept/new"}`))
	require.NoError(t, err)
	require.False(t, result.Success)
	require.Contains(t, result.Error, interfaces.ErrWikiRenameUnsupported.Error())
	require.Empty(t, legacy.getCalls)
}

func TestWikiRenameToolFailureKeepsOldRoute(t *testing.T) {
	for _, renameErr := range []error{
		repository.ErrWikiPageSlugConflict,
		repository.ErrWikiPageConflict,
		errors.New("transaction failed"),
	} {
		t.Run(renameErr.Error(), func(t *testing.T) {
			svc := newRenameToolService()
			svc.err = renameErr
			routes := NewWikiRouteResolver()
			result, err := NewWikiRenamePageTool(
				svc,
				[]string{"kb-a"},
				routes,
			).Execute(context.Background(), json.RawMessage(`{"slug":"concept/old","new_slug":"concept/new"}`))
			require.NoError(t, err)
			require.False(t, result.Success)
			require.Contains(t, result.Error, renameErr.Error())
			require.Len(t, svc.requests, 1)
			scopes := NewWikiScopesFromKBIDs([]string{"kb-a"})
			require.Len(t, routes.scopesForSlug("concept/old", scopes), 1)
			require.Empty(t, routes.scopesForSlug("concept/new", scopes))
		})
	}
}

func TestWikiRenameToolRejectsAmbiguityAndInvalidArguments(t *testing.T) {
	for _, scenario := range []string{
		"ambiguous",
		"backend-failure",
		"malformed-json",
		"invalid-slug",
		"same-slug",
		"empty-scope",
	} {
		t.Run(scenario, func(t *testing.T) {
			svc := newRenameToolService()
			args, kbIDs := `{"slug":"concept/old","new_slug":"concept/new"}`, []string{"kb-a", "kb-b"}
			switch scenario {
			case "ambiguous":
				svc.pages[wikiPageKey("kb-b", "concept/old")] = newTestWikiPage("kb-b", "concept/old")
			case "backend-failure":
				svc.getErrors = map[string]error{wikiPageKey("kb-b", "concept/old"): errors.New("database unavailable")}
			case "malformed-json":
				args = `{`
			case "invalid-slug":
				args = `{"slug":"concept/old","new_slug":"concept/new|oops"}`
			case "same-slug":
				args = `{"slug":"concept/old","new_slug":"Concept/Old"}`
			case "empty-scope":
				kbIDs = nil
			}
			result, err := NewWikiRenamePageTool(svc, kbIDs).Execute(context.Background(), json.RawMessage(args))
			require.NoError(t, err)
			require.False(t, result.Success)
			require.Empty(t, svc.requests)
		})
	}
}

func TestWikiRenameToolReportsCommittedSyncWarning(t *testing.T) {
	svc := newRenameToolService()
	svc.result.SyncWarnings = []string{"Rename committed; retrieval sync needs attention"}
	result, err := NewWikiRenamePageTool(
		svc,
		[]string{"kb-a"},
	).Execute(context.Background(), json.RawMessage(`{"slug":"concept/old","new_slug":"concept/new"}`))
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Contains(t, result.Output, "Rename committed; retrieval sync needs attention")
	require.Equal(t, svc.result.SyncWarnings, result.Data["sync_warnings"])
}
