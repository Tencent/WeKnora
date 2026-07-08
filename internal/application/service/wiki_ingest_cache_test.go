package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestRebuildWikiRetractsForCachedMap(t *testing.T) {
	ctx := context.Background()
	result := &docIngestResult{
		KnowledgeID: "kid-1",
		DocTitle:    "Doc One",
		Pages: []types.WikiLogPageRef{
			{Slug: "kept"},
			{Slug: "summary/kid-1"},
		},
	}
	updates := []SlugUpdate{
		{
			Slug:        "new",
			Type:        types.WikiPageTypeEntity,
			DocTitle:    "Doc One",
			KnowledgeID: "kid-1",
		},
	}
	oldPageSlugs := map[string]bool{
		"kept":          true,
		"stale":         true,
		"summary/kid-1": true,
	}
	batchCtx := &WikiBatchContext{
		SummaryContentByKnowledgeID: func(context.Context, string) string {
			return "previous summary contribution"
		},
	}

	got := rebuildWikiRetractsForCachedMap(ctx, result, updates, oldPageSlugs, batchCtx, "current content", "zh")

	if len(got) != 3 {
		t.Fatalf("expected 3 updates, got %d: %#v", len(got), got)
	}
	if got[0].Slug != "new" || got[0].Type != types.WikiPageTypeEntity {
		t.Fatalf("first update should preserve cached addition, got %#v", got[0])
	}

	bySlug := map[string]SlugUpdate{}
	for _, u := range got[1:] {
		bySlug[u.Slug] = u
	}
	if _, ok := bySlug["summary/kid-1"]; ok {
		t.Fatal("summary page must not get a retract update")
	}
	if bySlug["kept"].Type != "retract" {
		t.Fatalf("kept slug should retract prior contribution, got %#v", bySlug["kept"])
	}
	if bySlug["kept"].RetractDocContent != "previous summary contribution" {
		t.Fatalf("kept retract content = %q", bySlug["kept"].RetractDocContent)
	}
	if bySlug["stale"].Type != "retractStale" {
		t.Fatalf("stale slug should retract current content, got %#v", bySlug["stale"])
	}
	if bySlug["stale"].RetractDocContent != "current content" {
		t.Fatalf("stale retract content = %q", bySlug["stale"].RetractDocContent)
	}
	if bySlug["stale"].Language != "zh" {
		t.Fatalf("stale language = %q", bySlug["stale"].Language)
	}
}
