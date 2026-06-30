package tools

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

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
