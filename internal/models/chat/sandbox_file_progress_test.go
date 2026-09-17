package chat

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestCountContentLines(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"hello", 1},
		{"hello\n", 1},
		{"hello\nworld", 2},
		{"hello\nworld\n", 2},
		{"a\n\nb", 3},
	}
	for _, tc := range cases {
		if got := countContentLines(tc.in); got != tc.want {
			t.Errorf("countContentLines(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestSandboxFileProgressWriteStreamsLineCount(t *testing.T) {
	p := newSandboxFileProgress(writeSandboxFileName)

	if _, ok := p.Feed(`{"path":"`); ok {
		t.Fatal("path still open, should not emit yet")
	}
	payload, ok := p.Feed(`/workspace/output/deck.py","content":"`)
	if !ok {
		t.Fatal("expected emit once path is complete")
	}
	if payload["path"] != "/workspace/output/deck.py" {
		t.Fatalf("path = %v", payload["path"])
	}
	if payload["added_lines"] != 0 {
		t.Fatalf("no content yet, added_lines = %v", payload["added_lines"])
	}

	payload, ok = p.Feed(`line1\nline2\nline3`)
	if !ok {
		t.Fatal("expected emit when line count grows")
	}
	if payload["added_lines"] != 3 {
		t.Fatalf("added_lines = %v, want 3", payload["added_lines"])
	}
	preview, _ := payload["preview"].(string)
	if !strings.Contains(preview, "line1") || !strings.Contains(preview, "line3") {
		t.Fatalf("preview = %q", preview)
	}
	if _, exists := payload["content"]; exists {
		t.Fatal("progress payload must not carry the file body")
	}
}

func TestSandboxFileProgressWritePreviewCapsAtTenLines(t *testing.T) {
	p := newSandboxFileProgress(writeSandboxFileName)
	var body strings.Builder
	body.WriteString(`{"path":"/workspace/a.py","content":"`)
	for i := 0; i < 15; i++ {
		if i > 0 {
			body.WriteString(`\n`)
		}
		body.WriteString("x")
	}
	body.WriteString(`"}`)
	payload, ok := p.Feed(body.String())
	if !ok {
		t.Fatal("expected emit")
	}
	if payload["added_lines"] != 15 {
		t.Fatalf("added_lines = %v, want 15", payload["added_lines"])
	}
	preview, _ := payload["preview"].(string)
	if strings.Count(preview, "\n")+1 != sandboxFilePreviewMaxLines {
		t.Fatalf("preview lines = %q", preview)
	}
}

func TestSandboxFileProgressEditCountsOldAndNew(t *testing.T) {
	p := newSandboxFileProgress(editSandboxFileName)
	payload, ok := p.Feed(
		`{"path":"/workspace/a.py","edits":[{"old_string":"a\nb\n","new_string":"a\nb\nc\nd\n"}]}`,
	)
	if !ok {
		t.Fatal("expected emit for complete edit JSON")
	}
	if payload["path"] != "/workspace/a.py" {
		t.Fatalf("path = %v", payload["path"])
	}
	if payload["removed_lines"] != 2 {
		t.Fatalf("removed_lines = %v, want 2", payload["removed_lines"])
	}
	if payload["added_lines"] != 4 {
		t.Fatalf("added_lines = %v, want 4", payload["added_lines"])
	}
}

func TestSandboxFileProgressEditPartialJSON(t *testing.T) {
	p := newSandboxFileProgress(editSandboxFileName)
	sandboxProgressMinInterval = 0
	t.Cleanup(func() { sandboxProgressMinInterval = 120 * time.Millisecond })

	payload, ok := p.Feed(`{"path":"/workspace/a.py","edits":[{"old_string":"foo","new_string":"foo\nbar\nbaz`)
	if !ok {
		t.Fatal("expected emit for closable partial JSON")
	}
	if payload["path"] != "/workspace/a.py" {
		t.Fatalf("path = %v", payload["path"])
	}
	added, _ := payload["added_lines"].(int)
	if added < 2 {
		t.Fatalf("added_lines = %v, want at least 2 from the partial new_string", payload["added_lines"])
	}
}

func TestClosePartialJSON(t *testing.T) {
	closed := closePartialJSON(`{"path":"/x","content":"hello`)
	var m map[string]any
	if err := json.Unmarshal([]byte(closed), &m); err != nil {
		t.Fatalf("closed JSON should parse: %v (%s)", err, closed)
	}
	if m["content"] != "hello" {
		t.Fatalf("content = %v", m["content"])
	}
}
