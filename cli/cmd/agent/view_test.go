package agentcmd

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
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
	if err := runView(context.Background(), &cmdutil.FormatOptions{Mode: cmdutil.FormatHuman}, svc, "ag_abc"); err != nil {
		t.Fatalf("runView: %v", err)
	}
	got := out.String()
	for _, want := range []string{"ag_abc", "Research", "deep-dive helper", "Builtin:", "Identity", "LLM", "KB attachment", "Tools", "smart-reasoning", "model_42", "selected", "kb_x", "knowledge_search", "Web search"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

// TestAgentViewFields_TopLevelOnly pins the contract that the field-hint
// list on `agent view` only lists top-level Agent keys (including `config`
// as a whole). Nested AgentConfig fields are reachable via --jq.
func TestAgentViewFields_TopLevelOnly(t *testing.T) {
	for _, want := range []string{"id", "name", "config", "created_at"} {
		if !slices.Contains(agentViewFields, want) {
			t.Errorf("agentViewFields missing top-level key %q", want)
		}
	}
	for _, dotted := range []string{"config.system_prompt", "config.model_id", "config.fallback_strategy"} {
		if slices.Contains(agentViewFields, dotted) {
			t.Errorf("agentViewFields must not list dotted nested key %q (the --jq projection path handles nesting directly)", dotted)
		}
	}
}

// TestRenderAgent_RendersAllGroupsWithOmitEmpty validates the grouped
// human rendering: present groups print, zero-value fields omit, and an
// entire section is suppressed when all of its fields are zero.
func TestRenderAgent_RendersAllGroupsWithOmitEmpty(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	ag := &sdk.Agent{
		ID:   "ag_abc",
		Name: "Test",
		Config: &sdk.AgentConfig{
			AgentMode:          "smart-reasoning",
			SystemPrompt:       "You help users.",
			ModelID:            "model-x",
			Temperature:        0.7,
			KBSelectionMode:    "selected",
			KnowledgeBases:     []string{"kb_a"},
			FAQPriorityEnabled: true,
			WebSearchEnabled:   false, // zero — omitted in human
			FallbackStrategy:   "fixed",
			FallbackResponse:   "I don't know.",
		},
	}
	renderAgent(iostreams.IO.Out, ag)
	body := out.String()
	// Group labels appear:
	for _, label := range []string{"Identity", "LLM", "KB attachment", "FAQ", "Fallback", "Templates"} {
		if !strings.Contains(body, label) {
			t.Errorf("missing group label %q in:\n%s", label, body)
		}
	}
	// Set fields rendered:
	for _, want := range []string{"smart-reasoning", "model-x", "You help users."} {
		if !strings.Contains(body, want) {
			t.Errorf("missing value %q in:\n%s", want, body)
		}
	}
	// Zero-value fields omitted (web search disabled → max_results not shown):
	if strings.Contains(body, "web_search_max_results") {
		t.Errorf("zero-valued web_search_max_results leaked into:\n%s", body)
	}
	// Section with all zero values must be suppressed entirely (no Retrieval
	// fields were set in this fixture).
	if strings.Contains(body, "Retrieval") {
		t.Errorf("Retrieval section rendered with all-zero fields in:\n%s", body)
	}
	// Multi-turn section is all-zero too — must be suppressed.
	if strings.Contains(body, "Multi-turn") {
		t.Errorf("Multi-turn section rendered with all-zero fields in:\n%s", body)
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
	if err := runView(context.Background(), &cmdutil.FormatOptions{Mode: cmdutil.FormatHuman}, svc, "ag_min"); err != nil {
		t.Fatalf("runView: %v", err)
	}
	got := out.String()
	if strings.Contains(got, "Description:") {
		t.Errorf("empty description should be omitted, got:\n%s", got)
	}
	if strings.Contains(got, "Builtin:") {
		t.Errorf("non-builtin should not render Builtin: line, got:\n%s", got)
	}
	// With Config==nil none of the grouped config sections should appear.
	for _, label := range []string{"LLM:", "KB attachment:", "Retrieval:", "Query rewrite:", "Tools:", "FAQ:", "Web search:", "Multi-turn:", "Fallback:", "Templates:"} {
		if strings.Contains(got, label) {
			t.Errorf("nil Config should not render %q, got:\n%s", label, got)
		}
	}
}

func TestView_JSON_BareObject(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeViewSvc{resp: &sdk.Agent{ID: "ag_json", Name: "JSONy"}}
	if err := runView(context.Background(), &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc, "ag_json"); err != nil {
		t.Fatalf("runView: %v", err)
	}
	var env struct {
		OK   bool      `json:"ok"`
		Data sdk.Agent `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := env.Data
	if got.ID != "ag_json" || got.Name != "JSONy" {
		t.Errorf("envelope.data shape wrong: id=%s name=%s", got.ID, got.Name)
	}
	if !env.OK {
		t.Errorf("envelope.ok must be true, got %q", out.String())
	}
}

func TestView_404_MapsToResourceNotFound(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeViewSvc{err: errors.New("HTTP error 404: agent not found")}
	err := runView(context.Background(), &cmdutil.FormatOptions{Mode: cmdutil.FormatHuman}, svc, "ag_missing")
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
