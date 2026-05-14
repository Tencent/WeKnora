package agentcmd

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

type fakeViewSvc struct {
	resp *sdk.Agent
	err  error
}

func (f *fakeViewSvc) GetAgent(_ context.Context, _ string) (*sdk.Agent, error) {
	return f.resp, f.err
}

func TestView_Human_RendersMetadataAndConfig(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeViewSvc{resp: &sdk.Agent{
		ID:          "ag_abc",
		Name:        "Research",
		Description: "deep-dive helper",
		IsBuiltin:   true,
		CreatedAt:   time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC),
		UpdatedAt:   time.Date(2026, 5, 2, 13, 30, 0, 0, time.UTC),
		Config: &sdk.AgentConfig{
			AgentMode:        "smart-reasoning",
			ModelID:          "model_42",
			KBSelectionMode:  "selected",
			KnowledgeBases:   []string{"kb_x", "kb_y"},
			AllowedTools:     []string{"knowledge_search", "web_search"},
			WebSearchEnabled: true,
		},
	}}
	if err := runView(context.Background(), nil, svc, "ag_abc"); err != nil {
		t.Fatalf("runView: %v", err)
	}
	got := out.String()
	for _, want := range []string{"ag_abc", "Research", "deep-dive helper", "Builtin:", "Config:", "smart-reasoning", "model_42", "selected", "kb_x", "knowledge_search", "Web search:"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestView_Human_OmitsEmptyFields(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeViewSvc{resp: &sdk.Agent{
		ID:        "ag_min",
		Name:      "Minimal",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}}
	if err := runView(context.Background(), nil, svc, "ag_min"); err != nil {
		t.Fatalf("runView: %v", err)
	}
	got := out.String()
	if strings.Contains(got, "Description:") {
		t.Errorf("empty description should be omitted, got:\n%s", got)
	}
	if strings.Contains(got, "Builtin:") {
		t.Errorf("non-builtin should not render Builtin: line, got:\n%s", got)
	}
	if strings.Contains(got, "Config:") {
		t.Errorf("nil Config should not render Config: section, got:\n%s", got)
	}
}

func TestView_JSON_BareObject(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeViewSvc{resp: &sdk.Agent{ID: "ag_json", Name: "JSONy"}}
	if err := runView(context.Background(), &cmdutil.JSONOptions{}, svc, "ag_json"); err != nil {
		t.Fatalf("runView: %v", err)
	}
	var got sdk.Agent
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.ID != "ag_json" || got.Name != "JSONy" {
		t.Errorf("bare object shape wrong: id=%s name=%s", got.ID, got.Name)
	}
	if strings.Contains(out.String(), `"ok":`) || strings.Contains(out.String(), `"data":`) {
		t.Errorf("bare output must not carry envelope keys, got %q", out.String())
	}
}

func TestView_404_MapsToResourceNotFound(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeViewSvc{err: errors.New("HTTP error 404: agent not found")}
	err := runView(context.Background(), nil, svc, "ag_missing")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var typed *cmdutil.Error
	if !errors.As(err, &typed) {
		t.Fatalf("expected *cmdutil.Error, got %T", err)
	}
	if typed.Code != cmdutil.CodeResourceNotFound {
		t.Errorf("expected resource.not_found, got %s", typed.Code)
	}
}
