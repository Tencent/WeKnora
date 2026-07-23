package service

import (
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
)

// chunkIDNamespace is a fixed, project-specific UUID namespace used to derive
// deterministic (UUIDv5-shaped) chunk IDs. It must NEVER change: changing it
// would shift every derived chunk ID and defeat cross-rebuild reuse.
var chunkIDNamespace = uuid.MustParse("7c9e3a52-1f4b-4d8e-9b6a-5e2d8c1f0a37")

// stableChunkID derives a content-addressed, deterministic chunk ID from
// (knowledgeID, chunkType, stable sequence, normalized content).
//
// Rationale (issue #1679): chunk IDs used to be uuid.New() — regenerated on
// every parse — so any reparse/rebuild invalidated all downstream references
// (vector index entries, wiki SourceChunks/ChunkRefs, graph edges) and made
// per-chunk caching impossible. With a content hash-derived ID, identical
// content at the same position in the same knowledge yields the same ID
// across rebuilds, letting embeddings, wiki references and graph edges
// survive a reparse by reference.
//
// The returned value is a valid RFC 4122 UUID (v5, SHA-1 name-based) so all
// existing storage/validation that expects UUID-shaped chunk IDs keeps
// working unchanged.
func stableChunkID(knowledgeID string, chunkType types.ChunkType, seq int, content string) string {
	sum := sha256.Sum256([]byte(normalizeChunkContent(content)))
	name := fmt.Sprintf("%s|%s|%d|%x", knowledgeID, chunkType, seq, sum)
	return uuid.NewSHA1(chunkIDNamespace, []byte(name)).String()
}

// normalizeChunkContent canonicalizes chunk text before hashing so that
// byte-level noise that does not change meaning (CRLF vs LF, leading/trailing
// whitespace) does not shift the derived chunk ID between rebuilds.
func normalizeChunkContent(content string) string {
	return strings.TrimSpace(strings.ReplaceAll(content, "\r\n", "\n"))
}
