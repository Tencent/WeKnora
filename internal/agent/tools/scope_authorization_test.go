package tools

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type scopeKnowledgeService struct {
	interfaces.KnowledgeService
	knowledge *types.Knowledge
}

func (s *scopeKnowledgeService) GetKnowledgeByIDOnly(context.Context, string) (*types.Knowledge, error) {
	return s.knowledge, nil
}

func testKnowledgeTag(id string) *types.KnowledgeTag {
	return &types.KnowledgeTag{ID: id}
}

func TestKnowledgeIDsMatchingAnyTag(t *testing.T) {
	matches, err := knowledgeIDsMatchingAnyTag(
		context.Background(),
		[]string{"doc-1", "doc-2"},
		[]string{"tag-a"},
		func(_ context.Context, ids []string) (map[string][]*types.KnowledgeTag, error) {
			return map[string][]*types.KnowledgeTag{
				"doc-1": []*types.KnowledgeTag{testKnowledgeTag("tag-z")},
				"doc-2": []*types.KnowledgeTag{testKnowledgeTag("tag-a")},
			}, nil
		},
	)
	if err != nil {
		t.Fatalf("knowledgeIDsMatchingAnyTag() error = %v", err)
	}
	if matches["doc-1"] {
		t.Fatalf("doc-1 should not match tag-a")
	}
	if !matches["doc-2"] {
		t.Fatalf("doc-2 should match tag-a")
	}
}

func TestAuthorizeKnowledgeInSearchTargetsRejectsSiblingKB(t *testing.T) {
	service := &scopeKnowledgeService{knowledge: &types.Knowledge{ID: "doc-2", KnowledgeBaseID: "kb-2"}}
	targets := types.SearchTargets{{
		Type:            types.SearchTargetTypeKnowledgeBase,
		KnowledgeBaseID: "kb-1",
	}}
	if _, err := authorizeKnowledgeInSearchTargets(context.Background(), targets, "doc-2", service); err == nil {
		t.Fatal("document from a sibling KB must be rejected")
	}
}

func TestAuthorizeKnowledgeInSearchTargetsAllowsBoundDocument(t *testing.T) {
	service := &scopeKnowledgeService{knowledge: &types.Knowledge{ID: "doc-1", KnowledgeBaseID: "kb-1"}}
	targets := types.SearchTargets{{
		Type:            types.SearchTargetTypeKnowledge,
		KnowledgeBaseID: "kb-1",
		KnowledgeIDs:    []string{"doc-1"},
	}}
	got, err := authorizeKnowledgeInSearchTargets(context.Background(), targets, "doc-1", service)
	if err != nil || got == nil || got.ID != "doc-1" {
		t.Fatalf("bound document authorization failed: got=%+v err=%v", got, err)
	}
}

func TestValidateKnowledgeBaseIDsInSearchTargetsRejectsMixedScope(t *testing.T) {
	targets := types.SearchTargets{{
		Type:            types.SearchTargetTypeKnowledgeBase,
		KnowledgeBaseID: "kb-1",
	}}
	if err := validateKnowledgeBaseIDsInSearchTargets(targets, []string{"kb-1", "kb-2"}); err == nil {
		t.Fatal("mixed valid and out-of-scope KB IDs must be rejected")
	}
}

func TestPagePassesWikiScope_TagScope(t *testing.T) {
	page := &types.WikiPage{
		Slug:       "entity/acme",
		SourceRefs: []string{"doc-1|Acme intro", "doc-2"},
	}

	passes, err := pagePassesWikiScope(
		context.Background(),
		page,
		WikiScope{KnowledgeBaseID: "kb-1", TagIDs: []string{"tag-a"}},
		func(_ context.Context, ids []string) (map[string][]*types.KnowledgeTag, error) {
			return map[string][]*types.KnowledgeTag{
				"doc-1": []*types.KnowledgeTag{testKnowledgeTag("tag-z")},
				"doc-2": []*types.KnowledgeTag{testKnowledgeTag("tag-a")},
			}, nil
		},
	)
	if err != nil {
		t.Fatalf("pagePassesWikiScope() error = %v", err)
	}
	if !passes {
		t.Fatalf("page should pass when one source document has the mentioned tag")
	}

	passes, err = pagePassesWikiScope(
		context.Background(),
		page,
		WikiScope{KnowledgeBaseID: "kb-1", TagIDs: []string{"tag-missing"}},
		func(_ context.Context, ids []string) (map[string][]*types.KnowledgeTag, error) {
			return map[string][]*types.KnowledgeTag{
				"doc-1": []*types.KnowledgeTag{testKnowledgeTag("tag-z")},
				"doc-2": []*types.KnowledgeTag{testKnowledgeTag("tag-a")},
			}, nil
		},
	)
	if err != nil {
		t.Fatalf("pagePassesWikiScope() error = %v", err)
	}
	if passes {
		t.Fatalf("page should be filtered when no source document has the mentioned tag")
	}
}

func TestNewWikiScopesFromSearchTargetsMergesAlternativesPerKB(t *testing.T) {
	targets := types.SearchTargets{
		{Type: types.SearchTargetTypeKnowledge, KnowledgeBaseID: "kb-1", KnowledgeIDs: []string{"doc-1"}},
		{Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "kb-1", TagIDs: []string{"tag-1"}},
		{Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "kb-2"},
		{Type: types.SearchTargetTypeKnowledge, KnowledgeBaseID: "kb-outside", KnowledgeIDs: []string{"doc-x"}},
	}
	scopes := NewWikiScopesFromSearchTargets(targets, []string{"kb-1", "kb-2"})
	if len(scopes) != 2 {
		t.Fatalf("scope count = %d, want 2: %+v", len(scopes), scopes)
	}
	if len(scopes[0].KnowledgeIDs) != 1 || scopes[0].KnowledgeIDs[0] != "doc-1" ||
		len(scopes[0].TagIDs) != 1 || scopes[0].TagIDs[0] != "tag-1" {
		t.Fatalf("kb-1 alternatives were not merged: %+v", scopes[0])
	}
	if len(scopes[1].KnowledgeIDs) != 0 || len(scopes[1].TagIDs) != 0 {
		t.Fatalf("whole-KB target must remain unrestricted: %+v", scopes[1])
	}
}

func TestPagePassesWikiScopeUsesDocumentOrTagUnion(t *testing.T) {
	page := &types.WikiPage{SourceRefs: []string{"doc-by-tag"}}
	passes, err := pagePassesWikiScope(
		context.Background(),
		page,
		WikiScope{
			KnowledgeBaseID: "kb-1",
			KnowledgeIDs:    []string{"different-explicit-doc"},
			TagIDs:          []string{"tag-1"},
		},
		func(context.Context, []string) (map[string][]*types.KnowledgeTag, error) {
			return map[string][]*types.KnowledgeTag{
				"doc-by-tag": {testKnowledgeTag("tag-1")},
			}, nil
		},
	)
	if err != nil || !passes {
		t.Fatalf("tag-authorized page should pass the union scope: passes=%v err=%v", passes, err)
	}
}

func TestPagePassesWikiScopeFailsClosedWithoutProvenance(t *testing.T) {
	scope := WikiScope{KnowledgeBaseID: "kb-1", KnowledgeIDs: []string{"doc-1"}}
	for _, page := range []*types.WikiPage{
		{Slug: "concept/uncited", PageType: types.WikiPageTypeConcept},
		{Slug: "index", PageType: types.WikiPageTypeIndex},
	} {
		passes, err := pagePassesWikiScope(context.Background(), page, scope, nil)
		if err != nil {
			t.Fatalf("pagePassesWikiScope() error = %v", err)
		}
		if passes {
			t.Fatalf("restricted Wiki scope must reject page without attributable provenance: %+v", page)
		}
	}
}

func TestNewWikiScopesUsesLogicalTagsAndSkipsEmptyDocumentTargets(t *testing.T) {
	targets := types.SearchTargets{
		{Type: types.SearchTargetTypeKnowledge, KnowledgeBaseID: "kb-tag", ScopeTagIDs: []string{"tag-logical"}},
		{Type: types.SearchTargetTypeKnowledge, KnowledgeBaseID: "kb-empty"},
	}
	scopes := NewWikiScopesFromSearchTargets(targets, []string{"kb-tag", "kb-empty"})
	if len(scopes) != 1 || scopes[0].KnowledgeBaseID != "kb-tag" ||
		len(scopes[0].TagIDs) != 1 || scopes[0].TagIDs[0] != "tag-logical" {
		t.Fatalf("unexpected Wiki scopes: %+v", scopes)
	}
}

func TestFilterGraphResultsHonorsDocumentScope(t *testing.T) {
	targets := types.SearchTargets{{
		Type: types.SearchTargetTypeKnowledge, KnowledgeBaseID: "kb-1", KnowledgeIDs: []string{"doc-allowed"},
	}}
	results := []*types.SearchResult{
		{ID: "chunk-1", KnowledgeID: "doc-allowed", KnowledgeBaseID: "kb-1"},
		{ID: "chunk-2", KnowledgeID: "doc-outside", KnowledgeBaseID: "kb-1"},
	}
	filtered, err := filterSearchResultsInSearchTargets(context.Background(), targets, "kb-1", results, nil)
	if err != nil {
		t.Fatalf("filterSearchResultsInSearchTargets() error = %v", err)
	}
	if len(filtered) != 1 || filtered[0].KnowledgeID != "doc-allowed" {
		t.Fatalf("graph result scope leaked: %+v", filtered)
	}
}
