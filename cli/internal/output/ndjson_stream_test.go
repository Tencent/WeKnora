package output_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/cli/internal/output"
)

func TestEmitInit_ForcesTypeField(t *testing.T) {
	var buf bytes.Buffer
	// Pass an InitEvent with Type deliberately set to something wrong; EmitInit
	// must overwrite it with "init".
	ev := output.InitEvent{Type: "wrong", SessionID: "sess_x"}
	if err := output.EmitInit(&buf, ev); err != nil {
		t.Fatalf("EmitInit: %v", err)
	}
	var got struct {
		Type      string `json:"type"`
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, buf.String())
	}
	if got.Type != "init" {
		t.Errorf("type: got %q, want init", got.Type)
	}
	if got.SessionID != "sess_x" {
		t.Errorf("session_id: got %q, want sess_x", got.SessionID)
	}
}

func TestEmitInit_OmitsEmptyOptionalFields(t *testing.T) {
	var buf bytes.Buffer
	// Only SessionID set; all optional fields should be absent from JSON.
	ev := output.InitEvent{SessionID: "sess_only"}
	if err := output.EmitInit(&buf, ev); err != nil {
		t.Fatalf("EmitInit: %v", err)
	}
	line := buf.String()
	for _, field := range []string{"kb_id", "agent_id", "model", "profile", "request_id", "message_id"} {
		if strings.Contains(line, `"`+field+`"`) {
			t.Errorf("optional field %q should be omitted when empty, got: %s", field, line)
		}
	}
}

func TestEmitSDKEvent_PassthroughVerbatim(t *testing.T) {
	var buf bytes.Buffer
	payload := map[string]any{"type": "answer", "content": "hi"}
	if err := output.EmitSDKEvent(&buf, payload); err != nil {
		t.Fatalf("EmitSDKEvent: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, buf.String())
	}
	if got["type"] != "answer" {
		t.Errorf("type: got %v, want answer", got["type"])
	}
	if got["content"] != "hi" {
		t.Errorf("content: got %v, want hi", got["content"])
	}
	// Must be a single NDJSON line terminated by newline.
	raw := buf.String()
	if !strings.HasSuffix(raw, "\n") {
		t.Errorf("NDJSON line must end with newline, got %q", raw)
	}
	if strings.Count(raw, "\n") != 1 {
		t.Errorf("expected exactly one newline, got %d in %q", strings.Count(raw, "\n"), raw)
	}
}
