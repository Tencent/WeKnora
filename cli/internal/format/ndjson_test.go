package format_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/cli/internal/format"
)

func TestWriteNDJSON_ArraySplitsPerElement(t *testing.T) {
	var buf bytes.Buffer
	arr := []map[string]any{{"id": "1"}, {"id": "2"}, {"id": "3"}}
	if err := format.WriteNDJSON(&buf, arr); err != nil {
		t.Fatalf("WriteNDJSON: %v", err)
	}
	want := `{"id":"1"}` + "\n" + `{"id":"2"}` + "\n" + `{"id":"3"}` + "\n"
	if buf.String() != want {
		t.Errorf("got %q want %q", buf.String(), want)
	}
}

func TestWriteNDJSON_SingleRecord(t *testing.T) {
	var buf bytes.Buffer
	rec := map[string]string{"id": "abc"}
	if err := format.WriteNDJSON(&buf, rec); err != nil {
		t.Fatalf("WriteNDJSON: %v", err)
	}
	want := `{"id":"abc"}` + "\n"
	if buf.String() != want {
		t.Errorf("got %q want %q", buf.String(), want)
	}
}

func TestWriteNDJSON_EmptySlice(t *testing.T) {
	var buf bytes.Buffer
	if err := format.WriteNDJSON(&buf, []string{}); err != nil {
		t.Fatalf("WriteNDJSON: %v", err)
	}
	if buf.String() != "" {
		t.Errorf("got %q want empty", buf.String())
	}
}

func TestWriteNDJSON_TypedSlice(t *testing.T) {
	type item struct {
		ID string `json:"id"`
	}
	var buf bytes.Buffer
	if err := format.WriteNDJSON(&buf, []item{{ID: "x"}, {ID: "y"}}); err != nil {
		t.Fatalf("WriteNDJSON: %v", err)
	}
	want := `{"id":"x"}` + "\n" + `{"id":"y"}` + "\n"
	if buf.String() != want {
		t.Errorf("got %q want %q", buf.String(), want)
	}
}

func TestWriteNDJSON_NoHTMLEscape(t *testing.T) {
	var buf bytes.Buffer
	if err := format.WriteNDJSON(&buf, map[string]string{"k": "a<b>c&d"}); err != nil {
		t.Fatalf("WriteNDJSON: %v", err)
	}
	if !strings.Contains(buf.String(), "a<b>c&d") {
		t.Errorf("expected unescaped HTML chars, got %q", buf.String())
	}
}
