package chunker

import (
	"strings"
	"testing"
)

func TestHeadingHierarchyPreservesLiteralTrailingHashes(t *testing.T) {
	for _, tc := range []struct{ line, want string }{
		{"# C#", "C#"},
		{"## F#  ", "F#"},
		{"# topic###", "topic###"},
		{`# topic \#`, `topic \#`},
		{"# C# ###", "C#"},
		{"# Topic\t###\t", "Topic"},
		{"# Topic ### more", "Topic ### more"},
	} {
		t.Run(tc.line, func(t *testing.T) {
			h := NewHeadingHierarchy()
			_, got := h.Observe(tc.line)
			if got != tc.want {
				t.Fatalf("heading = %q, want %q", got, tc.want)
			}
			if h.Breadcrumb() != tc.want {
				t.Fatalf("breadcrumb = %q", h.Breadcrumb())
			}
		})
	}
}

func TestHeadingSplitterPreservesCSharpContext(t *testing.T) {
	text := "# C#\n" + strings.Repeat("CSharp reference content. ", 50) +
		"\n# F#\n" + strings.Repeat("FSharp reference content. ", 50)
	cfg := DefaultConfig()
	cfg.ChunkSize = 400
	cfg.ChunkOverlap = 0
	chunks := splitByHeadingsImpl(text, cfg, nil)
	found := false
	for _, chunk := range chunks {
		if strings.Contains(chunk.Content, "CSharp") {
			found = true
			if chunk.ContextHeader != "# C#" {
				t.Fatalf("CSharp context = %q", chunk.ContextHeader)
			}
		}
	}
	if !found {
		t.Fatal("no CSharp chunks produced")
	}
}
