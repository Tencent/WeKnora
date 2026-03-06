package chat

import (
	"testing"
)

func TestJSONFieldExtractor_Basic(t *testing.T) {
	e := newJSONFieldExtractor("answer")

	// Simulate streaming JSON: {"answer":"Hello world"}
	got := ""
	got += e.Feed(`{"answer":"`)
	got += e.Feed(`Hello`)
	got += e.Feed(` world`)
	got += e.Feed(`"}`)

	if got != "Hello world" {
		t.Errorf("expected 'Hello world', got %q", got)
	}
	if !e.IsDone() {
		t.Error("expected extractor to be done")
	}
}

func TestJSONFieldExtractor_WithEscapes(t *testing.T) {
	e := newJSONFieldExtractor("answer")

	// Simulate: {"answer":"line1\nline2 and \"quoted\""}
	got := ""
	got += e.Feed(`{"answer":"line1\nline2`)
	got += e.Feed(` and \"quoted`)
	got += e.Feed(`\""}`)

	expected := "line1\nline2 and \"quoted\""
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestJSONFieldExtractor_OneChunk(t *testing.T) {
	e := newJSONFieldExtractor("answer")

	got := e.Feed(`{"answer":"complete answer here"}`)

	if got != "complete answer here" {
		t.Errorf("expected 'complete answer here', got %q", got)
	}
	if !e.IsDone() {
		t.Error("expected extractor to be done")
	}
}

func TestJSONFieldExtractor_SmallChunks(t *testing.T) {
	e := newJSONFieldExtractor("answer")

	// Very small chunks
	got := ""
	chunks := []string{`{`, `"`, `a`, `n`, `s`, `w`, `e`, `r`, `"`, `:`, `"`, `H`, `i`, `"`, `}`}
	for _, c := range chunks {
		got += e.Feed(c)
	}

	if got != "Hi" {
		t.Errorf("expected 'Hi', got %q", got)
	}
}

func TestJSONFieldExtractor_Markdown(t *testing.T) {
	e := newJSONFieldExtractor("answer")

	got := ""
	got += e.Feed(`{"answer":"# Title\n\n`)
	got += e.Feed(`This is **bold** and `)
	got += e.Feed(`*italic* text.\n\n`)
	got += e.Feed(`- item 1\n- item 2`)
	got += e.Feed(`"}`)

	expected := "# Title\n\nThis is **bold** and *italic* text.\n\n- item 1\n- item 2"
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestJSONFieldExtractor_UnicodeEscape(t *testing.T) {
	e := newJSONFieldExtractor("answer")

	got := ""
	got += e.Feed(`{"answer":"Hello \u4e16\u754c`)
	got += e.Feed(`"}`)

	expected := "Hello 世界"
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestJSONFieldExtractor_IncompleteEscapeAtBoundary(t *testing.T) {
	e := newJSONFieldExtractor("answer")

	// Escape sequence split across chunks
	got := ""
	got += e.Feed(`{"answer":"before\`)
	got += e.Feed(`nafter"}`)

	expected := "before\nafter"
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestJSONFieldExtractor_WhitespaceInJSON(t *testing.T) {
	e := newJSONFieldExtractor("answer")

	// Whitespace around colon
	got := e.Feed(`{ "answer" : "content here" }`)

	if got != "content here" {
		t.Errorf("expected 'content here', got %q", got)
	}
}

func TestJSONFieldExtractor_EmptyAnswer(t *testing.T) {
	e := newJSONFieldExtractor("answer")

	got := e.Feed(`{"answer":""}`)

	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
	if !e.IsDone() {
		t.Error("expected extractor to be done")
	}
}

// Test extracting "thought" field (for thinking tool)
func TestJSONFieldExtractor_ThoughtField(t *testing.T) {
	e := newJSONFieldExtractor("thought")

	got := ""
	got += e.Feed(`{"thought":"Let me analyze`)
	got += e.Feed(` the problem step by step`)
	got += e.Feed(`","next_thought_needed":true,"thought_number":1,"total_thoughts":3}`)

	expected := "Let me analyze the problem step by step"
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
	if !e.IsDone() {
		t.Error("expected extractor to be done")
	}
}

func TestJSONFieldExtractor_ThoughtFieldWithEscapes(t *testing.T) {
	e := newJSONFieldExtractor("thought")

	got := ""
	got += e.Feed(`{"thought":"Step 1:\n- Analyze the query\n- `)
	got += e.Feed(`Search for \"relevant\" info`)
	got += e.Feed(`","thought_number":1}`)

	expected := "Step 1:\n- Analyze the query\n- Search for \"relevant\" info"
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}
