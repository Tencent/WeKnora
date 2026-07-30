package contentcache

import (
	"strings"
	"testing"
)

func TestStableChunkIDIsContentAddressedWithinKnowledge(t *testing.T) {
	input := ChunkIDInput{
		KnowledgeID:   "knowledge-1",
		ChunkType:     "text",
		Seq:           7,
		Occurrence:    1,
		Content:       "  hello\r\nworld  ",
		ContextHeader: "# Intro",
	}

	got := StableChunkID(input)
	again := StableChunkID(ChunkIDInput{
		KnowledgeID:   "knowledge-1",
		ChunkType:     "text",
		Seq:           7,
		Occurrence:    1,
		Content:       "hello\nworld",
		ContextHeader: "# Intro",
	})

	if got == "" {
		t.Fatal("StableChunkID returned empty id")
	}
	if len(got) != 36 {
		t.Fatalf("StableChunkID length = %d, want UUID length 36", len(got))
	}
	if got != again {
		t.Fatalf("stable id changed for equivalent normalized content: %q != %q", got, again)
	}
}

func TestStableChunkIDNormalizesUnicode(t *testing.T) {
	composed := ChunkIDInput{
		KnowledgeID: "knowledge-1",
		ChunkType:   "text",
		Occurrence:  1,
		Content:     "café",
	}
	decomposed := composed
	decomposed.Content = "cafe\u0301"

	if StableChunkID(composed) != StableChunkID(decomposed) {
		t.Fatal("StableChunkID must normalize canonically equivalent Unicode")
	}
	if TextHash(composed.Content) != TextHash(decomposed.Content) {
		t.Fatal("TextHash must normalize canonically equivalent Unicode")
	}
}

func TestStableChunkIDIgnoresSourceSequence(t *testing.T) {
	base := ChunkIDInput{
		KnowledgeID: "knowledge-1",
		ChunkType:   "text",
		Seq:         1,
		Occurrence:  1,
		Content:     "same text",
		ParentID:    "parent-a",
	}
	moved := base
	moved.Seq = 99
	if StableChunkID(base) != StableChunkID(moved) {
		t.Fatal("StableChunkID must survive unrelated insertions/reordering")
	}
}

func TestStableChunkIDDifferentiatesContentTypeOccurrenceAndParent(t *testing.T) {
	base := ChunkIDInput{
		KnowledgeID: "knowledge-1",
		ChunkType:   "text",
		Seq:         1,
		Occurrence:  1,
		Content:     "same text",
		ParentID:    "parent-a",
	}
	baseID := StableChunkID(base)

	cases := map[string]ChunkIDInput{
		"content": {
			KnowledgeID: "knowledge-1",
			ChunkType:   "text",
			Seq:         1,
			Occurrence:  1,
			Content:     "different text",
			ParentID:    "parent-a",
		},
		"type": {
			KnowledgeID: "knowledge-1",
			ChunkType:   "parent_text",
			Seq:         1,
			Occurrence:  1,
			Content:     "same text",
			ParentID:    "parent-a",
		},
		"occurrence": {
			KnowledgeID: "knowledge-1",
			ChunkType:   "text",
			Seq:         1,
			Occurrence:  2,
			Content:     "same text",
			ParentID:    "parent-a",
		},
		"parent": {
			KnowledgeID: "knowledge-1",
			ChunkType:   "text",
			Seq:         1,
			Occurrence:  1,
			Content:     "same text",
			ParentID:    "parent-b",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := StableChunkID(tc); got == baseID {
				t.Fatalf("StableChunkID did not change when %s changed", name)
			}
		})
	}
}

func TestChunkIdentityKeyUsesStructuredFields(t *testing.T) {
	a := ChunkIdentityKey(ChunkIDInput{
		KnowledgeID:   "k",
		ChunkType:     "text",
		ContextHeader: "a\x00b",
		Content:       "c",
	})
	b := ChunkIdentityKey(ChunkIDInput{
		KnowledgeID:   "k",
		ChunkType:     "text",
		ContextHeader: "a",
		Content:       "b\x00c",
	})
	if a == b {
		t.Fatal("ChunkIdentityKey must not collide when fields contain separators")
	}
}

func TestCacheKeysIncludeLayerInvalidationInputs(t *testing.T) {
	textHash := TextHash("  alpha\r\nbeta ")
	if textHash != TextHash("alpha\nbeta") {
		t.Fatal("TextHash should use normalized text")
	}

	embeddingA := EmbeddingKey(textHash, "embedder-a", 1536)
	embeddingB := EmbeddingKey(textHash, "embedder-a", 3072)
	if embeddingA == embeddingB {
		t.Fatal("EmbeddingKey must include embedding dimension")
	}

	vlmA := VLMKey(ImageHash([]byte("image bytes")), "vlm-a", "ocr-v1")
	vlmB := VLMKey(ImageHash([]byte("image bytes")), "vlm-a", "ocr-v2")
	if vlmA == vlmB {
		t.Fatal("VLMKey must include prompt version")
	}
	vlmC := VLMKey(ImageHash([]byte("different image bytes")), "vlm-a", "ocr-v1")
	if vlmA == vlmC {
		t.Fatal("VLMKey must include image bytes hash")
	}
	vlmD := VLMKey(ImageHash([]byte("image bytes")), "vlm-b", "ocr-v1")
	if vlmA == vlmD {
		t.Fatal("VLMKey must include model identity")
	}
	vlmSameBytesDifferentURL := VLMKey(ImageHash([]byte("image bytes")), "vlm-a", "ocr-v1")
	if vlmA != vlmSameBytesDifferentURL {
		t.Fatal("VLMKey should be driven by image bytes, not transient serving URL")
	}

	wikiA := WikiMapKey(TextHash("document"), "standard", "chat-a", "wiki-v1")
	wikiB := WikiMapKey(TextHash("document"), "focused", "chat-a", "wiki-v1")
	if wikiA == wikiB {
		t.Fatal("WikiMapKey must include extraction granularity")
	}

	postprocessA := PostprocessLLMKey(TextHash("payload"), "summary", "chat-a", "summary-v1")
	postprocessB := PostprocessLLMKey(TextHash("payload"), "question", "chat-a", "summary-v1")
	if postprocessA == postprocessB {
		t.Fatal("PostprocessLLMKey must include postprocess layer")
	}

	parseA := ParseArtifactKey(ImageHash([]byte("file")), "mineru", TextHash(`{"ocr":true}`))
	parseB := ParseArtifactKey(ImageHash([]byte("file")), "mineru", TextHash(`{"ocr":false}`))
	if parseA == parseB {
		t.Fatal("ParseArtifactKey must include render config")
	}

	graphA := GraphExtractKey(TextHash("chunk"), TextHash("cfg-a"), "chat-a", "graph-v1")
	graphB := GraphExtractKey(TextHash("chunk"), TextHash("cfg-b"), "chat-a", "graph-v1")
	if graphA == graphB {
		t.Fatal("GraphExtractKey must include extraction config")
	}

	for _, key := range []string{embeddingA, vlmA, wikiA, postprocessA, parseA, graphA} {
		if strings.Contains(key, " ") {
			t.Fatalf("cache key contains spaces: %q", key)
		}
	}
}
