package service

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCutLogPreviewCutsOnRuneBoundary(t *testing.T) {
	// 34 three-byte characters are 102 bytes, so the old s[:100] cut landed
	// inside the 34th character.
	caption := strings.Repeat("图", 34)
	if old := caption[:100] + "..."; utf8.ValidString(old) {
		t.Fatal("fixture no longer reproduces the old byte cut")
	}
	got := cutLogPreview(caption, 100)
	if !utf8.ValidString(got) {
		t.Fatalf("preview is not valid UTF-8: %q", got)
	}
	if want := strings.Repeat("图", 33) + "..."; got != want {
		t.Fatalf("preview = %q, want %q", got, want)
	}
}

func TestCutLogPreviewKeepsShortAndASCIIText(t *testing.T) {
	if got := cutLogPreview("短文本", 100); got != "短文本" {
		t.Fatalf("short preview = %q", got)
	}
	if got, want := cutLogPreview(strings.Repeat("a", 250), 200), strings.Repeat("a", 200)+"..."; got != want {
		t.Fatalf("len(preview) = %d, want %d", len(got), len(want))
	}
}
