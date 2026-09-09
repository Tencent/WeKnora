package agent

import (
	"strings"
	"testing"
)

func TestWikiChunkCitationPromptKeepsStablePrefixBeforeDynamicChunks(t *testing.T) {
	rules := strings.Index(WikiChunkCitationPrompt, "<instructions>")
	candidates := strings.Index(WikiChunkCitationPrompt, "{{.CandidateSlugs}}")
	chunks := strings.Index(WikiChunkCitationPrompt, "{{.ChunksXML}}")
	if rules < 0 || candidates < 0 || chunks < 0 {
		t.Fatal("wiki citation prompt markers are missing")
	}
	if !(rules < candidates && candidates < chunks) {
		t.Fatalf("cacheable prefix order changed: rules=%d candidates=%d chunks=%d", rules, candidates, chunks)
	}
}
