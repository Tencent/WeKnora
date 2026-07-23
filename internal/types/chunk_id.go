package types

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

const stableChunkIDNamespace = "0ed67a0f-6d4d-4c9a-99ac-4c880ee62e96"

// NormalizeForHash returns the canonical text used for document chunk hashing.
// It mirrors embedding input semantics by including the context header when
// present, while smoothing transport-only whitespace differences.
func NormalizeForHash(content, contextHeader string) string {
	content = normalizeHashText(content)
	contextHeader = normalizeHashText(contextHeader)
	if contextHeader == "" {
		return content
	}
	if content == "" {
		return contextHeader
	}
	return contextHeader + "\n\n" + content
}

func normalizeHashText(s string) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// ContentHash returns a 64-character SHA-256 hash for normalized chunk content.
func ContentHash(content, contextHeader string) string {
	normalized := NormalizeForHash(content, contextHeader)
	if normalized == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}

// StableChunkID derives a deterministic UUID-format chunk ID.
func StableChunkID(tenantID uint64, knowledgeID string, chunkType ChunkType, contentHash string, occurrenceIndex int) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		stableChunkIDNamespace,
		fmt.Sprintf("%d", tenantID),
		knowledgeID,
		string(chunkType),
		contentHash,
		fmt.Sprintf("%d", occurrenceIndex),
	}, "\x00")))
	return formatUUIDFromHash(sum)
}

func formatUUIDFromHash(sum [32]byte) string {
	id := sum[:16]
	id[6] = (id[6] & 0x0f) | 0x80
	id[8] = (id[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", id[0:4], id[4:6], id[6:8], id[8:10], id[10:16])
}

// ChunkIDAllocator assigns stable IDs while disambiguating duplicate content
// within one document by occurrence order.
type ChunkIDAllocator struct {
	tenantID    uint64
	knowledgeID string
	seen        map[string]int
}

func NewChunkIDAllocator(tenantID uint64, knowledgeID string) *ChunkIDAllocator {
	return &ChunkIDAllocator{
		tenantID:    tenantID,
		knowledgeID: knowledgeID,
		seen:        make(map[string]int),
	}
}

// Allocate returns the stable chunk ID and content hash for a document chunk.
func (a *ChunkIDAllocator) Allocate(chunkType ChunkType, content, contextHeader string) (string, string) {
	if a == nil {
		a = NewChunkIDAllocator(0, "")
	}
	contentHash := ContentHash(content, contextHeader)
	key := string(chunkType) + "\x00" + contentHash
	occurrenceIndex := a.seen[key]
	a.seen[key] = occurrenceIndex + 1
	return StableChunkID(a.tenantID, a.knowledgeID, chunkType, contentHash, occurrenceIndex), contentHash
}
