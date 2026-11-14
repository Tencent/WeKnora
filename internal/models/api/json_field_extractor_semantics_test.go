package api

import (
	"encoding/json"
	"testing"
	"unicode/utf8"
)

func TestJSONFieldExtractorSupplementaryUnicodeMatchesJSON(t *testing.T) {
	tests := []struct {
		name      string
		field     string
		arguments string
	}{
		{"content pair", "content", `{"path":"report.txt","content":"Launch \ud83d\ude80\n"}`},
		{"path pair", "path", `{"path":"\ud83d\udcdd.txt","content":"done"}`},
		{"two pairs", "content", `{"path":"report.txt","content":"\uD83D\uDC69\u200d\uD83D\uDD2C"}`},
		{"thinking pair", "thought", `{"thought":"Delivery \ud83d\ude80","next_thought_needed":false}`},
		{"BMP control", "content", `{"path":"report.txt","content":"\u4e16\u754c"}`},
		{"literal control", "content", `{"path":"report.txt","content":"Launch 🚀"}`},
		{"unpaired control", "content", `{"path":"report.txt","content":"\ud83d x \udc00"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkFieldAtEverySplit(t, tt.arguments, tt.field)
		})
	}
}

func TestJSONFieldExtractorFindsKeyAfterSameNamedValue(t *testing.T) {
	tests := []struct {
		field     string
		arguments string
	}{
		{"content", `{"path":"content","content":"real file contents\n"}`},
		{"path", `{"content":"path","path":"notes/report.txt"}`},
		{"content", `{"path":"normal.txt","content":"ordinary text"}`},
		{"path", `{"path":"normal.txt","content":"ordinary text"}`},
	}
	for _, tt := range tests {
		t.Run(tt.field+"/"+tt.arguments, func(t *testing.T) {
			checkFieldAtEverySplit(t, tt.arguments, tt.field)
		})
	}
}

func checkFieldAtEverySplit(t *testing.T, arguments, field string) {
	t.Helper()
	var expected map[string]json.RawMessage
	if err := json.Unmarshal([]byte(arguments), &expected); err != nil {
		t.Fatal(err)
	}
	var want string
	if err := json.Unmarshal(expected[field], &want); err != nil {
		t.Fatal(err)
	}
	for split := 0; split <= len(arguments); split++ {
		// Outer provider events decode their argument deltas as UTF-8 strings.
		if !utf8.ValidString(arguments[:split]) || !utf8.ValidString(arguments[split:]) {
			continue
		}
		e := NewJSONFieldExtractor(field)
		first := e.Feed(arguments[:split])
		second := e.Feed(arguments[split:])
		if !utf8.ValidString(first) || !utf8.ValidString(second) {
			t.Fatalf("split %d emitted invalid UTF-8: %q / %q", split, first, second)
		}
		if got := first + second; got != want || !e.IsDone() {
			t.Fatalf("split %d: got %q, done=%t; JSON decoded %q", split, got, e.IsDone(), want)
		}
	}
}

func TestJSONFieldExtractorHoldsPotentialSurrogatePair(t *testing.T) {
	e := NewJSONFieldExtractor("content")
	if got := e.Feed(`{"path":"report.txt","content":"Launch \ud83d`); got != "Launch " {
		t.Fatalf("high-surrogate prefix = %q, want only complete text", got)
	}
	if got := e.Feed(`\ud`); got != "" {
		t.Fatalf("partial low surrogate = %q", got)
	}
	if got := e.Feed(`e80"}`); got != "🚀" || !e.IsDone() {
		t.Fatalf("completed pair = %q, done=%t", got, e.IsDone())
	}
}

func TestSandboxWriteProgressRetainsJSONFieldValues(t *testing.T) {
	tests := []string{
		`{"path":"content","content":"Launch \ud83d\ude80\n"}`,
		`{"content":"path","path":"notes/report.txt"}`,
		`{"path":"normal.txt","content":"ordinary text\n"}`,
	}
	for _, arguments := range tests {
		t.Run(arguments, func(t *testing.T) {
			var want struct {
				Path    string `json:"path"`
				Content string `json:"content"`
			}
			if err := json.Unmarshal([]byte(arguments), &want); err != nil {
				t.Fatal(err)
			}
			progress := NewSandboxFileProgress("write_sandbox_file")
			payload, ok := progress.Feed(arguments)
			if !ok || payload["path"] != want.Path || payload["bytes"] != len(want.Content) {
				t.Fatalf("progress = %#v, emitted=%t; want path %q and %d bytes",
					payload, ok, want.Path, len(want.Content))
			}
		})
	}
}
