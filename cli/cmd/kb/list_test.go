package kb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	if err := runList(context.Background(), &ListOptions{Limit: 30}, &cmdutil.FormatOptions{Mode: cmdutil.FormatHuman}, &fakeListSvc{items: []sdk.KnowledgeBase{}}); err != nil {
		t.Fatalf("runList: %v", err)
	}
	if !strings.Contains(out.String(), "(no knowledge bases)") {
		t.Errorf("empty output expected '(no knowledge bases)', got %q", out.String())
	}
}

func TestList_Empty_JSON(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	fopts := &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}
	if err := runList(context.Background(), &ListOptions{Limit: 30}, fopts, &fakeListSvc{items: []sdk.KnowledgeBase{}}); err != nil {
		t.Fatalf("runList: %v", err)
	}
	var env struct {
		OK   bool                `json:"ok"`
		Data []sdk.KnowledgeBase `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("parse: %v\n%s", err, out.String())
	}
	if !env.OK {
		t.Error("envelope.ok must be true")
	}
	if len(env.Data) != 0 {
		t.Errorf("expected empty data, got %d items", len(env.Data))
	}
}

func TestList_NonEmpty_Human_RenderColumns(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	now := time.Now()
	items := []sdk.KnowledgeBase{
		{ID: "kb1", Name: "Marketing", KnowledgeCount: 5, UpdatedAt: now.Add(-3 * time.Hour)},
		{ID: "kb2", Name: "Engineering", KnowledgeCount: 1, UpdatedAt: now.Add(-2 * 24 * time.Hour)},
	}
	if err := runList(context.Background(), &ListOptions{Limit: 30}, &cmdutil.FormatOptions{Mode: cmdutil.FormatHuman}, &fakeListSvc{items: items}); err != nil {
		t.Fatalf("runList: %v", err)
	}
	got := out.String()
	for _, want := range []string{"ID", "NAME", "DOCS", "UPDATED", "kb1", "Marketing", "5 docs", "kb2", "Engineering", "1 doc"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q in:\n%s", want, got)
		}
	}
}

func TestList_JSON_JQProjection(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	now := time.Now()
	items := []sdk.KnowledgeBase{
		{ID: "kb1", Name: "Marketing", Description: "MKT desc", UpdatedAt: now},
	}
	// --jq projects from the envelope; .data[] | ... extracts from the array inside envelope.
	fopts := &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON, JQ: ".data[] | {id, name}"}
	if err := runList(context.Background(), &ListOptions{Limit: 30}, fopts, &fakeListSvc{items: items}); err != nil {
		t.Fatalf("runList: %v", err)
	}
	var item map[string]any
	if err := json.Unmarshal(out.Bytes(), &item); err != nil {
		t.Fatalf("parse: %v\n%s", err, out.String())
	}
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
	// .data | length counts the items inside the envelope's data array.
	fopts := &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON, JQ: ".data | length"}
	if err := runList(context.Background(), &ListOptions{Limit: 30}, fopts, &fakeListSvc{items: items}); err != nil {
		t.Fatalf("runList: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != "2" {
		t.Errorf("expected '2', got %q", got)
	}
}

func TestList_PinnedFilter(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	now := time.Now()
	items := []sdk.KnowledgeBase{
		{ID: "kb1", Name: "Marketing", IsPinned: true, UpdatedAt: now},
		{ID: "kb2", Name: "Engineering", IsPinned: false, UpdatedAt: now.Add(-time.Hour)},
		{ID: "kb3", Name: "Finance", IsPinned: true, UpdatedAt: now.Add(-2 * time.Hour)},
	}
	if err := runList(context.Background(), &ListOptions{Pinned: true, Limit: 30}, &cmdutil.FormatOptions{Mode: cmdutil.FormatHuman}, &fakeListSvc{items: items}); err != nil {
		t.Fatalf("runList: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "kb1") || !strings.Contains(got, "kb3") {
		t.Errorf("expected pinned KBs kb1 and kb3 in output, got:\n%s", got)
	}
	if strings.Contains(got, "kb2") {
		t.Errorf("unpinned kb2 should be filtered out, got:\n%s", got)
	}
}

func TestList_PinnedFilter_NoPinned_HumanMessage(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	items := []sdk.KnowledgeBase{
		{ID: "kb1", Name: "Marketing", IsPinned: false, UpdatedAt: time.Now()},
	}
	if err := runList(context.Background(), &ListOptions{Pinned: true, Limit: 30}, &cmdutil.FormatOptions{Mode: cmdutil.FormatHuman}, &fakeListSvc{items: items}); err != nil {
		t.Fatalf("runList: %v", err)
	}
	if !strings.Contains(out.String(), "(no pinned knowledge bases)") {
		t.Errorf("expected pinned-specific empty message, got: %q", out.String())
	}
}

// makeKBs returns N KBs with distinct IDs and descending UpdatedAt.
func makeKBs(n int) []sdk.KnowledgeBase {
	base := time.Now()
	out := make([]sdk.KnowledgeBase, n)
	for i := 0; i < n; i++ {
		out[i] = sdk.KnowledgeBase{
			ID:        fmt.Sprintf("kb_%02d", i),
			Name:      fmt.Sprintf("kb-%02d", i),
			UpdatedAt: base.Add(-time.Duration(i) * time.Hour),
		}
	}
	return out
}

func TestList_Limit_CapsResults(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeListSvc{items: makeKBs(20)}
	fopts := &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}
	if err := runList(context.Background(), &ListOptions{Limit: 5}, fopts, svc); err != nil {
		t.Fatalf("runList: %v", err)
	}
	got := strings.Count(out.String(), `"id":"kb_`)
	if got != 5 {
		t.Errorf("--limit 5 should slice 20 items to 5; got %d in:\n%s", got, out.String())
	}
}

func TestList_Limit_Zero_Rejected(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeListSvc{items: makeKBs(7)}
	fopts := &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}
	err := runList(context.Background(), &ListOptions{Limit: 0}, fopts, svc)
	if err == nil {
		t.Fatal("expected error for --limit 0")
	}
	var typed *cmdutil.Error
	if !errors.As(err, &typed) {
		t.Fatalf("expected *cmdutil.Error, got %T: %v", err, err)
	}
	if typed.Code != cmdutil.CodeInputInvalidArgument {
		t.Errorf("expected CodeInputInvalidArgument, got %v", typed.Code)
	}
}

func TestList_Limit_Negative_Rejected(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	err := runList(context.Background(), &ListOptions{Limit: -1}, &cmdutil.FormatOptions{Mode: cmdutil.FormatHuman}, &fakeListSvc{items: makeKBs(3)})
	if err == nil {
		t.Fatal("expected error for negative --limit")
	}
	var typed *cmdutil.Error
	if !errors.As(err, &typed) {
		t.Fatalf("expected *cmdutil.Error, got %T: %v", err, err)
	}
	if typed.Code != cmdutil.CodeInputInvalidArgument {
		t.Errorf("expected CodeInputInvalidArgument, got %v", typed.Code)
	}
}
