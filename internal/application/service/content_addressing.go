package service

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
)

const stableChunkIDVersion = "chunk-id-v1"

// NormalizeForHash canonicalizes deterministic cache/chunk inputs while
// preserving meaningful text structure.
func NormalizeForHash(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.TrimSpace(s)
}

func contentHash(s string) string {
	sum := sha256.Sum256([]byte(NormalizeForHash(s)))
	return hex.EncodeToString(sum[:])
}

func bytesHash(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func stableHash(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ChunkIDAllocator assigns deterministic IDs and disambiguates duplicate chunks
// within a document in a reproducible way.
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

func (a *ChunkIDAllocator) StableChunkID(chunkType types.ChunkType, content, contextHeader string, positionKey string) (string, string) {
	normalized := NormalizeForHash(contextHeader + "\n" + content)
	hash := contentHash(normalized)
	duplicateKey := strings.Join([]string{string(chunkType), positionKey, hash}, "\x00")
	occurrence := a.seen[duplicateKey]
	a.seen[duplicateKey] = occurrence + 1
	raw := fmt.Sprintf("%s|%d|%s|%s|%s|%s|%d",
		stableChunkIDVersion,
		a.tenantID,
		a.knowledgeID,
		chunkType,
		hash,
		positionKey,
		occurrence,
	)
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(raw)).String(), hash
}

func StableDerivedChunkID(tenantID uint64, knowledgeID string, chunkType types.ChunkType, parentChunkID, content string) (string, string) {
	hash := contentHash(content)
	raw := fmt.Sprintf("%s|%d|%s|%s|%s|%s",
		stableChunkIDVersion,
		tenantID,
		knowledgeID,
		chunkType,
		parentChunkID,
		hash,
	)
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(raw)).String(), hash
}
