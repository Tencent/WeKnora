package service

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// NormalizeChunkContent applies lightweight normalization (NFC + CRLF→LF + TrimSpace).
//
// IMPORTANT: This is intentionally minimal. Do NOT add ToLower, traditional/simplified
// Chinese conversion, or punctuation stripping here — those belong to FAQ's
// NormalizeQuestion (see internal/types/faq.go CalculateFAQContentHash).
//
// Document chunks must preserve case and character variants to avoid merging
// semantically distinct content into one stable ID (e.g. "Python" vs "python").
// FAQ uses aggressive normalization for fuzzy matching; document chunks do not.
func NormalizeChunkContent(s string) string {
	s = norm.NFC.String(s)
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.TrimSpace(s)
	return s
}

// ComputeFAQChunkID computes a stable chunk ID for FAQ entries based on Q+A content hash.
func ComputeFAQChunkID(question, answer string) string {
	normalizedQuestion := NormalizeChunkContent(question)
	normalizedAnswer := NormalizeChunkContent(answer)

	combined := fmt.Sprintf("Q:%s\x1FA:%s", normalizedQuestion, normalizedAnswer)
	hash := sha256.Sum256([]byte(combined))

	hashStr := hex.EncodeToString(hash[:])
	if len(hashStr) > 16 {
		hashStr = hashStr[:16]
	}

	return "faq_" + hashStr
}

// ComputeContentChunkID computes a content-addressed chunk ID for document chunks.
func ComputeContentChunkID(content string) string {
	normalizedContent := NormalizeChunkContent(content)
	hash := sha256.Sum256([]byte(normalizedContent))

	hashStr := hex.EncodeToString(hash[:])
	if len(hashStr) > 16 {
		hashStr = hashStr[:16]
	}

	return "kb_" + hashStr
}

// ComputeEmbeddingID computes an ID from an embedding vector (optional, for dedup).
func ComputeEmbeddingID(embedding []float32) string {
	var sb strings.Builder
	for _, v := range embedding {
		sb.WriteString(strconv.FormatFloat(float64(v), 'f', -1, 32))
		sb.WriteRune(',')
	}

	hash := sha256.Sum256([]byte(sb.String()))
	hashStr := hex.EncodeToString(hash[:])
	if len(hashStr) > 16 {
		hashStr = hashStr[:16]
	}

	return "emb_" + hashStr
}

// ComputeChunkStableID computes a deterministic chunk ID from (docID, role, seq, content).
func ComputeChunkStableID(docID, role string, seq int, content string) string {
	h := sha256.New()
	h.Write([]byte(docID))
	h.Write([]byte{0x1F})
	h.Write([]byte(role))
	h.Write([]byte{0x1F})
	h.Write([]byte(strconv.Itoa(seq)))
	h.Write([]byte{0x1F})
	h.Write([]byte(NormalizeChunkContent(content)))
	return "ck_" + hex.EncodeToString(h.Sum(nil)[:16])
}

// ComputeChunkContentHash computes a full SHA-256 hash of normalized content for dedup matching.
func ComputeChunkContentHash(content string) string {
	normalized := NormalizeChunkContent(content)
	hash := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(hash[:])
}
