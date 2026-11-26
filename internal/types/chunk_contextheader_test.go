package types

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestChunkContextHeaderHiddenInJSON(t *testing.T) {
	c := Chunk{
		ID:            "test",
		Content:       "body",
		ContextHeader: "# Heading",
	}
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "Heading") || strings.Contains(string(b), "context_header") {
		t.Errorf("ContextHeader leaked into JSON: %s", string(b))
	}
}

func TestChunkJSONOmitsFAQAppendIndexPending(t *testing.T) {
	chunk := Chunk{Flags: ChunkFlagRecommended | ChunkFlagFAQAppendIndexPending}
	data, err := json.Marshal(chunk)
	if err != nil {
		t.Fatal(err)
	}
	var response Chunk
	if err := json.Unmarshal(data, &response); err != nil {
		t.Fatal(err)
	}
	if response.Flags != ChunkFlagRecommended || !chunk.Flags.HasFlag(ChunkFlagFAQAppendIndexPending) {
		t.Fatalf("expected public flags %d and an intact retry bit, got %d and %d",
			ChunkFlagRecommended, response.Flags, chunk.Flags)
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
