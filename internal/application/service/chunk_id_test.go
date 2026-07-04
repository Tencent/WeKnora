package service

import (
	"strings"
	"testing"
)

func TestNormalizeChunkContent(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"trim spaces", "  hello  ", "hello"},
		{"CRLF to LF", "line1\r\nline2\r\n", "line1\nline2"},
		{"empty", "", ""},
		{"only whitespace", "   \t\n  ", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeChunkContent(tt.input)
			if got != tt.want {
				t.Errorf("NormalizeChunkContent(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestComputeChunkStableID_Deterministic(t *testing.T) {
	docID := "knowledge-123"
	role := "text"
	seq := 5
	content := "This is a test chunk."

	id1 := ComputeChunkStableID(docID, role, seq, content)
	id2 := ComputeChunkStableID(docID, role, seq, content)

	if id1 != id2 {
		t.Errorf("same input should produce same ID: %s != %s", id1, id2)
	}
	if !strings.HasPrefix(id1, "ck_") {
		t.Errorf("ID should have ck_ prefix, got: %s", id1)
	}
	if len(id1) != 35 {
		t.Errorf("ID should be 35 chars (ck_ + 32 hex from 16 bytes), got %d: %s", len(id1), id1)
	}
}

func TestComputeChunkStableID_DifferentSeq(t *testing.T) {
	id1 := ComputeChunkStableID("doc1", "text", 1, "content")
	id2 := ComputeChunkStableID("doc1", "text", 2, "content")
	if id1 == id2 {
		t.Error("different seq should produce different IDs")
	}
}

func TestComputeChunkStableID_DifferentDocID(t *testing.T) {
	id1 := ComputeChunkStableID("doc1", "text", 1, "content")
	id2 := ComputeChunkStableID("doc2", "text", 1, "content")
	if id1 == id2 {
		t.Error("different docID should produce different IDs")
	}
}

func TestComputeChunkStableID_DifferentRole(t *testing.T) {
	id1 := ComputeChunkStableID("doc1", "text", 1, "content")
	id2 := ComputeChunkStableID("doc1", "parent", 1, "content")
	if id1 == id2 {
		t.Error("different role should produce different IDs")
	}
}

func TestComputeChunkStableID_Normalization(t *testing.T) {
	id1 := ComputeChunkStableID("doc1", "text", 1, "line1\r\nline2")
	id2 := ComputeChunkStableID("doc1", "text", 1, "line1\nline2")
	if id1 != id2 {
		t.Error("CRLF vs LF should produce same ID after normalization")
	}
}

func TestComputeChunkContentHash(t *testing.T) {
	hash1 := ComputeChunkContentHash("hello world")
	hash2 := ComputeChunkContentHash("hello world")
	if hash1 != hash2 {
		t.Error("same content should produce same hash")
	}
	if len(hash1) != 64 {
		t.Errorf("hash should be 64 hex chars, got %d: %s", len(hash1), hash1)
	}
}

func TestComputeChunkContentHash_DifferentContent(t *testing.T) {
	hash1 := ComputeChunkContentHash("hello")
	hash2 := ComputeChunkContentHash("world")
	if hash1 == hash2 {
		t.Error("different content should produce different hashes")
	}
}

func TestComputeChunkContentHash_Normalization(t *testing.T) {
	hash1 := ComputeChunkContentHash("  hello  ")
	hash2 := ComputeChunkContentHash("hello")
	if hash1 != hash2 {
		t.Error("trim space should produce same hash")
	}
}
