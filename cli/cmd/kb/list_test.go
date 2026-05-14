package kb

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
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
	if err := runList(context.Background(), nil, &fakeListSvc{items: []sdk.KnowledgeBase{}}); err != nil {
		t.Fatalf("runList: %v", err)
	}
	if !strings.Contains(out.String(), "(no knowledge bases)") {
		t.Errorf("empty output expected '(no knowledge bases)', got %q", out.String())
	}
}

func TestList_Empty_JSON(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	jopts := &cmdutil.JSONOptions{}
	if err := runList(context.Background(), jopts, &fakeListSvc{items: []sdk.KnowledgeBase{}}); err != nil {
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
	if err := runList(context.Background(), nil, &fakeListSvc{items: items}); err != nil {
		t.Fatalf("runList: %v", err)
	}
	got := out.String()
	for _, want := range []string{"ID", "NAME", "DOCS", "UPDATED", "kb1", "Marketing", "5 docs", "kb2", "Engineering", "1 doc"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q in:\n%s", want, got)
		}
	}
}

func TestList_JSON_FieldFilter(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	now := time.Now()
	items := []sdk.KnowledgeBase{
		{ID: "kb1", Name: "Marketing", Description: "MKT desc", UpdatedAt: now},
	}
	jopts := &cmdutil.JSONOptions{Fields: []string{"id", "name"}}
	if err := runList(context.Background(), jopts, &fakeListSvc{items: items}); err != nil {
		t.Fatalf("runList: %v", err)
	}
	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			Items []map[string]any `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("parse: %v\n%s", err, out.String())
	}
	if len(env.Data.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(env.Data.Items))
	}
	item := env.Data.Items[0]
	if item["id"] != "kb1" || item["name"] != "Marketing" {
		t.Errorf("kept fields wrong: %+v", item)
	}
	if _, has := item["description"]; has {
		t.Errorf("description should be dropped, got: %+v", item)
	}
}

func TestList_JSON_JQ(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	now := time.Now()
	items := []sdk.KnowledgeBase{
		{ID: "kb1", Name: "Marketing", UpdatedAt: now},
		{ID: "kb2", Name: "Engineering", UpdatedAt: now.Add(-time.Hour)},
	}
	jopts := &cmdutil.JSONOptions{JQ: ".data.items | length"}
	if err := runList(context.Background(), jopts, &fakeListSvc{items: items}); err != nil {
		t.Fatalf("runList: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != "2" {
		t.Errorf("expected '2', got %q", got)
	}
}
