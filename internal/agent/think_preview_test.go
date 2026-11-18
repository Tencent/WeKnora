package agent

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCutDebugPreviewCutsOnRuneBoundary(t *testing.T) {
	// 34 three-byte characters are 102 bytes, so the old s[:100] cut landed
	// inside the 34th character.
	content := strings.Repeat("答", 34)
	if old := content[:100] + "..."; utf8.ValidString(old) {
		t.Fatal("fixture no longer reproduces the old byte cut")
	}

	got := cutDebugPreview(content, 100)
	if !utf8.ValidString(got) {
		t.Fatalf("cutDebugPreview returned invalid UTF-8: %q", got)
	}
	if want := strings.Repeat("答", 33) + "..."; got != want {
		t.Fatalf("cutDebugPreview = %q, want %q", got, want)
	}
}

func TestCutDebugPreviewKeepsShortAndASCIIText(t *testing.T) {
	if got := cutDebugPreview("短回答", 300); got != "短回答" {
		t.Fatalf("short text changed: %q", got)
	}
	ascii := strings.Repeat("a", 301)
	if got := cutDebugPreview(ascii, 300); got != strings.Repeat("a", 300)+"..." {
		t.Fatalf("ASCII cut = %q", got)
	}
}
