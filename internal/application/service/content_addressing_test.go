package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestNormalizeForHash(t *testing.T) {
	if got := NormalizeForHash(" \r\n hello\rworld \n "); got != "hello\nworld" {
		t.Fatalf("NormalizeForHash() = %q", got)
	}
}

func TestChunkIDAllocatorIsDeterministic(t *testing.T) {
	first := NewChunkIDAllocator(7, "knowledge-1")
	id1, hash1 := first.StableChunkID(types.ChunkTypeText, "same", "", "text:1")
	id2, hash2 := first.StableChunkID(types.ChunkTypeText, "same", "", "text:2")

	second := NewChunkIDAllocator(7, "knowledge-1")
	id1Again, hash1Again := second.StableChunkID(types.ChunkTypeText, "same", "", "text:1")
	id2Again, hash2Again := second.StableChunkID(types.ChunkTypeText, "same", "", "text:2")

	if id1 != id1Again || id2 != id2Again || hash1 != hash1Again || hash2 != hash2Again {
		t.Fatalf("stable allocator changed across runs: (%s,%s) (%s,%s) vs (%s,%s) (%s,%s)",
			id1, hash1, id2, hash2, id1Again, hash1Again, id2Again, hash2Again)
	}
	if id1 == id2 {
		t.Fatal("different stable positions must not collide")
	}
}

func TestStableDerivedChunkIDChangesWithContent(t *testing.T) {
	id1, hash1 := StableDerivedChunkID(7, "knowledge-1", types.ChunkTypeImageOCR, "parent-1", "before")
	id2, hash2 := StableDerivedChunkID(7, "knowledge-1", types.ChunkTypeImageOCR, "parent-1", "after")
	if id1 == id2 || hash1 == hash2 {
		t.Fatal("derived chunk identity must change when content changes")
	}
}
