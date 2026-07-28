package service

import (
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/artifact"
	"github.com/Tencent/WeKnora/internal/types"
)

type desiredChunkSet struct {
	All     []*types.Chunk
	Text    []*types.Chunk
	Added   []*types.Chunk
	Updated []*types.Chunk
	Stale   []*types.Chunk
}

type desiredParentIdentity struct {
	chunk       *types.Chunk
	semanticKey string
	contentHash string
}

// buildDesiredDocumentChunks assigns order-independent UUIDv5 identities. The
// current parser does not expose structural block IDs, so the safest available
// source anchor is the exact persisted content digest; unrelated insertion or
// reordering therefore leaves IDs stable without risking a false content reuse.
func buildDesiredDocumentChunks(
	knowledge *types.Knowledge,
	parsed []types.ParsedChunk,
	parsedParents []types.ParsedParentChunk,
	existing []*types.Chunk,
) (desiredChunkSet, error) {
	if knowledge == nil {
		return desiredChunkSet{}, fmt.Errorf("knowledge must not be nil")
	}
	now := time.Now()
	existingByID := make(map[string]*types.Chunk, len(existing))
	existingParentContent := make(map[string]string)
	legacyCandidates := make(map[string][]*types.Chunk)
	for _, chunk := range existing {
		if chunk == nil {
			continue
		}
		existingByID[chunk.ID] = chunk
		if chunk.ChunkType == types.ChunkTypeParentText {
			existingParentContent[chunk.ID] = artifact.SHA256Hex([]byte(chunk.Content))
		}
	}
	for _, chunk := range existing {
		if chunk == nil || !isDocumentBaseChunk(chunk.ChunkType) {
			continue
		}
		parentDigest := existingParentContent[chunk.ParentChunkID]
		key := legacyChunkSemanticKey(chunk.ChunkType, parentDigest, chunk.Content)
		legacyCandidates[key] = append(legacyCandidates[key], chunk)
	}

	desiredLegacyCounts := make(map[string]int)
	for _, parent := range parsedParents {
		desiredLegacyCounts[legacyChunkSemanticKey(types.ChunkTypeParentText, "", parent.Content)]++
	}
	for _, chunk := range parsed {
		if strings.TrimSpace(chunk.Content) == "" {
			continue
		}
		parentDigest := ""
		if chunk.ParentIndex >= 0 && chunk.ParentIndex < len(parsedParents) {
			parentDigest = artifact.SHA256Hex([]byte(parsedParents[chunk.ParentIndex].Content))
		}
		desiredLegacyCounts[legacyChunkSemanticKey(types.ChunkTypeText, parentDigest, chunk.Content)]++
	}

	allocator := artifact.NewIdentityAllocator(knowledge.ID, artifact.StableEntityIDVersion)
	parents := make([]desiredParentIdentity, len(parsedParents))
	result := desiredChunkSet{}
	for index, parsedParent := range parsedParents {
		contentHash := artifact.SHA256Hex([]byte(parsedParent.Content))
		identity, err := allocator.Next(
			types.ChunkTypeParentText,
			"",
			"content:"+contentHash,
			parsedParent.Content,
		)
		if err != nil {
			return desiredChunkSet{}, err
		}
		legacyKey := legacyChunkSemanticKey(types.ChunkTypeParentText, "", parsedParent.Content)
		id := uniqueLegacyChunkID(identity.ID, legacyKey, desiredLegacyCounts, legacyCandidates)
		chunk := &types.Chunk{
			ID:              id,
			TenantID:        knowledge.TenantID,
			KnowledgeID:     knowledge.ID,
			KnowledgeBaseID: knowledge.KnowledgeBaseID,
			Content:         parsedParent.Content,
			ContentHash:     contentHash,
			ChunkIndex:      parsedParent.Seq,
			IsEnabled:       true,
			CreatedAt:       now,
			UpdatedAt:       now,
			StartAt:         parsedParent.Start,
			EndAt:           parsedParent.End,
			ChunkType:       types.ChunkTypeParentText,
		}
		preserveLiveChunkFields(chunk, existingByID[id])
		parents[index] = desiredParentIdentity{
			chunk:       chunk,
			semanticKey: identity.SemanticKey,
			contentHash: contentHash,
		}
		result.All = append(result.All, chunk)
	}
	linkDesiredChunks(parentChunks(parents))

	for index, parsedChunk := range parsed {
		if strings.TrimSpace(parsedChunk.Content) == "" {
			continue
		}
		parentSemantic := ""
		parentDigest := ""
		parentID := ""
		if parsedChunk.ParentIndex >= 0 && parsedChunk.ParentIndex < len(parents) {
			parent := parents[parsedChunk.ParentIndex]
			parentSemantic = parent.semanticKey
			parentDigest = parent.contentHash
			parentID = parent.chunk.ID
		}
		contentHash := artifact.SHA256Hex([]byte(parsedChunk.Content))
		identity, err := allocator.Next(
			types.ChunkTypeText,
			parentSemantic,
			"content:"+contentHash,
			parsedChunk.Content,
		)
		if err != nil {
			return desiredChunkSet{}, err
		}
		legacyKey := legacyChunkSemanticKey(types.ChunkTypeText, parentDigest, parsedChunk.Content)
		id := uniqueLegacyChunkID(identity.ID, legacyKey, desiredLegacyCounts, legacyCandidates)
		chunk := &types.Chunk{
			ID:              id,
			TenantID:        knowledge.TenantID,
			KnowledgeID:     knowledge.ID,
			KnowledgeBaseID: knowledge.KnowledgeBaseID,
			Content:         parsedChunk.Content,
			ContentHash:     contentHash,
			ContextHeader:   parsedChunk.ContextHeader,
			ChunkIndex:      int(parsedChunk.Seq),
			IsEnabled:       true,
			CreatedAt:       now,
			UpdatedAt:       now,
			StartAt:         int(parsedChunk.Start),
			EndAt:           int(parsedChunk.End),
			ChunkType:       types.ChunkTypeText,
			ParentChunkID:   parentID,
		}
		preserveLiveChunkFields(chunk, existingByID[id])
		parsed[index].ChunkID = id
		result.All = append(result.All, chunk)
		result.Text = append(result.Text, chunk)
	}
	if len(parents) == 0 {
		linkDesiredChunks(result.Text)
	}

	desiredIDs := make(map[string]struct{}, len(result.All))
	for _, chunk := range result.All {
		desiredIDs[chunk.ID] = struct{}{}
		if existingByID[chunk.ID] == nil {
			result.Added = append(result.Added, chunk)
		} else {
			result.Updated = append(result.Updated, chunk)
		}
	}
	for _, chunk := range existing {
		if chunk == nil || !isDocumentBaseChunk(chunk.ChunkType) {
			continue
		}
		if _, desired := desiredIDs[chunk.ID]; !desired {
			result.Stale = append(result.Stale, chunk)
		}
	}
	return result, nil
}

func parentChunks(parents []desiredParentIdentity) []*types.Chunk {
	result := make([]*types.Chunk, 0, len(parents))
	for _, parent := range parents {
		result = append(result, parent.chunk)
	}
	return result
}

func linkDesiredChunks(chunks []*types.Chunk) {
	for index, chunk := range chunks {
		chunk.PreChunkID = ""
		chunk.NextChunkID = ""
		if index > 0 {
			chunk.PreChunkID = chunks[index-1].ID
		}
		if index+1 < len(chunks) {
			chunk.NextChunkID = chunks[index+1].ID
		}
	}
}

func uniqueLegacyChunkID(
	fallback string,
	semanticKey string,
	desiredCounts map[string]int,
	candidates map[string][]*types.Chunk,
) string {
	if desiredCounts[semanticKey] == 1 && len(candidates[semanticKey]) == 1 {
		return candidates[semanticKey][0].ID
	}
	return fallback
}

func legacyChunkSemanticKey(chunkType, parentDigest, content string) string {
	return chunkType + "\x00" + parentDigest + "\x00" + artifact.SHA256Hex([]byte(content))
}

func preserveLiveChunkFields(desired, live *types.Chunk) {
	if live == nil {
		return
	}
	desired.SeqID = live.SeqID
	desired.CreatedAt = live.CreatedAt
	desired.IsEnabled = live.IsEnabled
	desired.Flags = live.Flags
	desired.Status = live.Status
	desired.Metadata = append(types.JSON(nil), live.Metadata...)
	desired.RelationChunks = append(types.JSON(nil), live.RelationChunks...)
	desired.IndirectRelationChunks = append(types.JSON(nil), live.IndirectRelationChunks...)
	desired.ImageInfo = live.ImageInfo
}

func isDocumentBaseChunk(chunkType string) bool {
	return chunkType == types.ChunkTypeText || chunkType == types.ChunkTypeParentText
}

func chunkIDList(chunks []*types.Chunk) []string {
	result := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		if chunk != nil {
			result = append(result, chunk.ID)
		}
	}
	return result
}
