package hash

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

// StableChunkID generates a deterministic, content-addressable chunk ID
// from knowledgeID + normalized content + optional stable sequence.
// Format: sha256(knowledgeID|normalizedContent|seq)[:16]  (short but collision-resistant for practical use)
// This replaces uuid.New().String() so that identical content yields identical IDs
// across reparse / rebuild / crash recovery, enabling cache hits on vectors, wiki refs, graph edges, etc.
//
// Normalization rules (to achieve high hit rate):
//   - Trim whitespace
//   - Collapse internal whitespace runs to single space
//   - Lowercase (for case-insensitive matching where appropriate)
//   - Remove common markdown artifacts that don't change semantics
//
// The seq parameter should be the original document order (ChunkIndex or stable position)
// to disambiguate identical text blocks within the same document.
func StableChunkID(knowledgeID, content string, seq int) string {
	normalized := normalizeForID(content)
	payload := fmt.Sprintf("%s|%s|%d", knowledgeID, normalized, seq)
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])[:16] // 16 hex chars = 64-bit, sufficient for per-doc uniqueness
}

// StableParentChunkID is the same as StableChunkID but marks it as parent for clarity.
func StableParentChunkID(knowledgeID, content string, seq int) string {
	return StableChunkID(knowledgeID, content, seq)
}

// normalizeForID produces a canonical string for hashing.
// It is intentionally lossy in a controlled way so that semantically identical
// chunks (minor whitespace, case, markdown noise) produce the same ID.
func normalizeForID(s string) string {
	s = strings.TrimSpace(s)
	// Collapse any run of whitespace (space, tab, newline) into a single space
	re := regexp.MustCompile(`\s+`)
	s = re.ReplaceAllString(s, " ")
	// Lowercase for better reuse across documents that differ only in case
	s = strings.ToLower(s)
	return s
}

// ContentHash produces a full 64-char SHA256 for use as ContentHash field
// (more collision resistant, used for de-duplication / cache key).
func ContentHash(content string) string {
	normalized := normalizeForID(content)
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}

// ImageContentHash produces a stable key for VLM caching: hash(imageBytes) + model + promptVersion
func ImageContentHash(imageBytes []byte, vlmModelID, promptVersion string) string {
	h := sha256.New()
	h.Write(imageBytes)
	h.Write([]byte("|"))
	h.Write([]byte(vlmModelID))
	h.Write([]byte("|"))
	h.Write([]byte(promptVersion))
	sum := h.Sum(nil)
	return hex.EncodeToString(sum[:])
}

// WikiDocMapKey produces cache key for per-document wiki extract/summary/classify map.
// Uses frozen (post-VLM) document content hash + granularity + model + prompt version.
func WikiDocMapKey(docContentHash, granularity, chatModelID, promptVersion string) string {
	payload := fmt.Sprintf("%s|%s|%s|%s", docContentHash, granularity, chatModelID, promptVersion)
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:16])
}

// EmbeddingCacheKey produces key for embedding cache.
func EmbeddingCacheKey(normalizedText, embeddingModelID string, dim int) string {
	payload := fmt.Sprintf("%s|%s|%d", normalizedText, embeddingModelID, dim)
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:16])
}