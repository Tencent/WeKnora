package service

import "github.com/Tencent/WeKnora/internal/types"

// newDocumentIndexInfo builds the canonical index payload for a document
// chunk. Folder placement comes from the knowledge row, not from the chunk, so
// every indexing path must pass the authoritative FolderID explicitly.
func newDocumentIndexInfo(chunk *types.Chunk, folderID string) *types.IndexInfo {
	return &types.IndexInfo{
		Content:         chunk.Content,
		SourceID:        chunk.ID,
		SourceType:      types.ChunkSourceType,
		ChunkID:         chunk.ID,
		KnowledgeID:     chunk.KnowledgeID,
		KnowledgeBaseID: chunk.KnowledgeBaseID,
		FolderID:        folderID,
		IsEnabled:       true,
	}
}
