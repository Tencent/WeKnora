package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestStableChunkID(t *testing.T) {
	knowledgeID := "test-kb-001"
	seq := 1
	content := "This is a test chunk content."

	id1 := StableChunkID(knowledgeID, seq, content)
	id2 := StableChunkID(knowledgeID, seq, content)

	if id1 != id2 {
		t.Errorf("Same input should produce same ID: %s != %s", id1, id2)
	}
	t.Logf("Stable ID: %s", id1)
}

func TestStableChunkID_DifferentContent(t *testing.T) {
	knowledgeID := "test-kb-001"
	seq := 1

	id1 := StableChunkID(knowledgeID, seq, "content A")
	id2 := StableChunkID(knowledgeID, seq, "content B")

	if id1 == id2 {
		t.Errorf("Different content should produce different IDs: %s == %s", id1, id2)
	}
}

func TestStableChunkID_DifferentKnowledge(t *testing.T) {
	seq := 1
	content := "same content"

	id1 := StableChunkID("kb-001", seq, content)
	id2 := StableChunkID("kb-002", seq, content)

	if id1 == id2 {
		t.Errorf("Different knowledge ID should produce different chunk IDs")
	}
}

func TestStableChunkID_DifferentSeq(t *testing.T) {
	knowledgeID := "test-kb-001"
	content := "same content"

	id1 := StableChunkID(knowledgeID, 0, content)
	id2 := StableChunkID(knowledgeID, 1, content)

	if id1 == id2 {
		t.Errorf("Different seq should produce different chunk IDs")
	}
}

func TestNormalizeChunkContent(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"trailing spaces", "hello world   ", "hello world"},
		{"leading spaces", "   hello world", "hello world"},
		{"empty string", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NormalizeChunkContent(tt.input)
			if result != tt.expected {
				t.Errorf("NormalizeChunkContent(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestComputeContentHash(t *testing.T) {
	knowledgeID := "test-kb-001"
	seq := 1
	content := "test content"

	hash1 := ComputeContentHash(knowledgeID, seq, content)
	hash2 := ComputeContentHash(knowledgeID, seq, content)

	if hash1 != hash2 {
		t.Errorf("Same input should produce same hash")
	}

	hash3 := ComputeContentHash(knowledgeID, seq, "different content")
	if hash1 == hash3 {
		t.Errorf("Different content should produce different hash")
	}
	t.Logf("Content hash: %s", hash1)
}

func makeChunk(id string, idx int, hash string) *types.Chunk {
	return &types.Chunk{
		ID:           id,
		ChunkIndex:   idx,
		ContentHash:  hash,
	}
}

func TestDiffChunks_AllUnchanged(t *testing.T) {
	oldChunks := []*types.Chunk{
		makeChunk("chunk-1", 0, "hash-1"),
		makeChunk("chunk-2", 1, "hash-2"),
	}
	newChunks := []*types.Chunk{
		makeChunk("chunk-1", 0, "hash-1"),
		makeChunk("chunk-2", 1, "hash-2"),
	}

	result := DiffChunks(oldChunks, newChunks)

	if len(result.Unchanged) != 2 {
		t.Errorf("Expected 2 unchanged, got %d", len(result.Unchanged))
	}
	if len(result.Added) != 0 {
		t.Errorf("Expected 0 added, got %d", len(result.Added))
	}
	if len(result.RemovedIDs) != 0 {
		t.Errorf("Expected 0 removed, got %d", len(result.RemovedIDs))
	}
	if len(result.Changed) != 0 {
		t.Errorf("Expected 0 changed, got %d", len(result.Changed))
	}
}

func TestDiffChunks_AllChanged(t *testing.T) {
	oldChunks := []*types.Chunk{
		makeChunk("chunk-1", 0, "hash-1"),
		makeChunk("chunk-2", 1, "hash-2"),
	}
	newChunks := []*types.Chunk{
		makeChunk("chunk-1", 0, "hash-1-new"),
		makeChunk("chunk-2", 1, "hash-2-new"),
	}

	result := DiffChunks(oldChunks, newChunks)

	if len(result.Unchanged) != 0 {
		t.Errorf("Expected 0 unchanged, got %d", len(result.Unchanged))
	}
	if len(result.Changed) != 2 {
		t.Errorf("Expected 2 changed, got %d", len(result.Changed))
	}
}

func TestDiffChunks_Mixed(t *testing.T) {
	oldChunks := []*types.Chunk{
		makeChunk("chunk-1", 0, "hash-1"), // unchanged
		makeChunk("chunk-2", 1, "hash-2"), // changed
		makeChunk("chunk-3", 2, "hash-3"), // removed
	}
	newChunks := []*types.Chunk{
		makeChunk("chunk-1", 0, "hash-1"),      // unchanged
		makeChunk("chunk-2", 1, "hash-2-new"),  // changed
		makeChunk("chunk-4", 3, "hash-4"),      // added
	}

	result := DiffChunks(oldChunks, newChunks)

	if len(result.Unchanged) != 1 {
		t.Errorf("Expected 1 unchanged, got %d", len(result.Unchanged))
	}
	if len(result.Added) != 1 {
		t.Errorf("Expected 1 added, got %d", len(result.Added))
	}
	if len(result.RemovedIDs) != 2 {
		t.Errorf("Expected 2 removed (1 changed + 1 removed), got %d", len(result.RemovedIDs))
	}
	if len(result.Changed) != 1 {
		t.Errorf("Expected 1 changed, got %d", len(result.Changed))
	}

	if result.Unchanged[0].ID != "chunk-1" {
		t.Errorf("Expected unchanged to be chunk-1, got %s", result.Unchanged[0].ID)
	}
	if result.Added[0].ID != "chunk-4" {
		t.Errorf("Expected added to be chunk-4, got %s", result.Added[0].ID)
	}
}

func TestDiffChunks_EmptyOld(t *testing.T) {
	oldChunks := []*types.Chunk{}
	newChunks := []*types.Chunk{
		makeChunk("chunk-1", 0, "hash-1"),
		makeChunk("chunk-2", 1, "hash-2"),
	}

	result := DiffChunks(oldChunks, newChunks)

	if len(result.Added) != 2 {
		t.Errorf("Expected 2 added, got %d", len(result.Added))
	}
	if len(result.Unchanged) != 0 {
		t.Errorf("Expected 0 unchanged, got %d", len(result.Unchanged))
	}
}

func TestDiffChunks_EmptyNew(t *testing.T) {
	oldChunks := []*types.Chunk{
		makeChunk("chunk-1", 0, "hash-1"),
		makeChunk("chunk-2", 1, "hash-2"),
	}
	newChunks := []*types.Chunk{}

	result := DiffChunks(oldChunks, newChunks)

	if len(result.RemovedIDs) != 2 {
		t.Errorf("Expected 2 removed, got %d", len(result.RemovedIDs))
	}
	if len(result.Added) != 0 {
		t.Errorf("Expected 0 added, got %d", len(result.Added))
	}
}

func TestDiffChunks_UnchangedGetsOldID(t *testing.T) {
	oldChunks := []*types.Chunk{
		makeChunk("old-id-1", 0, "hash-1"),
	}
	// New chunk has different ID but same content hash
	newChunks := []*types.Chunk{
		makeChunk("new-id-1", 0, "hash-1"),
	}

	result := DiffChunks(oldChunks, newChunks)

	if len(result.Unchanged) != 1 {
		t.Fatalf("Expected 1 unchanged, got %d", len(result.Unchanged))
	}
	// The unchanged chunk should get the old ID assigned
	if result.Unchanged[0].ID != "old-id-1" {
		t.Errorf("Expected unchanged chunk to keep old ID 'old-id-1', got '%s'", result.Unchanged[0].ID)
	}
}
