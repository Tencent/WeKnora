package types

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

var stableChunkNamespace = uuid.NewSHA1(uuid.NameSpaceOID, []byte("github.com/Tencent/WeKnora/stable-chunk-id/v1"))

// StableChunkIDSpec describes the deterministic identity inputs for a chunk.
type StableChunkIDSpec struct {
	KnowledgeID       string
	ChunkType         ChunkType
	Content           string
	Occurrence        int
	ChunkingConfigKey string
}

// NormalizeContentForIdentity trims content and collapses all whitespace runs.
func NormalizeContentForIdentity(content string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(content)), " ")
}

// StableContentHash returns a SHA-256 hash for normalized content.
func StableContentHash(content string) string {
	normalized := NormalizeContentForIdentity(content)
	if normalized == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}

// StableChunkID returns a UUID string derived from stable chunk inputs.
func StableChunkID(spec StableChunkIDSpec) string {
	key := fmt.Sprintf(
		"knowledge=%s\ntype=%s\ncontent_hash=%s\noccurrence=%d\nchunking=%s",
		spec.KnowledgeID,
		spec.ChunkType,
		StableContentHash(spec.Content),
		spec.Occurrence,
		spec.ChunkingConfigKey,
	)
	return uuid.NewSHA1(stableChunkNamespace, []byte(key)).String()
}

// StableChunkingConfigKey returns a deterministic key for chunking-sensitive identity.
func StableChunkingConfigKey(config ChunkingConfig) string {
	bytes, err := json.Marshal(config)
	if err != nil {
		return fmt.Sprintf("%+v", config)
	}
	sum := sha256.Sum256(bytes)
	return hex.EncodeToString(sum[:])
}
