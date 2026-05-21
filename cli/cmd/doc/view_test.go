package doc

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
	if err := runView(context.Background(), &ViewOptions{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatHuman}, svc, "doc_abc"); err != nil {
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

// A doc with no FileName falls back to Title for the NAME line - same
// ordering as `doc list` (KnowledgeDisplayName precedence).
func TestView_Human_TitleFallback(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeViewSvc{doc: &sdk.Knowledge{ID: "doc_url", Title: "Pasted article", FileName: ""}}
	if err := runView(context.Background(), &ViewOptions{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatHuman}, svc, "doc_url"); err != nil {
		t.Fatalf("runView: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "Pasted article") {
		t.Errorf("expected Title fallback in NAME line; got:\n%s", got)
	}
}

// Optional fields (ProcessedAt nil, ErrorMessage empty, EmbeddingModelID
// empty) must not produce empty KEY: lines - the formatter omits them
// rather than printing "PROCESSED: -".
func TestView_Human_OmitsEmptyFields(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeViewSvc{doc: &sdk.Knowledge{
		ID:       "doc_abc",
		FileName: "x.txt",
	}}
	if err := runView(context.Background(), &ViewOptions{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatHuman}, svc, "doc_abc"); err != nil {
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
	if err := runView(context.Background(), &ViewOptions{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc, "doc_abc"); err != nil {
		t.Fatalf("runView: %v", err)
	}
	got := out.String()
	var env struct {
		OK   bool          `json:"ok"`
		Data sdk.Knowledge `json:"data"`
	}
	if err := json.Unmarshal([]byte(got), &env); err != nil {
		t.Fatalf("parse: %v\n%s", err, got)
	}
	if !env.OK {
		t.Errorf("envelope.ok must be true, got %q", got)
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
	err := runView(context.Background(), &ViewOptions{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatHuman}, svc, "missing")
	if err == nil {
		t.Fatal("expected error")
	}
	if !cmdutil.IsNotFound(err) {
		t.Errorf("expected resource.not_found, got %v", err)
	}
}

// --- expanded human render: title/desc/source/channel/etc. ---

func TestView_Title_RendersWhenDifferentFromFileName(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeViewSvc{doc: &sdk.Knowledge{
		ID: "doc_t", FileName: "raw.pdf", Title: "Quarterly Plan",
	}}
	if err := runView(context.Background(), &ViewOptions{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatHuman}, svc, "doc_t"); err != nil {
		t.Fatalf("runView: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "TITLE:") || !strings.Contains(got, "Quarterly Plan") {
		t.Errorf("expected TITLE line:\n%s", got)
	}
}

// When Title and FileName are equal, the TITLE line is redundant with NAME
// and should be omitted.
func TestView_Title_OmittedWhenSameAsFileName(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeViewSvc{doc: &sdk.Knowledge{
		ID: "doc_t", FileName: "policy.pdf", Title: "policy.pdf",
	}}
	if err := runView(context.Background(), &ViewOptions{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatHuman}, svc, "doc_t"); err != nil {
		t.Fatalf("runView: %v", err)
	}
	for _, l := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(l, "TITLE:") {
			t.Errorf("TITLE line should be omitted when same as filename: %q", l)
		}
	}
}

func TestView_Description(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeViewSvc{doc: &sdk.Knowledge{ID: "doc_d", FileName: "x.pdf", Description: "Annual review"}}
	if err := runView(context.Background(), &ViewOptions{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatHuman}, svc, "doc_d"); err != nil {
		t.Fatalf("runView: %v", err)
	}
	if !strings.Contains(out.String(), "Annual review") {
		t.Errorf("expected description text:\n%s", out.String())
	}
}

func TestView_SourceAndChannel(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeViewSvc{doc: &sdk.Knowledge{
		ID: "doc_s", FileName: "x.pdf", Source: "https://example.com/x", Channel: "web",
	}}
	if err := runView(context.Background(), &ViewOptions{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatHuman}, svc, "doc_s"); err != nil {
		t.Fatalf("runView: %v", err)
	}
	got := out.String()
	for _, want := range []string{"SOURCE:", "https://example.com/x", "CHANNEL:", "web"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestView_SummaryAndEnableStatus(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeViewSvc{doc: &sdk.Knowledge{
		ID: "doc_st", FileName: "x.pdf",
		SummaryStatus: "completed",
		EnableStatus:  "disabled",
	}}
	if err := runView(context.Background(), &ViewOptions{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatHuman}, svc, "doc_st"); err != nil {
		t.Fatalf("runView: %v", err)
	}
	got := out.String()
	for _, want := range []string{"SUMMARY:", "completed", "ENABLED:", "disabled"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestView_TagID(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeViewSvc{doc: &sdk.Knowledge{ID: "doc_t", FileName: "x.pdf", TagID: "tag_abc"}}
	if err := runView(context.Background(), &ViewOptions{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatHuman}, svc, "doc_t"); err != nil {
		t.Fatalf("runView: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "TAG:") || !strings.Contains(got, "tag_abc") {
		t.Errorf("expected TAG line:\n%s", got)
	}
}

func TestView_StorageSize_Human(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeViewSvc{doc: &sdk.Knowledge{ID: "doc_sz", FileName: "x.pdf", StorageSize: 2 * 1024 * 1024}}
	if err := runView(context.Background(), &ViewOptions{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatHuman}, svc, "doc_sz"); err != nil {
		t.Fatalf("runView: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "STORAGE:") || !strings.Contains(got, "MB") {
		t.Errorf("expected STORAGE line with human-readable bytes:\n%s", got)
	}
}

func TestView_FileHash_Prefix12(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeViewSvc{doc: &sdk.Knowledge{
		ID:       "doc_h",
		FileName: "x.pdf",
		FileHash: "abcdef1234567890fedcba0987654321",
	}}
	if err := runView(context.Background(), &ViewOptions{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatHuman}, svc, "doc_h"); err != nil {
		t.Fatalf("runView: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "HASH:") || !strings.Contains(got, "abcdef123456") {
		t.Errorf("expected HASH line with 12-char prefix:\n%s", got)
	}
	if strings.Contains(got, "abcdef1234567890fedcba0987654321") {
		t.Errorf("full hash should be truncated, got:\n%s", got)
	}
}

func TestView_ErrorMessage_WarnPrefix(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeViewSvc{doc: &sdk.Knowledge{
		ID: "doc_e", FileName: "x.pdf", ErrorMessage: "parser failed at offset 4096",
	}}
	if err := runView(context.Background(), &ViewOptions{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatHuman}, svc, "doc_e"); err != nil {
		t.Fatalf("runView: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "parser failed at offset 4096") {
		t.Errorf("expected error message rendered:\n%s", got)
	}
	// Either ERROR: or a WARN-prefixed label is acceptable as a "warn"
	// signal — assert at least one.
	if !strings.Contains(got, "ERROR:") && !strings.Contains(got, "WARN") {
		t.Errorf("expected ERROR or WARN prefix on error line:\n%s", got)
	}
}
