package cmdutil_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/output"
)

func TestEmit_WrapsInEnvelope(t *testing.T) {
	var buf bytes.Buffer
	fopts := &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}
	data := []map[string]string{{"id": "kb_x"}, {"id": "kb_y"}}
	if err := fopts.Emit(&buf, data, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, `"ok":true`) {
		t.Errorf("expected envelope ok:true; got %q", got)
	}
	if !strings.Contains(got, `"data":[`) {
		t.Errorf("expected data array; got %q", got)
	}
}

func TestEmit_WithMeta(t *testing.T) {
	var buf bytes.Buffer
	fopts := &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}
	meta := &output.Meta{Count: 2, HasMore: true}
	if err := fopts.Emit(&buf, []string{"a", "b"}, meta); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, `"count":2`) {
		t.Errorf("expected meta.count; got %q", got)
	}
	if !strings.Contains(got, `"has_more":true`) {
		t.Errorf("expected meta.has_more; got %q", got)
	}
}

func TestEmit_NDJSON_NoEnvelope(t *testing.T) {
	// NDJSON path is event-passthrough; no envelope wrapping per spec §5.
	var buf bytes.Buffer
	fopts := &cmdutil.FormatOptions{Mode: cmdutil.FormatNDJSON}
	data := []map[string]string{{"id": "a"}, {"id": "b"}}
	if err := fopts.Emit(&buf, data, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := buf.String()
	if strings.Contains(got, `"ok":true`) {
		t.Errorf("NDJSON path should not wrap in envelope; got %q", got)
	}
	// Expect 2 lines, one per item
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 2 {
		t.Errorf("expected 2 NDJSON lines; got %d: %q", len(lines), got)
	}
}

func TestEmit_TTYIndents(t *testing.T) {
	var buf bytes.Buffer
	fopts := &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON, TTY: true}
	if err := fopts.Emit(&buf, map[string]string{"id": "x"}, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "\n  \"") {
		t.Errorf("expected indented multi-line output (TTY=true); got %q", got)
	}
}

func TestEmit_PopulatesProfile(t *testing.T) {
	cmdutil.SetProfile("staging")
	t.Cleanup(func() { cmdutil.SetProfile("") })

	var buf bytes.Buffer
	fopts := &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}
	if err := fopts.Emit(&buf, map[string]string{"id": "x"}, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, `"profile":"staging"`) {
		t.Errorf("expected profile:staging in envelope; got %q", got)
	}
}
