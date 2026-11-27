package text_test

import (
	"testing"

	"github.com/Tencent/WeKnora/cli/internal/text"
)

func TestContainsFold(t *testing.T) {
	tests := []struct {
		name        string
		lowerNeedle string
		fields      []string
		want        bool
	}{
		{"match case-insensitively", "hello", []string{"say Hello world"}, true},
		{"match second field", "b", []string{"a", "B"}, true},
		{"no match", "xyz", []string{"abc", "def"}, false},
		{"no fields", "x", nil, false},
		{"empty needle matches any field", "", []string{"abc"}, true},
		{"empty needle with no fields", "", nil, false},
		// The caller passes the needle in lowercase form (see the doc
		// comment on ContainsFold); an uppercase needle does not match.
		{"uppercase needle does not match", "HELLO", []string{"hello"}, false},
		{"empty field does not match non-empty needle", "x", []string{""}, false},
	}
	for _, tt := range tests {
		if got := text.ContainsFold(tt.lowerNeedle, tt.fields...); got != tt.want {
			t.Errorf("ContainsFold(%q, %q) = %v, want %v", tt.lowerNeedle, tt.fields, got, tt.want)
		}
	}
}

func TestKnowledgeDisplayName(t *testing.T) {
	tests := []struct {
		name     string
		fileName string
		title    string
		id       string
		want     string
	}{
		{"file name wins", "report.pdf", "Quarterly Report", "kb-1", "report.pdf"},
		{"title is the fallback", "", "Quarterly Report", "kb-1", "Quarterly Report"},
		{"id is the last resort", "", "", "kb-1", "kb-1"},
		{"all empty stays empty", "", "", "", ""},
	}
	for _, tt := range tests {
		if got := text.KnowledgeDisplayName(tt.fileName, tt.title, tt.id); got != tt.want {
			t.Errorf("KnowledgeDisplayName(%q, %q, %q) = %q, want %q", tt.fileName, tt.title, tt.id, got, tt.want)
		}
	}
}
