package format_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/cli/internal/format"
)

func TestWriteJSON_BareArray(t *testing.T) {
	buf := &bytes.Buffer{}
	if err := format.WriteJSON(buf, []map[string]string{
		{"id": "1", "name": "alpha"},
		{"id": "2", "name": "beta"},
	}); err != nil {
		t.Fatalf("err = %v", err)
	}
	if !bytes.HasPrefix(buf.Bytes(), []byte("[")) {
		t.Errorf("expected bare JSON array, got %q", buf.String())
	}
}

func TestWriteJSON_BareObject(t *testing.T) {
	buf := &bytes.Buffer{}
	if err := format.WriteJSON(buf, map[string]any{"id": "kb_x", "name": "Engineering"}); err != nil {
		t.Fatalf("err = %v", err)
	}
	if !bytes.HasPrefix(buf.Bytes(), []byte("{")) {
		t.Errorf("expected bare JSON object, got %q", buf.String())
	}
	if bytes.Contains(buf.Bytes(), []byte(`"ok":`)) || bytes.Contains(buf.Bytes(), []byte(`"data":`)) {
		t.Errorf("bare output must not carry envelope keys: %s", buf.String())
	}
}

func TestWriteJSONFiltered_FieldsOnArray(t *testing.T) {
	buf := &bytes.Buffer{}
	items := []map[string]any{
		{"id": "1", "name": "alpha", "kb_id": "kb_x", "updated_at": "2026-01-01"},
		{"id": "2", "name": "beta", "kb_id": "kb_x", "updated_at": "2026-01-02"},
	}
	if err := format.WriteJSONFiltered(buf, items, []string{"id", "name"}, ""); err != nil {
		t.Fatalf("err = %v", err)
	}
	var got []map[string]string
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("parse: %v\n%s", err, buf.String())
	}
	if len(got) != 2 {
		t.Fatalf("items len = %d, want 2", len(got))
	}
	for i, item := range got {
		if _, has := item["kb_id"]; has {
			t.Errorf("item[%d] should not have kb_id: %v", i, item)
		}
		if item["id"] == "" || item["name"] == "" {
			t.Errorf("item[%d] missing kept fields: %v", i, item)
		}
	}
}

func TestWriteJSONFiltered_FieldsOnObject(t *testing.T) {
	buf := &bytes.Buffer{}
	obj := map[string]any{"id": "kb_x", "name": "Engineering", "owner": "alice"}
	if err := format.WriteJSONFiltered(buf, obj, []string{"id", "name"}, ""); err != nil {
		t.Fatalf("err = %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("parse: %v\n%s", err, buf.String())
	}
	if _, has := got["owner"]; has {
		t.Errorf("should not retain owner: %v", got)
	}
	if got["id"] != "kb_x" || got["name"] != "Engineering" {
		t.Errorf("kept fields missing: %v", got)
	}
}

func TestWriteJSONFiltered_UnknownFieldSilent(t *testing.T) {
	buf := &bytes.Buffer{}
	if err := format.WriteJSONFiltered(buf, map[string]any{"id": "1"}, []string{"id", "nonexistent"}, ""); err != nil {
		t.Fatalf("err = %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got["id"] != "1" {
		t.Errorf("id missing: %v", got)
	}
	if _, has := got["nonexistent"]; has {
		t.Errorf("nonexistent should be silently dropped: %v", got)
	}
}

func TestWriteJSONFiltered_JQOnly(t *testing.T) {
	buf := &bytes.Buffer{}
	items := []map[string]any{
		{"id": "1", "name": "alpha"},
		{"id": "2", "name": "beta"},
	}
	if err := format.WriteJSONFiltered(buf, items, nil, ".[].id"); err != nil {
		t.Fatalf("err = %v", err)
	}
	// gh CLI parity: string results render without JSON quotes.
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 2 || lines[0] != "1" || lines[1] != "2" {
		t.Errorf("jq output mismatch: %q", buf.String())
	}
}

func TestWriteJSONFiltered_FieldsAndJQ(t *testing.T) {
	buf := &bytes.Buffer{}
	items := []map[string]any{
		{"id": "1", "name": "alpha", "secret": "drop-me"},
		{"id": "2", "name": "beta", "secret": "drop-me"},
	}
	// Field filter first → then jq selects from filtered shape.
	if err := format.WriteJSONFiltered(buf, items, []string{"id"}, ".[].id"); err != nil {
		t.Fatalf("err = %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "drop-me") {
		t.Errorf("field filter must drop unrequested keys before jq: %q", out)
	}
}

func TestWriteJSONFiltered_NilDataPassthrough(t *testing.T) {
	buf := &bytes.Buffer{}
	if err := format.WriteJSONFiltered(buf, nil, []string{"id"}, ""); err != nil {
		t.Fatalf("err = %v", err)
	}
	if strings.TrimSpace(buf.String()) != "null" {
		t.Errorf("nil should marshal to bare null, got %q", buf.String())
	}
}
