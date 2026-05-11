package kb

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

type fakeListSvc struct {
	items []sdk.KnowledgeBase
	err   error
}

func (f *fakeListSvc) ListKnowledgeBases(ctx context.Context) ([]sdk.KnowledgeBase, error) {
	return f.items, f.err
}

func TestList_Empty_Human(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	if err := runList(context.Background(), &ListOptions{}, &fakeListSvc{items: []sdk.KnowledgeBase{}}); err != nil {
		t.Fatalf("runList: %v", err)
	}
	if !strings.Contains(out.String(), "(no knowledge bases)") {
		t.Errorf("empty output expected '(no knowledge bases)', got %q", out.String())
	}
}

func TestList_Empty_JSON(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	if err := runList(context.Background(), &ListOptions{JSONOut: true}, &fakeListSvc{items: []sdk.KnowledgeBase{}}); err != nil {
		t.Fatalf("runList: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, `"items":[]`) {
		t.Errorf("empty JSON should contain items:[], got %q", got)
	}
	if strings.Contains(got, `"items":null`) {
		t.Error("items must be [] not null")
	}
}

func TestList_NonEmpty_Human_RenderColumns(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	now := time.Now()
	items := []sdk.KnowledgeBase{
		{ID: "kb1", Name: "Marketing", KnowledgeCount: 5, UpdatedAt: now.Add(-3 * time.Hour)},
		{ID: "kb2", Name: "Engineering", KnowledgeCount: 1, UpdatedAt: now.Add(-2 * 24 * time.Hour)},
	}
	if err := runList(context.Background(), &ListOptions{}, &fakeListSvc{items: items}); err != nil {
		t.Fatalf("runList: %v", err)
	}
	got := out.String()
	for _, want := range []string{"ID", "NAME", "DOCS", "UPDATED", "kb1", "Marketing", "5 docs", "kb2", "Engineering", "1 doc"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q in:\n%s", want, got)
		}
	}
}
