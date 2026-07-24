package contentcache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

const (
	keyVersion = "v1"
)

var chunkNamespace = uuid.MustParse("9f2d9e51-6b8a-4f7e-a424-a7f87a5f9d91")

// ChunkIDInput is the stable identity of a stored chunk.
type ChunkIDInput struct {
	KnowledgeID string
	ChunkType   string
	// Seq is retained for callers that still carry source order, but it is not
	// part of the content identity. Occurrence differentiates repeated equal
	// chunks without making unrelated insertions change existing IDs.
	Seq           int
	Occurrence    int
	Content       string
	ContextHeader string
	ParentID      string
	ImageURL      string
}

// NormalizeText canonicalizes text for content-addressed cache keys without
// hiding meaningful internal whitespace.
func NormalizeText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// TextHash returns a SHA-256 hash for normalized text.
func TextHash(s string) string {
	return sha256Hex([]byte(NormalizeText(s)))
}

// ImageHash returns a SHA-256 hash for raw image bytes.
func ImageHash(imageBytes []byte) string {
	return sha256Hex(imageBytes)
}

// StableChunkID returns a UUID-shaped deterministic chunk ID.
func StableChunkID(input ChunkIDInput) string {
	payload, _ := json.Marshal(struct {
		Version    string `json:"version"`
		Identity   string `json:"identity"`
		Occurrence int    `json:"occurrence"`
	}{
		Version:    keyVersion,
		Identity:   ChunkIdentityKey(input),
		Occurrence: input.Occurrence,
	})
	return uuid.NewSHA1(chunkNamespace, []byte(payload)).String()
}

// ChunkIdentityKey returns the order-independent identity shared by repeated
// equal chunks. Callers increment Occurrence per identity before calling
// StableChunkID.
func ChunkIdentityKey(input ChunkIDInput) string {
	payload, _ := json.Marshal(struct {
		Version       string `json:"version"`
		KnowledgeID   string `json:"knowledge_id"`
		ChunkType     string `json:"chunk_type"`
		Content       string `json:"content"`
		ContextHeader string `json:"context_header"`
		ParentID      string `json:"parent_id"`
		ImageURL      string `json:"image_url"`
	}{
		Version:       keyVersion,
		KnowledgeID:   strings.TrimSpace(input.KnowledgeID),
		ChunkType:     strings.TrimSpace(string(input.ChunkType)),
		Content:       NormalizeText(input.Content),
		ContextHeader: NormalizeText(input.ContextHeader),
		ParentID:      strings.TrimSpace(input.ParentID),
		ImageURL:      strings.TrimSpace(input.ImageURL),
	})
	return sha256Hex(payload)
}

// ChunkContentHash captures every text input that affects a chunk's semantic
// payload. It is separate from StableChunkID so callers can reuse it for
// embedding/wiki cache lookups.
func ChunkContentHash(content, contextHeader string) string {
	return TextHash(NormalizeText(contextHeader) + "\x00" + NormalizeText(content))
}

func VLMKey(imageHash, modelID, promptVersion string) string {
	return joinKey("vlm", imageHash, modelID, promptVersion)
}

func EmbeddingKey(textHash, modelID string, dimension int) string {
	return joinKey("embedding", textHash, modelID, fmt.Sprintf("dim%d", dimension))
}

func WikiMapKey(documentHash, granularity, modelID, promptVersion string) string {
	return joinKey("wiki-map", documentHash, granularity, modelID, promptVersion)
}

func PostprocessLLMKey(payloadHash, layer, modelID, promptVersion string) string {
	return joinKey("postprocess-llm", layer, payloadHash, modelID, promptVersion)
}

func ParseArtifactKey(fileHash, parserEngine, renderConfigHash string) string {
	return joinKey("parse-artifact", fileHash, parserEngine, renderConfigHash)
}

func GraphExtractKey(chunkHash, configHash, modelID, promptVersion string) string {
	return joinKey("graph-extract", chunkHash, configHash, modelID, promptVersion)
}

func joinKey(parts ...string) string {
	clean := make([]string, 0, len(parts)+1)
	clean = append(clean, keyVersion)
	for _, part := range parts {
		p := strings.TrimSpace(part)
		if p == "" {
			p = "_"
		}
		clean = append(clean, strings.ReplaceAll(p, " ", "_"))
	}
	return strings.Join(clean, ":")
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
