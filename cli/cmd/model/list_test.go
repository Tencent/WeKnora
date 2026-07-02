package modelcmd

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

type fakeListSvc struct {
	models []sdk.Model
	err    error
}

func (f *fakeListSvc) ListModels(_ context.Context) ([]sdk.Model, error) {
	return f.models, f.err
}

func TestModelList_Text(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeListSvc{models: []sdk.Model{
		{ID: "m1", DisplayName: "GPT-X", Type: sdk.ModelTypeKnowledgeQA, Source: sdk.ModelSourceOpenAI, IsDefault: true},
		{ID: "m2", Name: "bge", Type: sdk.ModelTypeEmbedding, Source: sdk.ModelSourceLocal},
	}}
	if err := runList(context.Background(), &ListOptions{Limit: 30}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc); err != nil {
		t.Fatalf("runList: %v", err)
	}
	got := out.String()
	for _, want := range []string{"ID", "NAME", "TYPE", "SOURCE", "m1", "GPT-X", "KnowledgeQA", "m2", "bge", "Embedding", "default"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

// TestModelList_TypeFilter: --type narrows the set case-insensitively, and the
// JSON envelope's meta.count reflects the filtered total.
func TestModelList_TypeFilter(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeListSvc{models: []sdk.Model{
		{ID: "m1", Type: sdk.ModelTypeKnowledgeQA},
		{ID: "m2", Type: sdk.ModelTypeEmbedding},
	}}
	if err := runList(context.Background(), &ListOptions{Type: "embedding", Limit: 30}, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc); err != nil {
		t.Fatalf("runList: %v", err)
	}
	var env struct {
		Data []sdk.Model `json:"data"`
		Meta struct {
			Count int `json:"count"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("parse: %v\n%s", err, out.String())
	}
	if len(env.Data) != 1 || env.Data[0].ID != "m2" {
		t.Errorf("expected only m2 (Embedding), got %+v", env.Data)
	}
	if env.Meta.Count != 1 {
		t.Errorf("meta.count = %d, want 1", env.Meta.Count)
	}
}

func TestModelList_Empty(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeListSvc{models: nil}
	if err := runList(context.Background(), &ListOptions{Limit: 30}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc); err != nil {
		t.Fatalf("runList: %v", err)
	}
	if !strings.Contains(out.String(), "(no models)") {
		t.Errorf("expected empty marker, got %q", out.String())
	}
}

func TestModelList_EmptyAfterFilter(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeListSvc{models: []sdk.Model{{ID: "m1", Type: sdk.ModelTypeEmbedding}}}
	if err := runList(context.Background(), &ListOptions{Type: "Rerank", Limit: 30}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc); err != nil {
		t.Fatalf("runList: %v", err)
	}
	if !strings.Contains(out.String(), "(no models match the filter)") {
		t.Errorf("expected filter empty marker, got %q", out.String())
	}
}

// TestModelList_SourceFilter: --source narrows by provider, case-insensitively.
func TestModelList_SourceFilter(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeListSvc{models: []sdk.Model{
		{ID: "m1", Type: sdk.ModelTypeEmbedding, Source: sdk.ModelSourceLocal},
		{ID: "m2", Type: sdk.ModelTypeKnowledgeQA, Source: sdk.ModelSourceOpenAI},
	}}
	if err := runList(context.Background(), &ListOptions{Source: "OpenAI", Limit: 30}, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc); err != nil {
		t.Fatalf("runList: %v", err)
	}
	var env struct {
		Data []sdk.Model `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("parse: %v\n%s", err, out.String())
	}
	if len(env.Data) != 1 || env.Data[0].ID != "m2" {
		t.Errorf("expected only m2 (openai), got %+v", env.Data)
	}
}

// TestModelList_InvalidEnum: a typo'd --type / --source is rejected up front
// (input.invalid_argument) instead of silently returning an empty set.
func TestModelList_InvalidEnum(t *testing.T) {
	for _, tc := range []struct{ name string; opts ListOptions }{
		{"type", ListOptions{Type: "bogus", Limit: 30}},
		{"source", ListOptions{Source: "bogus", Limit: 30}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _ = iostreams.SetForTest(t)
			svc := &fakeListSvc{models: []sdk.Model{{ID: "m1", Type: sdk.ModelTypeEmbedding, Source: sdk.ModelSourceLocal}}}
			err := runList(context.Background(), &tc.opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc)
			var typed *cmdutil.Error
			if !errors.As(err, &typed) || typed.Code != cmdutil.CodeInputInvalidArgument {
				t.Errorf("expected input.invalid_argument, got %v", err)
			}
		})
	}
}

// TestModelList_Limit_TruncatesAndSignals verifies --limit caps model list
// output and reports meta.total_count/has_more like every other list command.
// Regression: model list had no --limit, so an agent could not cap output or
// tell whether it saw the full set.
func TestModelList_Limit_TruncatesAndSignals(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeListSvc{models: []sdk.Model{
		{ID: "m1", Type: sdk.ModelTypeKnowledgeQA},
		{ID: "m2", Type: sdk.ModelTypeKnowledgeQA},
		{ID: "m3", Type: sdk.ModelTypeKnowledgeQA},
	}}
	fopts := &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}
	if err := runList(context.Background(), &ListOptions{Limit: 2}, fopts, svc); err != nil {
		t.Fatalf("runList: %v", err)
	}
	got := out.String()
	if n := strings.Count(got, `"id":"m`); n != 2 {
		t.Errorf("--limit 2 should slice to 2 models, got %d in:\n%s", n, got)
	}
	if !strings.Contains(got, `"has_more":true`) {
		t.Errorf("truncated model list must set has_more:true; got:\n%s", got)
	}
	if !strings.Contains(got, `"total_count":3`) {
		t.Errorf("truncated model list must report total_count:3; got:\n%s", got)
	}
}

// TestModelList_BadLimit_Rejected verifies an out-of-range --limit is exit-5
// typed validation, consistent with kb/session list.
func TestModelList_BadLimit_Rejected(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	err := runList(context.Background(), &ListOptions{Limit: 99999}, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, &fakeListSvc{})
	if err == nil {
		t.Fatal("--limit 99999 must be rejected")
	}
	var e *cmdutil.Error
	if !errors.As(err, &e) || e.Code != cmdutil.CodeInputInvalidArgument {
		t.Errorf("want input.invalid_argument, got %v", err)
	}
}
