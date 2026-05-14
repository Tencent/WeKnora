package doc

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

// fakeViewSvc scripts a GetKnowledge response. Tests bypass cobra and call
// runView directly with this fake injected.
type fakeViewSvc struct {
	doc *sdk.Knowledge
	err error
}

func (f *fakeViewSvc) GetKnowledge(_ context.Context, _ string) (*sdk.Knowledge, error) {
	return f.doc, f.err
}

func TestView_Human_RendersExpectedFields(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	processed := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	svc := &fakeViewSvc{doc: &sdk.Knowledge{
		ID:               "doc_abc",
		KnowledgeBaseID:  "kb1",
		FileName:         "policy.pdf",
		Title:            "Policy",
		FileType:         "pdf",
		FileSize:         2048,
		ParseStatus:      "completed",
		EmbeddingModelID: "text-embedding-3",
		CreatedAt:        time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt:        time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
		ProcessedAt:      &processed,
	}}
	if err := runView(context.Background(), &ViewOptions{}, nil, svc, "doc_abc"); err != nil {
		t.Fatalf("runView: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"ID:", "doc_abc", "NAME:", "policy.pdf", "KB:", "kb1",
		"TYPE:", "pdf", "SIZE:", "STATUS:", "completed",
		"EMBEDDING:", "text-embedding-3", "PROCESSED:",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

// A doc with no FileName falls back to Title for the NAME line — same
// ordering as `doc list` (KnowledgeDisplayName precedence).
func TestView_Human_TitleFallback(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeViewSvc{doc: &sdk.Knowledge{ID: "doc_url", Title: "Pasted article", FileName: ""}}
	if err := runView(context.Background(), &ViewOptions{}, nil, svc, "doc_url"); err != nil {
		t.Fatalf("runView: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "Pasted article") {
		t.Errorf("expected Title fallback in NAME line; got:\n%s", got)
	}
}

// Optional fields (ProcessedAt nil, ErrorMessage empty, EmbeddingModelID
// empty) must not produce empty KEY: lines — the formatter omits them
// rather than printing "PROCESSED: -".
func TestView_Human_OmitsEmptyFields(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeViewSvc{doc: &sdk.Knowledge{
		ID:       "doc_abc",
		FileName: "x.txt",
	}}
	if err := runView(context.Background(), &ViewOptions{}, nil, svc, "doc_abc"); err != nil {
		t.Fatalf("runView: %v", err)
	}
	// Line-prefix match (not substring): "ERROR:" as a substring could
	// also appear inside `STATUS:    error` if a future test seeds
	// ParseStatus="error". Splitting by newline pins the assertion to the
	// label column.
	lines := strings.Split(out.String(), "\n")
	for _, prefix := range []string{"PROCESSED:", "EMBEDDING:", "ERROR:"} {
		for _, l := range lines {
			if strings.HasPrefix(l, prefix) {
				t.Errorf("expected no line beginning with %q (empty field), got: %q", prefix, l)
			}
		}
	}
}

func TestView_JSON_BareObject(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeViewSvc{doc: &sdk.Knowledge{ID: "doc_abc", FileName: "x.txt", KnowledgeBaseID: "kb1"}}
	if err := runView(context.Background(), &ViewOptions{}, &cmdutil.JSONOptions{}, svc, "doc_abc"); err != nil {
		t.Fatalf("runView: %v", err)
	}
	got := out.String()
	if strings.Contains(got, `"ok":`) || strings.Contains(got, `"data":`) {
		t.Errorf("bare output must not carry envelope keys: %q", got)
	}
	for _, want := range []string{`"id":"doc_abc"`, `"file_name":"x.txt"`, `"knowledge_base_id":"kb1"`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestView_NotFound_ClassifiedAs404(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeViewSvc{err: errors.New("HTTP error 404: not found")}
	err := runView(context.Background(), &ViewOptions{}, nil, svc, "missing")
	if err == nil {
		t.Fatal("expected error")
	}
	if !cmdutil.IsNotFound(err) {
		t.Errorf("expected resource.not_found, got %v", err)
	}
}
