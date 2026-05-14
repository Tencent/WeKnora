package format_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/cli/internal/format"
)

func TestWriteEnvelopeFiltered_NoFilter(t *testing.T) {
	env := format.Success(map[string]any{"items": []any{
		map[string]any{"id": "1", "name": "alpha"},
	}}, &format.Meta{KBID: "kb_x"})
	buf := &bytes.Buffer{}
	if err := format.WriteEnvelopeFiltered(buf, env, nil, ""); err != nil {
		t.Fatalf("err = %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got["ok"] != true {
		t.Errorf("ok = %v, want true", got["ok"])
	}
}

func TestWriteEnvelopeFiltered_FieldsOnListItems(t *testing.T) {
	env := format.Success(map[string]any{"items": []any{
		map[string]any{"id": "1", "name": "alpha", "kb_id": "kb_x", "updated_at": "2026-01-01"},
		map[string]any{"id": "2", "name": "beta", "kb_id": "kb_x", "updated_at": "2026-01-02"},
	}}, nil)

	buf := &bytes.Buffer{}
	if err := format.WriteEnvelopeFiltered(buf, env, []string{"id", "name"}, ""); err != nil {
		t.Fatalf("err = %v", err)
	}

	var got struct {
		OK   bool `json:"ok"`
		Data struct {
			Items []map[string]string `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("parse: %v\n%s", err, buf.String())
	}
	if len(got.Data.Items) != 2 {
		t.Fatalf("items len = %d, want 2", len(got.Data.Items))
	}
	for i, item := range got.Data.Items {
		if _, has := item["kb_id"]; has {
			t.Errorf("item[%d] should not have kb_id: %v", i, item)
		}
		if _, has := item["updated_at"]; has {
			t.Errorf("item[%d] should not have updated_at: %v", i, item)
		}
		if item["id"] == "" || item["name"] == "" {
			t.Errorf("item[%d] missing required keys: %v", i, item)
		}
	}
}

func TestWriteEnvelopeFiltered_FieldsOnSingleObject(t *testing.T) {
	env := format.Success(map[string]any{
		"id":    "kb_x",
		"name":  "Engineering",
		"owner": "alice",
	}, nil)

	buf := &bytes.Buffer{}
	if err := format.WriteEnvelopeFiltered(buf, env, []string{"id", "name"}, ""); err != nil {
		t.Fatalf("err = %v", err)
	}

	var got struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("parse: %v\n%s", err, buf.String())
	}
	if _, has := got.Data["owner"]; has {
		t.Errorf("should not have owner: %v", got.Data)
	}
	if got.Data["id"] != "kb_x" || got.Data["name"] != "Engineering" {
		t.Errorf("missing kept fields: %v", got.Data)
	}
}

func TestWriteEnvelopeFiltered_UnknownFieldSilent(t *testing.T) {
	env := format.Success(map[string]any{"id": "1"}, nil)
	buf := &bytes.Buffer{}
	if err := format.WriteEnvelopeFiltered(buf, env, []string{"id", "nonexistent"}, ""); err != nil {
		t.Fatalf("err = %v", err)
	}
	var got struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.Data["id"] != "1" {
		t.Errorf("id missing: %v", got.Data)
	}
	if _, has := got.Data["nonexistent"]; has {
		t.Errorf("nonexistent should be silently dropped: %v", got.Data)
	}
}

func TestWriteEnvelopeFiltered_PreservesEnvelopeFields(t *testing.T) {
	// Even with field filter, meta/risk/error must be preserved.
	env := format.Success(map[string]any{"items": []any{
		map[string]any{"id": "1", "name": "x", "kb_id": "kb"},
	}}, &format.Meta{KBID: "kb_x", RequestID: "req_123"})

	buf := &bytes.Buffer{}
	if err := format.WriteEnvelopeFiltered(buf, env, []string{"id"}, ""); err != nil {
		t.Fatalf("err = %v", err)
	}
	var got struct {
		OK   bool         `json:"ok"`
		Meta *format.Meta `json:"_meta"`
		Data struct {
			Items []map[string]any `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("parse: %v\n%s", err, buf.String())
	}
	if got.Meta == nil || got.Meta.RequestID != "req_123" {
		t.Errorf("_meta lost or mangled: %+v", got.Meta)
	}
}

func TestWriteEnvelopeFiltered_JQOnly(t *testing.T) {
	env := format.Success(map[string]any{"items": []any{
		map[string]any{"id": "1", "name": "alpha"},
		map[string]any{"id": "2", "name": "beta"},
	}}, nil)

	buf := &bytes.Buffer{}
	if err := format.WriteEnvelopeFiltered(buf, env, nil, ".data.items[].id"); err != nil {
		t.Fatalf("err = %v", err)
	}
	// gh CLI parity: string results render without JSON quotes
	// (see https://github.com/cli/cli/blob/trunk/pkg/export/filter.go).
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %q", len(lines), buf.String())
	}
	if lines[0] != "1" || lines[1] != "2" {
		t.Errorf("wrong output: %q", buf.String())
	}
}

func TestWriteEnvelopeFiltered_FieldsAndJQ(t *testing.T) {
	env := format.Success(map[string]any{"items": []any{
		map[string]any{"id": "1", "name": "alpha", "kb_id": "kb"},
		map[string]any{"id": "2", "name": "beta", "kb_id": "kb"},
	}}, nil)

	buf := &bytes.Buffer{}
	if err := format.WriteEnvelopeFiltered(buf, env, []string{"id", "name"}, ".data.items | length"); err != nil {
		t.Fatalf("err = %v", err)
	}
	out := strings.TrimSpace(buf.String())
	if out != "2" {
		t.Errorf("expected '2', got %q", out)
	}
}

func TestWriteEnvelopeFiltered_JQParseError(t *testing.T) {
	env := format.Success(map[string]any{"x": 1}, nil)
	buf := &bytes.Buffer{}
	err := format.WriteEnvelopeFiltered(buf, env, nil, ".[invalid jq")
	if err == nil {
		t.Fatal("expected parse error, got nil")
	}
	if !strings.Contains(err.Error(), "jq parse") {
		t.Errorf("expected 'jq parse' in error, got %q", err.Error())
	}
}

func TestWriteEnvelopeFiltered_ScalarDataPassThrough(t *testing.T) {
	// Defensive: scalar data should not crash field filter.
	env := format.Success("just a string", nil)
	buf := &bytes.Buffer{}
	if err := format.WriteEnvelopeFiltered(buf, env, []string{"id"}, ""); err != nil {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(buf.String(), `"just a string"`) {
		t.Errorf("scalar data lost: %s", buf.String())
	}
}
