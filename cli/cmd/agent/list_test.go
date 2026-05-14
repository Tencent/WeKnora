package agentcmd

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
	items []sdk.Agent
	err   error
}

func (f *fakeListSvc) ListAgents(_ context.Context) ([]sdk.Agent, error) {
	return f.items, f.err
}

func TestList_Empty_Human(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	if err := runList(context.Background(), &ListOptions{}, nil, &fakeListSvc{}); err != nil {
		t.Fatalf("runList: %v", err)
	}
	if !strings.Contains(out.String(), "(no agents)") {
		t.Errorf("expected '(no agents)', got %q", out.String())
	}
}

func TestList_Empty_JSON(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	if err := runList(context.Background(), &ListOptions{}, &cmdutil.JSONOptions{}, &fakeListSvc{}); err != nil {
		t.Fatalf("runList: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != "[]" {
		t.Errorf("expected bare `[]`, got %q", got)
	}
}

func TestList_NonEmpty_Human_RendersColumns(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	now := time.Now()
	items := []sdk.Agent{
		{ID: "ag_a", Name: "Research", IsBuiltin: true, UpdatedAt: now.Add(-1 * time.Hour)},
		{ID: "ag_b", Name: "Triage", UpdatedAt: now.Add(-3 * 24 * time.Hour)},
	}
	if err := runList(context.Background(), &ListOptions{}, nil, &fakeListSvc{items: items}); err != nil {
		t.Fatalf("runList: %v", err)
	}
	got := out.String()
	for _, w := range []string{"ID", "NAME", "BUILTIN", "ag_a", "Research", "yes", "ag_b", "Triage"} {
		if !strings.Contains(got, w) {
			t.Errorf("output missing %q in:\n%s", w, got)
		}
	}
}

func TestList_NonEmpty_JSON_SortsByUpdatedAtDesc(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	now := time.Now()
	items := []sdk.Agent{
		{ID: "ag_old", Name: "old", UpdatedAt: now.Add(-7 * 24 * time.Hour)},
		{ID: "ag_new", Name: "new", UpdatedAt: now},
		{ID: "ag_mid", Name: "mid", UpdatedAt: now.Add(-1 * time.Hour)},
	}
	if err := runList(context.Background(), &ListOptions{}, &cmdutil.JSONOptions{}, &fakeListSvc{items: items}); err != nil {
		t.Fatalf("runList: %v", err)
	}
	var got []sdk.Agent
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	wantOrder := []string{"ag_new", "ag_mid", "ag_old"}
	for i, w := range wantOrder {
		if got[i].ID != w {
			t.Errorf("position %d: got %s, want %s (updated_at desc)", i, got[i].ID, w)
		}
	}
}

func TestList_JSON_FieldFilter(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	items := []sdk.Agent{
		{ID: "ag_x", Name: "Foo", Description: "long description"},
	}
	jopts := &cmdutil.JSONOptions{Fields: []string{"id", "name"}}
	if err := runList(context.Background(), &ListOptions{}, jopts, &fakeListSvc{items: items}); err != nil {
		t.Fatalf("runList: %v", err)
	}
	var got []map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, has := got[0]["description"]; has {
		t.Errorf("description should be filtered out: %+v", got[0])
	}
}

// makeAgents returns N Agents with distinct IDs and descending UpdatedAt.
func makeAgents(n int) []sdk.Agent {
	base := time.Now()
	out := make([]sdk.Agent, n)
	for i := 0; i < n; i++ {
		out[i] = sdk.Agent{
			ID:        fmt.Sprintf("ag_%02d", i),
			Name:      fmt.Sprintf("name-%02d", i),
			UpdatedAt: base.Add(-time.Duration(i) * time.Hour),
		}
	}
	return out
}

func TestList_Limit_CapsResults(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	if err := runList(context.Background(), &ListOptions{Limit: 5}, &cmdutil.JSONOptions{}, &fakeListSvc{items: makeAgents(20)}); err != nil {
		t.Fatalf("runList: %v", err)
	}
	got := strings.Count(out.String(), `"id":"ag_`)
	if got != 5 {
		t.Errorf("--limit 5 must slice 20 down to 5; got %d", got)
	}
}

func TestList_Limit_Zero_NoCap(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	if err := runList(context.Background(), &ListOptions{Limit: 0}, &cmdutil.JSONOptions{}, &fakeListSvc{items: makeAgents(7)}); err != nil {
		t.Fatalf("runList: %v", err)
	}
	got := strings.Count(out.String(), `"id":"ag_`)
	if got != 7 {
		t.Errorf("--limit 0 must not cap; got %d, want 7", got)
	}
}

func TestList_Limit_Negative_Rejected(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	err := runList(context.Background(), &ListOptions{Limit: -1}, nil, &fakeListSvc{items: makeAgents(2)})
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
