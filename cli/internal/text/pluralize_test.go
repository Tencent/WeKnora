package text_test

import (
	"testing"

	"github.com/Tencent/WeKnora/cli/internal/text"
)

func TestPluralize(t *testing.T) {
	tests := []struct {
		n     int
		thing string
		want  string
	}{
		{0, "doc", "0 docs"},
		{1, "doc", "1 doc"},
		{2, "doc", "2 docs"},
		{5, "chunk", "5 chunks"},
		{1, "chunk", "1 chunk"},
	}
	for _, tt := range tests {
		got := text.Pluralize(tt.n, tt.thing)
		if got != tt.want {
			t.Errorf("Pluralize(%d, %q) = %q, want %q", tt.n, tt.thing, got, tt.want)
		}
	}
}
