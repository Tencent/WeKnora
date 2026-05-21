package kb

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

type fakeGetSvc struct {
	kb  *sdk.KnowledgeBase
	err error
}

func (f *fakeGetSvc) GetKnowledgeBase(ctx context.Context, id string) (*sdk.KnowledgeBase, error) {
	return f.kb, f.err
}

func TestGet_OK_Human(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeGetSvc{kb: &sdk.KnowledgeBase{
		ID: "kb1", Name: "Marketing", KnowledgeCount: 12, ChunkCount: 245,
	}}
	if err := runView(context.Background(), &ViewOptions{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatHuman}, svc, "kb1"); err != nil {
		t.Fatalf("runGet: %v", err)
	}
	got := out.String()
	for _, want := range []string{"ID:", "kb1", "NAME:", "Marketing", "DOCS:", "12 docs", "CHUNKS:", "245 chunks"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestGet_OK_JSON(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeGetSvc{kb: &sdk.KnowledgeBase{ID: "kb1", Name: "Marketing"}}
	if err := runView(context.Background(), &ViewOptions{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc, "kb1"); err != nil {
		t.Fatalf("runGet: %v", err)
	}
	got := out.String()
	var env struct {
		OK   bool              `json:"ok"`
		Data sdk.KnowledgeBase `json:"data"`
	}
	if err := json.Unmarshal([]byte(got), &env); err != nil {
		t.Fatalf("parse: %v\n%s", err, got)
	}
	if !env.OK {
		t.Errorf("envelope.ok must be true, got %q", got)
	}
	if env.Data.ID != "kb1" {
		t.Errorf("expected id=kb1 in envelope.data, got %q", env.Data.ID)
	}
}

func TestGet_NotFound(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeGetSvc{err: errors.New("HTTP error 404: not found")}
	err := runView(context.Background(), &ViewOptions{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatHuman}, svc, "missing")
	if err == nil {
		t.Fatal("expected error")
	}
	if !cmdutil.IsNotFound(err) {
		t.Errorf("expected resource.not_found, got %v", err)
	}
}

// --- expanded human render: badges + extra KV lines ---

func TestView_Pinned_RendersPinnedLine(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeGetSvc{kb: &sdk.KnowledgeBase{ID: "kb1", Name: "Pinned", IsPinned: true}}
	if err := runView(context.Background(), &ViewOptions{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatHuman}, svc, "kb1"); err != nil {
		t.Fatalf("runView: %v", err)
	}
	if !strings.Contains(out.String(), "PINNED:") {
		t.Errorf("expected PINNED line for IsPinned=true:\n%s", out.String())
	}
}

func TestView_NotPinned_OmitsPinnedLine(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeGetSvc{kb: &sdk.KnowledgeBase{ID: "kb1", Name: "Plain", IsPinned: false}}
	if err := runView(context.Background(), &ViewOptions{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatHuman}, svc, "kb1"); err != nil {
		t.Fatalf("runView: %v", err)
	}
	for _, l := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(l, "PINNED:") {
			t.Errorf("PINNED line should be omitted when IsPinned=false: %q", l)
		}
	}
}

func TestView_Temporary_RendersTempLine(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeGetSvc{kb: &sdk.KnowledgeBase{ID: "kb_t", Name: "Tmp", IsTemporary: true}}
	if err := runView(context.Background(), &ViewOptions{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatHuman}, svc, "kb_t"); err != nil {
		t.Fatalf("runView: %v", err)
	}
	if !strings.Contains(out.String(), "TEMPORARY:") {
		t.Errorf("expected TEMPORARY line:\n%s", out.String())
	}
}

func TestView_SummaryModel(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeGetSvc{kb: &sdk.KnowledgeBase{ID: "kb1", Name: "X", SummaryModelID: "summary-model-x"}}
	if err := runView(context.Background(), &ViewOptions{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatHuman}, svc, "kb1"); err != nil {
		t.Fatalf("runView: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "SUMMARY MODEL:") || !strings.Contains(got, "summary-model-x") {
		t.Errorf("expected SUMMARY MODEL line:\n%s", got)
	}
}

func TestView_TypeAndSource(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeGetSvc{kb: &sdk.KnowledgeBase{ID: "kb1", Name: "X", Type: "general", Description: "d"}}
	if err := runView(context.Background(), &ViewOptions{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatHuman}, svc, "kb1"); err != nil {
		t.Fatalf("runView: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "TYPE:") || !strings.Contains(got, "general") {
		t.Errorf("expected TYPE line for non-empty Type:\n%s", got)
	}
}

func TestView_Processing(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeGetSvc{kb: &sdk.KnowledgeBase{ID: "kb_p", Name: "Busy", IsProcessing: true, ProcessingCount: 3}}
	if err := runView(context.Background(), &ViewOptions{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatHuman}, svc, "kb_p"); err != nil {
		t.Fatalf("runView: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "PROCESSING:") || !strings.Contains(got, "3") {
		t.Errorf("expected PROCESSING line with count:\n%s", got)
	}
}

func TestView_NotProcessing_OmitsProcessingLine(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeGetSvc{kb: &sdk.KnowledgeBase{ID: "kb_idle", Name: "Idle", IsProcessing: false}}
	if err := runView(context.Background(), &ViewOptions{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatHuman}, svc, "kb_idle"); err != nil {
		t.Fatalf("runView: %v", err)
	}
	for _, l := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(l, "PROCESSING:") {
			t.Errorf("PROCESSING line should be omitted: %q", l)
		}
	}
}

func TestView_CreatedAt_AlwaysRendered(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeGetSvc{kb: &sdk.KnowledgeBase{
		ID: "kb1", Name: "X",
		CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
	}}
	if err := runView(context.Background(), &ViewOptions{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatHuman}, svc, "kb1"); err != nil {
		t.Fatalf("runView: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "CREATED:") || !strings.Contains(got, "2026-01-01") {
		t.Errorf("expected CREATED line:\n%s", got)
	}
}
