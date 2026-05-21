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
	// mutation 无 payload 时 data 字段应被省略（omitempty）
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
	meta := &output.Meta{Count: 2, HasMore: false}
	if err := output.WriteEnvelope(&buf, []string{"a", "b"}, meta, false, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, `"meta":{"count":2}`) {
		// has_more:false should be omitted by omitempty when false
		t.Errorf("meta unexpected shape; got %q", got)
	}
}

func TestWriteErrorEnvelope_FullShape(t *testing.T) {
	var buf bytes.Buffer
	errDetail := &output.ErrDetail{
		Type:         "input.confirmation_required",
		Message:      "kb delete kb_x requires confirmation",
		Hint:         "re-run with -y/--yes",
		RetryCommand: "weknora kb delete kb_x -y",
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
	if !strings.Contains(got, `"retry_command":"weknora kb delete kb_x -y"`) {
		t.Errorf("missing retry_command; got %q", got)
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

func TestGetNotice_NilSafe(t *testing.T) {
	output.PendingNotice = nil
	if got := output.GetNotice(); got != nil {
		t.Errorf("GetNotice with nil PendingNotice should return nil; got %v", got)
	}
}

func TestGetNotice_WithSetter(t *testing.T) {
	output.PendingNotice = func() map[string]any {
		return map[string]any{"deprecation": "foo is deprecated"}
	}
	defer func() { output.PendingNotice = nil }()
	got := output.GetNotice()
	if got["deprecation"] != "foo is deprecated" {
		t.Errorf("notice not populated; got %v", got)
	}
}
