package contentcache

import (
	"crypto/sha256"
	"encoding/hex"
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
	KnowledgeID   string
	ChunkType     string
	Seq           int
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
	payload := strings.Join([]string{
		keyVersion,
		input.KnowledgeID,
		string(input.ChunkType),
		fmt.Sprintf("%d", input.Seq),
		NormalizeText(input.ContextHeader),
		NormalizeText(input.Content),
		input.ParentID,
		input.ImageURL,
	}, "\x00")
	return uuid.NewSHA1(chunkNamespace, []byte(payload)).String()
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
