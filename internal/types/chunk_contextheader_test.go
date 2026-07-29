package types

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestChunkContextHeaderHiddenInJSON(t *testing.T) {
	c := Chunk{
		ID:              "test",
		Content:         "body",
		ContextHeader:   "# Heading",
		StableIdentity:  "stable-id",
		IdentityVersion: "identity-v1",
	}
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	jsonText := string(b)
	for _, privateValue := range []string{
		"Heading",
		"context_header",
		"stable-id",
		"stable_identity",
		"identity-v1",
		"identity_version",
	} {
		if strings.Contains(jsonText, privateValue) {
			t.Errorf("internal chunk identity data leaked into JSON: %s", jsonText)
		}
	}
}

func TestParsedChunkEmbeddingContent(t *testing.T) {
	pc := ParsedChunk{Content: "body", ContextHeader: "# H"}
	got := pc.EmbeddingContent()
	want := "# H\n\nbody"
	if got != want {
		t.Errorf("EmbeddingContent mismatch: got %q, want %q", got, want)
	}
	pc2 := ParsedChunk{Content: "only body"}
	if pc2.EmbeddingContent() != "only body" {
		t.Errorf("EmbeddingContent without header should equal Content")
	}
}
