package types

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// ContentHashAlgo identifies the hashing algorithm used for all cache keys
// and stable chunk identities in the content-addressed artifact cache system.
const ContentHashAlgo = "sha256"

var markdownImageURLPattern = regexp.MustCompile(`(!\[[^\]]*\]\()([^\s)]+)([^)]*\))`)

// HashString computes the SHA-256 hex digest of an arbitrary string.
func HashString(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// HashBytes computes the SHA-256 hex digest of raw bytes.
func HashBytes(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// HashAll computes a SHA-256 hex digest of ordered, length-prefixed parts.
// Length-prefixing preserves boundaries for arbitrary strings, unlike joining
// with a delimiter (where {"a|b", "c"} and {"a", "b|c"} collide).
func HashAll(parts ...string) string {
	var builder strings.Builder
	for _, part := range parts {
		fmt.Fprintf(&builder, "%d:", len(part))
		builder.WriteString(part)
	}
	return HashString(builder.String())
}

// HashSorted computes SHA-256 hex of a sorted set of length-prefixed parts so
// the result is independent of the caller's input ordering. Only useful when
// the semantics truly require order-independence.
func HashSorted(parts ...string) string {
	sorted := make([]string, len(parts))
	copy(sorted, parts)
	sort.Strings(sorted)
	return HashAll(sorted...)
}

// StableChunkContentHash removes regenerated Markdown image destinations before
// hashing. Parsed embedded images are persisted at fresh storage URLs on each
// reparse; treating those URLs as content would otherwise invalidate stable text
// IDs and every downstream cache despite identical source bytes.
func StableChunkContentHash(content string) string {
	stable := markdownImageURLPattern.ReplaceAllString(content, "$1<image>$3")
	return HashString(stable)
}

// StableChunkID generates a deterministic chunk ID for content-derived
// chunks.  The ID is SHA-256("chunk|<knowledgeID>|<chunkIndex>|<chunkType>|<contentHash>")
// truncated to 36 characters to fit the existing VARCHAR(36) primary key.
//
// This is ONLY used for content-derived chunks (text, parent_text,
// summary, image_ocr, image_caption).  FAQ chunks and user-created chunks
// continue to use UUIDs so existing APIs are not disturbed.
//
// chunkIndex ensures same-text chunks at different positions get
// different IDs even within the same document.
func StableChunkID(knowledgeID string, chunkIndex int, chunkType ChunkType, contentHash string) string {
	input := fmt.Sprintf("chunk|%s|%d|%s|%s", knowledgeID, chunkIndex, chunkType, contentHash)
	full := HashString(input)
	if len(full) > 36 {
		return full[:36]
	}
	return full
}

// StableQuestionID generates a deterministic identifier for a generated
// question, replacing the previous time.UnixNano() scheme.  Using the
// question text hash ensures the same question gets the same ID across
// rebuilds, which stabilises the SourceID used for index entries
// ({chunkID}-{questionID}).
func StableQuestionID(question string) string {
	full := HashString("question|" + question)
	if len(full) > 24 {
		return "q" + full[:23]
	}
	return "q" + full
}

// ImageChunkStableID generates a deterministic chunk ID for multimodal child
// chunks (OCR / caption). The image identity must distinguish repeated image
// occurrences under one parent while remaining stable across regenerated URLs.
func ImageChunkStableID(parentChunkID string, imageIdentity string, chunkType ChunkType) string {
	input := fmt.Sprintf("imgchunk|%s|%s|%s", parentChunkID, imageIdentity, chunkType)
	full := HashString(input)
	if len(full) > 36 {
		return full[:36]
	}
	return full
}

// EmbeddingInputHash computes the content hash for a chunk's effective
// embedding input: titlePrefix + EmbeddingContent() after whitespace
// trimming.  This must exactly match the value that will be passed to
// sanitizeForEmbedding downstream so cache keys align.
//
// Intentionally exported: cache keys for the embedding layer are computed
// close to the retriever, not here, but this function documents the
// contract for callers.
func EmbeddingInputHash(titlePrefix, contextHeader, content string) string {
	body := strings.TrimSpace(content)
	var input string
	if contextHeader != "" {
		input = titlePrefix + contextHeader + "\n\n" + body
	} else {
		input = titlePrefix + body
	}
	return HashString(input)
}
