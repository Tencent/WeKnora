package types

import "testing"

func TestCacheFingerprintStableForEquivalentPayload(t *testing.T) {
	a := CacheFingerprint("scope", map[string]any{
		"model": "embed-a",
		"dim":   1024,
	})
	b := CacheFingerprint("scope", map[string]any{
		"dim":   1024,
		"model": "embed-a",
	})
	if a == "" {
		t.Fatal("fingerprint is empty")
	}
	if a != b {
		t.Fatalf("expected stable fingerprint for equivalent payloads: %s != %s", a, b)
	}
}

func TestDocumentChunkReuseFingerprintInvalidatesEmbeddingInputs(t *testing.T) {
	base := DocumentChunkReuseFingerprint(
		"content",
		"Heading",
		ChunkTypeText,
		"embed-a",
		1024,
		"chunking-a",
		"Doc Title",
	)
	same := DocumentChunkReuseFingerprint(
		" content ",
		" Heading ",
		ChunkTypeText,
		"embed-a",
		1024,
		"chunking-a",
		"Doc Title",
	)
	if base != same {
		t.Fatalf("expected trimmed-equivalent chunk input to reuse fingerprint: %s != %s", base, same)
	}
	if got := DocumentChunkReuseFingerprint("changed", "Heading", ChunkTypeText, "embed-a", 1024, "chunking-a", "Doc Title"); got == base {
		t.Fatal("content changes must invalidate document chunk reuse")
	}
	if got := DocumentChunkReuseFingerprint("content", "Heading", ChunkTypeText, "embed-b", 1024, "chunking-a", "Doc Title"); got == base {
		t.Fatal("embedding model changes must invalidate document chunk reuse")
	}
	if got := DocumentChunkReuseFingerprint("content", "Heading", ChunkTypeText, "embed-a", 768, "chunking-a", "Doc Title"); got == base {
		t.Fatal("embedding dimension changes must invalidate document chunk reuse")
	}
	if got := DocumentChunkReuseFingerprint("content", "Heading", ChunkTypeText, "embed-a", 1024, "chunking-b", "Doc Title"); got == base {
		t.Fatal("chunking config changes must invalidate document chunk reuse")
	}
}
