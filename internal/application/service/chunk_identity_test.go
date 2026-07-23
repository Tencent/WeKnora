package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
)

func TestStableChunkIDDeterministic(t *testing.T) {
	a := stableChunkID("knowledge-1", types.ChunkTypeText, 3, "hello world")
	b := stableChunkID("knowledge-1", types.ChunkTypeText, 3, "hello world")
	if a != b {
		t.Fatalf("same inputs must derive the same chunk ID: %s vs %s", a, b)
	}
}

func TestStableChunkIDIsValidUUID(t *testing.T) {
	id := stableChunkID("knowledge-1", types.ChunkTypeText, 0, "content")
	parsed, err := uuid.Parse(id)
	if err != nil {
		t.Fatalf("stable chunk ID must be a valid UUID, got %q: %v", id, err)
	}
	if parsed.Version() != 5 {
		t.Fatalf("expected UUIDv5 (SHA-1 name-based), got v%d", parsed.Version())
	}
}

func TestStableChunkIDChangesWithAnyIdentityPart(t *testing.T) {
	base := stableChunkID("knowledge-1", types.ChunkTypeText, 3, "hello world")

	if got := stableChunkID("knowledge-2", types.ChunkTypeText, 3, "hello world"); got == base {
		t.Fatal("different knowledge ID must derive a different chunk ID")
	}
	if got := stableChunkID("knowledge-1", types.ChunkTypeParentText, 3, "hello world"); got == base {
		t.Fatal("different chunk type must derive a different chunk ID")
	}
	if got := stableChunkID("knowledge-1", types.ChunkTypeText, 4, "hello world"); got == base {
		t.Fatal("different sequence must derive a different chunk ID")
	}
	if got := stableChunkID("knowledge-1", types.ChunkTypeText, 3, "hello world!"); got == base {
		t.Fatal("different content must derive a different chunk ID")
	}
}

func TestStableChunkIDNormalizesContentNoise(t *testing.T) {
	base := stableChunkID("k", types.ChunkTypeText, 1, "line one\nline two")

	// CRLF vs LF must not shift the ID between rebuilds.
	if got := stableChunkID("k", types.ChunkTypeText, 1, "line one\r\nline two"); got != base {
		t.Fatal("CRLF/LF difference must not change the chunk ID")
	}
	// Leading/trailing whitespace must not shift the ID.
	if got := stableChunkID("k", types.ChunkTypeText, 1, "  line one\nline two \n"); got != base {
		t.Fatal("surrounding whitespace must not change the chunk ID")
	}
	// Interior whitespace IS meaningful.
	if got := stableChunkID("k", types.ChunkTypeText, 1, "line  one\nline two"); got == base {
		t.Fatal("interior whitespace change must change the chunk ID")
	}
}

func TestStableChunkIDIdenticalContentDistinctSeqs(t *testing.T) {
	// Two chunks with identical content at different positions must not
	// collide (PK safety within one knowledge).
	a := stableChunkID("k", types.ChunkTypeText, 1, "repeated paragraph")
	b := stableChunkID("k", types.ChunkTypeText, 2, "repeated paragraph")
	if a == b {
		t.Fatal("identical content at different seq must derive distinct IDs")
	}
}
