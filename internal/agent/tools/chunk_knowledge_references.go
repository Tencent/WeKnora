package tools

import "github.com/Tencent/WeKnora/internal/types"

// chunkKnowledgeReference converts a knowledge-base chunk exposed to the model
// into the runtime-only reference used by feedback attribution. content may be
// enriched with OCR/caption context by the calling tool.
func chunkKnowledgeReference(chunk *types.Chunk, knowledgeTitle, content string) *types.SearchResult {
	if chunk == nil || chunk.ID == "" {
		return nil
	}
	if content == "" {
		content = chunk.Content
	}
	return &types.SearchResult{
		ID:              chunk.ID,
		Content:         content,
		KnowledgeID:     chunk.KnowledgeID,
		ChunkIndex:      chunk.ChunkIndex,
		KnowledgeTitle:  knowledgeTitle,
		StartAt:         chunk.StartAt,
		EndAt:           chunk.EndAt,
		MatchType:       types.MatchTypeKeywords,
		ChunkType:       string(chunk.ChunkType),
		ParentChunkID:   chunk.ParentChunkID,
		ImageInfo:       chunk.ImageInfo,
		ChunkMetadata:   chunk.Metadata,
		MatchedContent:  content,
		KnowledgeBaseID: chunk.KnowledgeBaseID,
		RecallWeight:    chunk.RecallWeight,
	}
}

func chunkKnowledgeReferences(chunks []*types.Chunk, knowledgeTitle string) []*types.SearchResult {
	references := make([]*types.SearchResult, 0, len(chunks))
	for _, chunk := range chunks {
		if reference := chunkKnowledgeReference(chunk, knowledgeTitle, ""); reference != nil {
			references = append(references, reference)
		}
	}
	return references
}
