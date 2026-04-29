package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestParseOutLinks(t *testing.T) {
	svc := &wikiPageService{}

	tests := []struct {
		name    string
		content string
		want    []string
	}{
		{
			name:    "single link",
			content: "See [[entity/acme-corp]] for details.",
			want:    []string{"entity/acme-corp"},
		},
		{
			name:    "multiple links",
			content: "See [[entity/acme-corp]] and [[concept/rag]] for details.",
			want:    []string{"entity/acme-corp", "concept/rag"},
		},
		{
			name:    "duplicate links deduplicated",
			content: "See [[entity/acme-corp]] and also [[entity/acme-corp]] again.",
			want:    []string{"entity/acme-corp"},
		},
		{
			name:    "pipe syntax: slug|display name",
			content: "See [[entity/acme-corp|Acme Corp]] for details.",
			want:    []string{"entity/acme-corp"},
		},
		{
			name:    "mixed: pipe and bare links",
			content: "See [[entity/acme-corp|Acme Corp]] and [[concept/rag]] here.",
			want:    []string{"entity/acme-corp", "concept/rag"},
		},
		{
			name:    "no links",
			content: "Just plain text without any links.",
			want:    nil,
		},
		{
			name:    "empty content",
			content: "",
			want:    nil,
		},
		{
			name:    "link with spaces normalized",
			content: "See [[Entity/Acme Corp]] for details.",
			want:    []string{"entity/acme-corp"},
		},
		{
			name:    "nested brackets ignored",
			content: "Not a link: [not [a] link]",
			want:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := svc.parseOutLinks(tt.content)
			if len(got) != len(tt.want) {
				t.Errorf("parseOutLinks() = %v (len %d), want %v (len %d)", got, len(got), tt.want, len(tt.want))
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("parseOutLinks()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestNormalizeSlug(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Entity/Acme Corp", "entity/acme-corp"},
		{"  hello  ", "hello"},
		{"UPPER-CASE", "upper-case"},
		{"already-ok", "already-ok"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := normalizeSlug(tt.input)
			if got != tt.want {
				t.Errorf("normalizeSlug(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestContainsString(t *testing.T) {
	slice := []string{"a", "b", "c"}
	if !containsString(slice, "b") {
		t.Error("should contain 'b'")
	}
	if containsString(slice, "d") {
		t.Error("should not contain 'd'")
	}
	if containsString(nil, "a") {
		t.Error("nil slice should not contain anything")
	}
}

func TestRemoveString(t *testing.T) {
	slice := types.StringArray{"a", "b", "c", "b"}
	result := removeString(slice, "b")
	if len(result) != 2 {
		t.Errorf("Expected 2 items after removing 'b', got %d: %v", len(result), result)
	}
	if result[0] != "a" || result[1] != "c" {
		t.Errorf("Unexpected result: %v", result)
	}

	// Remove non-existing
	result2 := removeString(slice, "z")
	if len(result2) != 4 {
		t.Errorf("Expected 4 items (nothing removed), got %d", len(result2))
	}
}
