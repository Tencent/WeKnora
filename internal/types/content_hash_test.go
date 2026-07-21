package types

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
)

func TestHashString_Deterministic(t *testing.T) {
	a := HashString("hello")
	b := HashString("hello")
	if a != b {
		t.Fatalf("HashString not deterministic: %s != %s", a, b)
	}
}

func TestHashString_Different(t *testing.T) {
	a := HashString("hello")
	b := HashString("world")
	if a == b {
		t.Fatal("different inputs produced same hash")
	}
}

func TestHashBytes_MatchesHashString(t *testing.T) {
	data := []byte("test payload")
	bs := HashBytes(data)
	ss := HashString("test payload")
	if bs != ss {
		t.Fatalf("HashBytes != HashString for same content: %s != %s", bs, ss)
	}
}

func TestHashAll_OrderMatters(t *testing.T) {
	a := HashAll("a", "b")
	b := HashAll("b", "a")
	if a == b {
		t.Fatal("HashAll should be order-sensitive but identical values produced")
	}
}

func TestHashAll_PartBoundariesMatter(t *testing.T) {
	a := HashAll("a|b", "c")
	b := HashAll("a", "b|c")
	if a == b {
		t.Fatal("HashAll must preserve part boundaries")
	}
}

func TestHashSorted_OrderIndependent(t *testing.T) {
	a := HashSorted("b", "a", "c")
	b := HashSorted("a", "c", "b")
	if a != b {
		t.Fatalf("HashSorted should be order-independent: %s != %s", a, b)
	}
}

func TestStableChunkContentHash_IgnoresMarkdownImageDestination(t *testing.T) {
	a := StableChunkContentHash("before ![diagram](local://exports/a.png) after")
	b := StableChunkContentHash("before ![diagram](local://exports/b.png) after")
	if a != b {
		t.Fatal("regenerated image URLs must not change stable chunk content hash")
	}
}

func TestStableChunkID_Deterministic(t *testing.T) {
	contentHash := HashString("some chunk content")
	a := StableChunkID("knowledge-1", 3, ChunkTypeText, contentHash)
	b := StableChunkID("knowledge-1", 3, ChunkTypeText, contentHash)
	if a != b {
		t.Fatalf("StableChunkID not deterministic: %s != %s", a, b)
	}
}

func TestStableChunkID_DifferentKnowledge(t *testing.T) {
	ch := HashString("same content")
	a := StableChunkID("k1", 0, ChunkTypeText, ch)
	b := StableChunkID("k2", 0, ChunkTypeText, ch)
	if a == b {
		t.Fatal("different knowledge produced same chunk ID")
	}
}

func TestStableChunkID_DifferentIndex(t *testing.T) {
	ch := HashString("same content")
	a := StableChunkID("k1", 0, ChunkTypeText, ch)
	b := StableChunkID("k1", 1, ChunkTypeText, ch)
	if a == b {
		t.Fatal("different chunk index produced same ID")
	}
}

func TestStableChunkID_DifferentType(t *testing.T) {
	ch := HashString("same content")
	a := StableChunkID("k1", 0, ChunkTypeText, ch)
	b := StableChunkID("k1", 0, ChunkTypeSummary, ch)
	if a == b {
		t.Fatal("different chunk type produced same ID")
	}
}

func TestStableChunkID_Format_Length(t *testing.T) {
	ch := HashString("content")
	id := StableChunkID("knowledge-abc", 5, ChunkTypeParentText, ch)
	if len(id) > 36 {
		t.Fatalf("StableChunkID longer than 36 chars: %d", len(id))
	}
	if len(id) == 0 {
		t.Fatal("StableChunkID empty")
	}
}

func TestStableChunkID_Format_HexOnly(t *testing.T) {
	ch := HashString("content")
	id := StableChunkID("kb-1", 2, ChunkTypeText, ch)
	// Should be lowercase hex (from SHA-256)
	for _, r := range id {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			t.Fatalf("StableChunkID contains non-hex character: %c in %s", r, id)
		}
	}
}

func TestStableQuestionID_Deterministic(t *testing.T) {
	a := StableQuestionID("What is the capital of France?")
	b := StableQuestionID("What is the capital of France?")
	if a != b {
		t.Fatalf("StableQuestionID not deterministic: %s != %s", a, b)
	}
}

func TestStableQuestionID_Different(t *testing.T) {
	a := StableQuestionID("What is X?")
	b := StableQuestionID("What is Y?")
	if a == b {
		t.Fatal("different questions produced same ID")
	}
}

func TestStableQuestionID_Format(t *testing.T) {
	id := StableQuestionID("test question")
	if len(id) == 0 {
		t.Fatal("StableQuestionID empty")
	}
	if id[0] != 'q' {
		t.Fatalf("StableQuestionID should start with 'q': %s", id)
	}
	if len(id) > 24 {
		t.Fatalf("StableQuestionID longer than 24 chars: %d", len(id))
	}
}

func TestImageChunkStableID_Deterministic(t *testing.T) {
	a := ImageChunkStableID("parent-1", "local://images/img.png", ChunkTypeImageOCR)
	b := ImageChunkStableID("parent-1", "local://images/img.png", ChunkTypeImageOCR)
	if a != b {
		t.Fatalf("ImageChunkStableID not deterministic: %s != %s", a, b)
	}
}

func TestImageChunkStableID_DifferentImageHash(t *testing.T) {
	a := ImageChunkStableID("p1", HashString("image-a"), ChunkTypeImageOCR)
	b := ImageChunkStableID("p1", HashString("image-b"), ChunkTypeImageOCR)
	if a == b {
		t.Fatal("different image hashes produced same ID")
	}
}

func TestImageChunkStableID_DifferentType(t *testing.T) {
	a := ImageChunkStableID("p1", HashString("image-a"), ChunkTypeImageOCR)
	b := ImageChunkStableID("p1", HashString("image-a"), ChunkTypeImageCaption)
	if a == b {
		t.Fatal("different chunk type produced same ID")
	}
}

// TestHashString_CollisionResistance verifies no collisions across 100k
// unique random-ish inputs, all truncated like StableChunkID.  SHA-256 is
// collision-resistant by design; this test guards against a silly bug
// (e.g. accidentally calling hex.EncodeToString twice).
func TestHashString_CollisionResistance(t *testing.T) {
	seen := make(map[string]string, 100000)
	for i := 0; i < 100000; i++ {
		input := fmt.Sprintf("chunk|kb-%d|%d|text|%x", i%1000, i, sha256.Sum256([]byte(fmt.Sprintf("payload-%d", i))))
		h := HashString(input)
		fullHash := hex.EncodeToString(sha256.New().Sum([]byte(input)))
		_ = fullHash
		trunc := h
		if len(trunc) > 36 {
			trunc = trunc[:36]
		}
		if prev, ok := seen[trunc]; ok {
			t.Fatalf("truncated hash collision: %q and %q both map to %s", prev, input, trunc)
		}
		seen[trunc] = input
	}
}

func TestEmbeddingInputHash_Deterministic(t *testing.T) {
	a := EmbeddingInputHash("Title\n", "## Section", "hello world")
	b := EmbeddingInputHash("Title\n", "## Section", "hello world")
	if a != b {
		t.Fatalf("EmbeddingInputHash not deterministic: %s != %s", a, b)
	}
}

func TestEmbeddingInputHash_TrimsContent(t *testing.T) {
	a := EmbeddingInputHash("", "", "  hello  ")
	b := EmbeddingInputHash("", "", "hello")
	if a != b {
		t.Fatalf("EmbeddingInputHash should trim surrounding whitespace: %s != %s", a, b)
	}
}
