package output_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/cli/internal/output"
)

func TestWriteEnvelope_SuccessWithData(t *testing.T) {
	var buf bytes.Buffer
	data := map[string]string{"id": "kb_x"}
	if err := output.WriteEnvelope(&buf, data, nil, false, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, `"ok":true`) {
		t.Errorf("missing ok:true; got %q", got)
	}
	if !strings.Contains(got, `"data":{"id":"kb_x"}`) {
		t.Errorf("missing data; got %q", got)
	}
}

func TestWriteEnvelope_OmitDataWhenNil(t *testing.T) {
	// Mutation with no payload: the data field should be omitted (omitempty).
	var buf bytes.Buffer
	if err := output.WriteEnvelope(&buf, nil, nil, false, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := buf.String()
	if strings.Contains(got, `"data"`) {
		t.Errorf("data field should be omitted when nil; got %q", got)
	}
	if !strings.Contains(got, `"ok":true`) {
		t.Errorf("missing ok:true; got %q", got)
	}
}

func TestWriteEnvelope_WithMeta(t *testing.T) {
	var buf bytes.Buffer
	meta := &output.Meta{Count: output.IntPtr(2), HasMore: false}
	if err := output.WriteEnvelope(&buf, []string{"a", "b"}, meta, false, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, `"meta":{"count":2}`) {
		// has_more:false should be omitted by omitempty when false
		t.Errorf("meta unexpected shape; got %q", got)
	}
}

// TestWriteEnvelope_ZeroCountSerializes pins the *int fix: a list command that
// sets Count to 0 (empty result) must still emit "count":0, while a meta that
// leaves Count nil (non-list / dry-run) must omit the key entirely. This is the
// agent-contract guarantee that omitempty on a plain int silently broke.
func TestWriteEnvelope_ZeroCountSerializes(t *testing.T) {
	var bufZero bytes.Buffer
	zero := &output.Meta{Count: output.IntPtr(0)}
	if err := output.WriteEnvelope(&bufZero, []string{}, zero, false, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotZero := bufZero.String(); !strings.Contains(gotZero, `"count":0`) {
		t.Errorf("explicit zero count must serialize; got %q", gotZero)
	}

	var bufNil bytes.Buffer
	if err := output.WriteEnvelope(&bufNil, []string{}, &output.Meta{}, false, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotNil := bufNil.String(); strings.Contains(gotNil, "count") {
		t.Errorf("nil count must be omitted; got %q", gotNil)
	}
}

func TestWriteErrorEnvelope_FullShape(t *testing.T) {
	var buf bytes.Buffer
	errDetail := &output.ErrDetail{
		Type:      "input.confirmation_required",
		Message:   "kb delete kb_x requires confirmation",
		Hint:      "re-run with -y/--yes",
		RetryArgv: []string{"weknora", "kb", "delete", "kb_x", "-y"},
		Risk: &output.RiskDetail{
			Level:  "destructive",
			Action: "kb.delete",
		},
	}
	if err := output.WriteErrorEnvelope(&buf, errDetail, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, `"ok":false`) {
		t.Errorf("missing ok:false; got %q", got)
	}
	if !strings.Contains(got, `"type":"input.confirmation_required"`) {
		t.Errorf("missing typed code; got %q", got)
	}
	if !strings.Contains(got, `"retry_argv":["weknora","kb","delete","kb_x","-y"]`) {
		t.Errorf("missing retry_argv; got %q", got)
	}
	if !strings.Contains(got, `"risk":{"level":"destructive","action":"kb.delete"}`) {
		t.Errorf("missing risk; got %q", got)
	}
}

func TestWriteEnvelope_IndentedTTYMode(t *testing.T) {
	var buf bytes.Buffer
	if err := output.WriteEnvelope(&buf, map[string]string{"id": "x"}, nil, true, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "\n  \"") {
		t.Errorf("expected indented multi-line output; got %q", got)
	}
}
