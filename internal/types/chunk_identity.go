package types

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

const (
	chunkIdentityVersion                 = "chunk-id:v1"
	embeddingFingerprintVersion          = "embedding:v1"
	ChunkEmbeddingFingerprintMetadataKey = "_weknora_embedding_fingerprint"
)

type ChunkIDAllocator struct {
	knowledgeID string
	occurrences map[string]int
}

func NewChunkIDAllocator(knowledgeID string) *ChunkIDAllocator {
	return &ChunkIDAllocator{knowledgeID: knowledgeID, occurrences: make(map[string]int)}
}

func NormalizeChunkContent(content string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	return strings.TrimSpace(content)
}

func ChunkContentHash(content string) string {
	return sha256Hex([]byte(NormalizeChunkContent(content)))
}

func (a *ChunkIDAllocator) Next(chunkType ChunkType, content string) (string, string) {
	contentHash := ChunkContentHash(content)
	occurrenceKey := string(chunkType) + "\x00" + contentHash
	occurrence := a.occurrences[occurrenceKey]
	a.occurrences[occurrenceKey] = occurrence + 1

	sum := sha256.Sum256([]byte(strings.Join([]string{
		chunkIdentityVersion,
		a.knowledgeID,
		string(chunkType),
		contentHash,
		strconv.Itoa(occurrence),
	}, "\x00")))
	idBytes := append([]byte(nil), sum[:16]...)
	idBytes[6] = (idBytes[6] & 0x0f) | 0x50
	idBytes[8] = (idBytes[8] & 0x3f) | 0x80
	id, err := uuid.FromBytes(idBytes)
	if err != nil {
		panic(err)
	}
	return id.String(), contentHash
}

func EmbeddingInput(title, contextHeader, content string) string {
	body := NormalizeChunkContent(content)
	if contextHeader != "" {
		body = contextHeader + "\n\n" + body
	}
	if title = strings.TrimSpace(title); title != "" {
		body = title + "\n" + body
	}
	return body
}

func EmbeddingFingerprint(modelKey string, dimensions int, input string) string {
	return sha256Hex([]byte(strings.Join([]string{
		embeddingFingerprintVersion,
		modelKey,
		strconv.Itoa(dimensions),
		input,
	}, "\x00")))
}

func ChunkEmbeddingFingerprint(metadata JSON) string {
	values, err := metadata.Map()
	if err != nil {
		return ""
	}
	fingerprint, _ := values[ChunkEmbeddingFingerprintMetadataKey].(string)
	return fingerprint
}

func WithChunkEmbeddingFingerprint(metadata JSON, fingerprint string) (JSON, error) {
	values := make(map[string]json.RawMessage)
	if len(metadata) > 0 {
		err := json.Unmarshal(metadata, &values)
		if err != nil {
			return nil, err
		}
	}
	if values == nil {
		values = make(map[string]json.RawMessage)
	}

	fingerprintData, err := json.Marshal(fingerprint)
	if err != nil {
		return nil, err
	}
	values[ChunkEmbeddingFingerprintMetadataKey] = fingerprintData

	data, err := json.Marshal(values)
	return JSON(data), err
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
