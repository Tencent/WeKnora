package service

import (
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/contentkey"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
)

// buildIngestionTextChunks is the production mapping from parser output to
// persisted parent/text chunk rows. Stable business identities are assigned
// before random database row IDs and their references are wired.
func buildIngestionTextChunks(
	tenantID uint64,
	knowledgeID string,
	knowledgeBaseID string,
	chunks []types.ParsedChunk,
	parents []types.ParsedParentChunk,
) ([]*types.Chunk, []*types.Chunk, error) {
	hasParentChild := len(parents) > 0
	parentCandidates := make([]contentkey.ChunkCandidate, len(parents))
	for i, parent := range parents {
		parentCandidates[i] = contentkey.ChunkCandidate{
			ChunkType:   types.ChunkTypeParentText,
			Content:     parent.Content,
			ParentIndex: -1,
		}
	}

	childCandidates := make([]contentkey.ChunkCandidate, len(chunks))
	for i, child := range chunks {
		parentIndex := -1
		if hasParentChild && child.ParentIndex >= 0 && child.ParentIndex < len(parents) {
			parentIndex = child.ParentIndex
		}
		childCandidates[i] = contentkey.ChunkCandidate{
			ChunkType:     types.ChunkTypeText,
			Content:       child.Content,
			ContextHeader: child.ContextHeader,
			ParentIndex:   parentIndex,
		}
	}

	parentIdentities, childIdentities, err := contentkey.AssignChunkIdentities(
		tenantID,
		knowledgeID,
		parentCandidates,
		childCandidates,
	)
	if err != nil {
		return nil, nil, err
	}

	now := time.Now()
	parentRows := make([]*types.Chunk, len(parents))
	for i, parent := range parents {
		parentRows[i] = &types.Chunk{
			ID:              uuid.NewString(),
			TenantID:        tenantID,
			KnowledgeID:     knowledgeID,
			KnowledgeBaseID: knowledgeBaseID,
			Content:         parent.Content,
			ChunkIndex:      parent.Seq,
			IsEnabled:       true,
			CreatedAt:       now,
			UpdatedAt:       now,
			StartAt:         parent.Start,
			EndAt:           parent.End,
			ChunkType:       types.ChunkTypeParentText,
			StableIdentity:  parentIdentities[i].StableIdentity,
			IdentityVersion: parentIdentities[i].IdentityVersion,
		}
		if i > 0 {
			parentRows[i-1].NextChunkID = parentRows[i].ID
			parentRows[i].PreChunkID = parentRows[i-1].ID
		}
	}

	textRows := make([]*types.Chunk, 0, len(chunks))
	for i, parsed := range chunks {
		if strings.TrimSpace(parsed.Content) == "" {
			continue
		}
		row := &types.Chunk{
			ID:              uuid.NewString(),
			TenantID:        tenantID,
			KnowledgeID:     knowledgeID,
			KnowledgeBaseID: knowledgeBaseID,
			Content:         parsed.Content,
			ContextHeader:   parsed.ContextHeader,
			ChunkIndex:      parsed.Seq,
			IsEnabled:       true,
			CreatedAt:       now,
			UpdatedAt:       now,
			StartAt:         parsed.Start,
			EndAt:           parsed.End,
			ChunkType:       types.ChunkTypeText,
			StableIdentity:  childIdentities[i].StableIdentity,
			IdentityVersion: childIdentities[i].IdentityVersion,
		}
		if hasParentChild && parsed.ParentIndex >= 0 && parsed.ParentIndex < len(parentRows) {
			row.ParentChunkID = parentRows[parsed.ParentIndex].ID
		}
		chunks[i].ChunkID = row.ID
		textRows = append(textRows, row)
	}

	return parentRows, textRows, nil
}
