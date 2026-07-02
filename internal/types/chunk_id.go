package types

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
)

// stableChunkIDLen is the number of hex characters kept from the SHA-256 digest
// when building a content-addressed chunk ID. 32 hex chars = 128 bits, which is
// collision-safe for per-knowledge chunk sets and fits the chunks.id VARCHAR(36)
// column (UUIDs are 36 chars, so this is strictly shorter).
const stableChunkIDLen = 32

// NormalizeForHash canonicalizes chunk content so that semantically identical
// content produces an identical hash across re-parses. The normalization is
// intentionally conservative — it only removes differences that carry no
// meaning for retrieval — so that distinct content is never collapsed together:
//
//   - CRLF / CR line endings are converted to LF
//   - trailing horizontal whitespace on each line is stripped
//   - leading/trailing whitespace of the whole string is trimmed
//
// Internal whitespace inside a line is preserved (collapsing it could merge
// genuinely different content, e.g. code blocks).
func NormalizeForHash(content string) string {
	if content == "" {
		return ""
	}
	s := strings.ReplaceAll(content, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = strings.TrimRight(ln, " \t")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// ContentHash returns the hex-encoded SHA-256 of the normalized content. The
// result is 64 hex characters and fits the chunks.content_hash VARCHAR(64)
// column. It is the cache key used to decide whether a chunk's content is
// unchanged across a re-parse.
func ContentHash(content string) string {
	return sumHex(NormalizeForHash(content))
}

// StableChunkID derives a deterministic, content-addressed chunk ID from the
// owning knowledge, the chunk type, the (normalized) content and an occurrence
// counter. Identical content of the same type within the same knowledge yields
// the same ID on every parse, which is what lets the reparse path reuse already
// computed embeddings. The occurrence counter disambiguates genuinely duplicate
// content within one document so two identical paragraphs do not collide on the
// primary key.
func StableChunkID(knowledgeID string, chunkType ChunkType, content string, occurrence int) string {
	h := sha256.New()
	h.Write([]byte(knowledgeID))
	h.Write([]byte{0})
	h.Write([]byte(chunkType))
	h.Write([]byte{0})
	h.Write([]byte(strconv.Itoa(occurrence)))
	h.Write([]byte{0})
	h.Write([]byte(NormalizeForHash(content)))
	return hex.EncodeToString(h.Sum(nil))[:stableChunkIDLen]
}

// ChunkIDAllocator hands out content-addressed chunk IDs for a single knowledge
// item while tracking how many times each (type, normalized-content) pair has
// been seen, so that duplicate content gets distinct, still-deterministic IDs.
// It is not safe for concurrent use; build a knowledge's chunks on one goroutine.
type ChunkIDAllocator struct {
	knowledgeID string
	seen        map[string]int
}

// NewChunkIDAllocator creates an allocator scoped to one knowledge ID.
func NewChunkIDAllocator(knowledgeID string) *ChunkIDAllocator {
	return &ChunkIDAllocator{knowledgeID: knowledgeID, seen: make(map[string]int)}
}

// Allocate returns a deterministic, content-addressed ID for the given chunk
// type and content together with the content hash to persist on the chunk.
// Calling it again with identical (type, content) within the same allocator
// yields a different ID (the next occurrence), keeping primary keys unique while
// remaining stable across re-parses that see the same document.
func (a *ChunkIDAllocator) Allocate(chunkType ChunkType, content string) (id string, contentHash string) {
	norm := NormalizeForHash(content)
	key := string(chunkType) + "\x00" + norm
	occ := a.seen[key]
	a.seen[key]++
	id = StableChunkID(a.knowledgeID, chunkType, content, occ)
	contentHash = sumHex(norm)
	return id, contentHash
}

// sumHex returns the hex-encoded SHA-256 of an already-normalized string.
func sumHex(normalized string) string {
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}
