package service

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

const ContentHashVersion = "v1"

func NormalizeChunkContent(content string) string {
	return strings.TrimSpace(content)
}

func ComputeContentHash(knowledgeID string, chunkIndex int, content string) string {
	normalized := NormalizeChunkContent(content)
	h := sha256.New()
	h.Write([]byte(ContentHashVersion + ":" + knowledgeID + ":" + strconv.Itoa(chunkIndex) + ":" + normalized))
	return hex.EncodeToString(h.Sum(nil))
}

func StableChunkID(knowledgeID string, chunkIndex int, content string) string {
	hash := ComputeContentHash(knowledgeID, chunkIndex, content)
	return hash[:32]
}

func StableChunkIDFromHash(hash string) string {
	if len(hash) >= 32 { return hash[:32] }
	return hash
}

// StableQuestionID generates a deterministic question ID from chunk ID,
// question index, and question content. Same input → same ID, so reparse
// won't create duplicate vectors for unchanged questions.
func StableQuestionID(chunkID string, questionIndex int, questionContent string) string {
	h := sha256.New()
	h.Write([]byte("q:"))
	h.Write([]byte(chunkID))
	h.Write([]byte{0})
	h.Write([]byte(strconv.Itoa(questionIndex)))
	h.Write([]byte{0})
	h.Write([]byte(strings.TrimSpace(questionContent)))
	return "q" + hex.EncodeToString(h.Sum(nil))[:16]
}

type ChunkDiffResult struct {
	Unchanged    []*types.Chunk
	Changed      []*types.Chunk
	Added        []*types.Chunk
	RemovedIDs   []string
	UnchangedIDs map[string]bool
}

func DiffChunks(oldChunks, newChunks []*types.Chunk) *ChunkDiffResult {
	result := &ChunkDiffResult{UnchangedIDs: make(map[string]bool)}
	type oldEntry struct { chunkID string; contentHash string }
	oldMap := make(map[int]oldEntry)
	for _, c := range oldChunks { oldMap[c.ChunkIndex] = oldEntry{c.ID, c.ContentHash} }
	newMap := make(map[int]*types.Chunk)
	for _, c := range newChunks { newMap[c.ChunkIndex] = c }
	for _, nc := range newChunks {
		old, exists := oldMap[nc.ChunkIndex]
		if !exists { result.Added = append(result.Added, nc)
	} else if old.contentHash == nc.ContentHash { nc.ID = old.chunkID; result.Unchanged = append(result.Unchanged, nc); result.UnchangedIDs[old.chunkID] = true
	} else { result.Changed = append(result.Changed, nc); result.RemovedIDs = append(result.RemovedIDs, old.chunkID) }
	}
	for idx, old := range oldMap { if _, exists := newMap[idx]; !exists { result.RemovedIDs = append(result.RemovedIDs, old.chunkID) } }
	return result
}
