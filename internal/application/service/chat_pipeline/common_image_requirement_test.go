package chatpipeline

import (
	"strings"
	"testing"
)

func TestAppendRetrievedImageOutputRequirement(t *testing.T) {
	base := "Answer from retrieved evidence."
	withImage := appendRetrievedImageOutputRequirement(
		base,
		"context\n![流程图](resource://AbCdEfGhIjKlMnOpQrStUv)",
	)
	for _, required := range []string{
		base,
		"MUST include at least one relevant Markdown image",
		"Copy the complete Markdown image syntax and its URL verbatim",
		"ASCII half-width parentheses",
		"immediately after the paragraph it supports",
	} {
		if !strings.Contains(withImage, required) {
			t.Fatalf("expected %q in dynamic image requirement:\n%s", required, withImage)
		}
	}

	withoutImage := appendRetrievedImageOutputRequirement(base, "text-only retrieved context")
	if withoutImage != base {
		t.Fatalf("text-only context should not change the system prompt: %q", withoutImage)
	}
}
