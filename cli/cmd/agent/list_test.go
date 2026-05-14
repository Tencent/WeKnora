package agentcmd

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
	items []sdk.Agent
	err   error
}

func (f *fakeListSvc) ListAgents(_ context.Context) ([]sdk.Agent, error) {
	return f.items, f.err
}

func TestList_Empty_Human(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	if err := runList(context.Background(), nil, &fakeListSvc{}); err != nil {
		t.Fatalf("runList: %v", err)
	}
	if !strings.Contains(out.String(), "(no agents)") {
		t.Errorf("expected '(no agents)', got %q", out.String())
	}
}

func TestList_Empty_JSON(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	if err := runList(context.Background(), &cmdutil.JSONOptions{}, &fakeListSvc{}); err != nil {
		t.Fatalf("runList: %v", err)
	}
	if !strings.Contains(out.String(), `"items":[]`) {
		t.Errorf("expected items:[], got %q", out.String())
	}
}

func TestList_NonEmpty_Human_RendersColumns(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	now := time.Now()
	items := []sdk.Agent{
		{ID: "ag_a", Name: "Research", IsBuiltin: true, UpdatedAt: now.Add(-1 * time.Hour)},
		{ID: "ag_b", Name: "Triage", UpdatedAt: now.Add(-3 * 24 * time.Hour)},
	}
	if err := runList(context.Background(), nil, &fakeListSvc{items: items}); err != nil {
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
	if err := runList(context.Background(), &cmdutil.JSONOptions{}, &fakeListSvc{items: items}); err != nil {
		t.Fatalf("runList: %v", err)
	}
	var env struct {
		Data struct {
			Items []sdk.Agent `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(env.Data.Items) != 3 {
		t.Fatalf("len = %d, want 3", len(env.Data.Items))
	}
	wantOrder := []string{"ag_new", "ag_mid", "ag_old"}
	for i, w := range wantOrder {
		if env.Data.Items[i].ID != w {
			t.Errorf("position %d: got %s, want %s (updated_at desc)", i, env.Data.Items[i].ID, w)
		}
	}
}

func TestList_JSON_FieldFilter(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	items := []sdk.Agent{
		{ID: "ag_x", Name: "Foo", Description: "long description"},
	}
	jopts := &cmdutil.JSONOptions{Fields: []string{"id", "name"}}
	if err := runList(context.Background(), jopts, &fakeListSvc{items: items}); err != nil {
		t.Fatalf("runList: %v", err)
	}
	var env struct {
		Data struct {
			Items []map[string]any `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, has := env.Data.Items[0]["description"]; has {
		t.Errorf("description should be filtered out: %+v", env.Data.Items[0])
	}
}
