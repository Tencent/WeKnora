package service

import (
	"bytes"
	"context"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type chunkSyncStats struct {
	Planned   int
	Created   int
	Updated   int
	Reused    int
	Deleted   int
	ExtraKept int
	StaleIDs  []string
}

// syncReparseBaseChunks keeps stable content-addressed chunks in place across
// reparses while removing text/parent chunks that no longer belong to the
// current parse result.
func (s *knowledgeService) syncReparseBaseChunks(
	ctx context.Context,
	tenantID uint64,
	knowledgeID string,
	desired []*types.Chunk,
) (chunkSyncStats, error) {
	stats := chunkSyncStats{Planned: len(desired)}

	existingChunks, err := s.chunkRepo.ListAllChunksByKnowledgeID(ctx, tenantID, knowledgeID)
	if err != nil {
		return stats, err
	}

	existingByID := make(map[string]*types.Chunk, len(existingChunks))
	for _, chunk := range existingChunks {
		existingByID[chunk.ID] = chunk
	}

	desiredIDs := make(map[string]struct{}, len(desired))
	toCreate := make([]*types.Chunk, 0)
	toUpdate := make([]*types.Chunk, 0)
	for _, chunk := range desired {
		desiredIDs[chunk.ID] = struct{}{}
		existing, ok := existingByID[chunk.ID]
		if !ok {
			toCreate = append(toCreate, chunk)
			continue
		}

		preserveExistingChunkIdentity(chunk, existing)
		if persistedChunkEqual(existing, chunk) {
			stats.Reused++
			continue
		}
		toUpdate = append(toUpdate, chunk)
	}

	staleIDs := make([]string, 0)
	for _, existing := range existingChunks {
		if _, ok := desiredIDs[existing.ID]; ok {
			continue
		}
		if shouldDeleteReparseStaleChunk(existing, desiredIDs) {
			staleIDs = append(staleIDs, existing.ID)
			continue
		}
		stats.ExtraKept++
	}

	if len(toCreate) > 0 {
		if err := s.chunkRepo.HardDeleteChunks(ctx, tenantID, chunkIDs(toCreate)); err != nil {
			return stats, err
		}
		if err := s.chunkRepo.CreateChunks(ctx, toCreate); err != nil {
			return stats, err
		}
		stats.Created = len(toCreate)
	}
	for _, chunk := range toUpdate {
		if err := s.chunkRepo.UpdateChunk(ctx, chunk); err != nil {
			return stats, err
		}
		stats.Updated++
	}
	if len(staleIDs) > 0 {
		stats.Deleted = len(staleIDs)
		stats.StaleIDs = staleIDs
	}

	return stats, nil
}

func (s *knowledgeService) deleteReparseStaleChunks(
	ctx context.Context,
	tenantID uint64,
	staleIDs []string,
) error {
	if len(staleIDs) == 0 {
		return nil
	}
	return s.chunkRepo.HardDeleteChunks(ctx, tenantID, staleIDs)
}

type staleVectorDeleter interface {
	DeleteByChunkIDList(ctx context.Context, indexIDList []string, dimension int, knowledgeType string) error
}

type vectorDimensionProvider interface {
	GetDimensions() int
}

func deleteReparseStaleVectors(
	ctx context.Context,
	deleter staleVectorDeleter,
	embeddingModel vectorDimensionProvider,
	staleIDs []string,
	knowledgeType string,
) error {
	if len(staleIDs) == 0 || deleter == nil || embeddingModel == nil {
		return nil
	}
	return deleter.DeleteByChunkIDList(ctx, staleIDs, embeddingModel.GetDimensions(), knowledgeType)
}

func (s *knowledgeService) upsertStableChunk(
	ctx context.Context,
	tenantID uint64,
	chunk *types.Chunk,
) (created bool, updated bool, err error) {
	stats, err := upsertStableChunks(ctx, s.chunkRepo, tenantID, []*types.Chunk{chunk})
	if err != nil {
		return false, false, err
	}
	return stats.Created == 1, stats.Updated == 1, nil
}

func upsertStableChunks(
	ctx context.Context,
	repo interfaces.ChunkRepository,
	tenantID uint64,
	chunks []*types.Chunk,
) (chunkSyncStats, error) {
	stats := chunkSyncStats{Planned: len(chunks)}
	if len(chunks) == 0 {
		return stats, nil
	}

	existingChunks, err := repo.ListChunksByID(ctx, tenantID, chunkIDs(chunks))
	if err != nil {
		return stats, err
	}
	existingByID := make(map[string]*types.Chunk, len(existingChunks))
	for _, chunk := range existingChunks {
		existingByID[chunk.ID] = chunk
	}

	toCreate := make([]*types.Chunk, 0)
	toUpdate := make([]*types.Chunk, 0)
	for _, chunk := range chunks {
		existing, ok := existingByID[chunk.ID]
		if !ok {
			toCreate = append(toCreate, chunk)
			continue
		}
		preserveExistingChunkIdentity(chunk, existing)
		if persistedChunkEqual(existing, chunk) {
			stats.Reused++
			continue
		}
		toUpdate = append(toUpdate, chunk)
	}

	if len(toCreate) > 0 {
		if err := repo.HardDeleteChunks(ctx, tenantID, chunkIDs(toCreate)); err != nil {
			return stats, err
		}
		if err := repo.CreateChunks(ctx, toCreate); err != nil {
			return stats, err
		}
		stats.Created = len(toCreate)
	}
	for _, chunk := range toUpdate {
		if err := repo.UpdateChunk(ctx, chunk); err != nil {
			return stats, err
		}
		stats.Updated++
	}
	return stats, nil
}

func preserveExistingChunkIdentity(desired *types.Chunk, existing *types.Chunk) {
	desired.SeqID = existing.SeqID
	desired.CreatedAt = existing.CreatedAt
	desired.DeletedAt = existing.DeletedAt
}

func persistedChunkEqual(a *types.Chunk, b *types.Chunk) bool {
	return a.ID == b.ID &&
		a.TenantID == b.TenantID &&
		a.KnowledgeID == b.KnowledgeID &&
		a.KnowledgeBaseID == b.KnowledgeBaseID &&
		a.TagID == b.TagID &&
		a.Content == b.Content &&
		a.ChunkIndex == b.ChunkIndex &&
		a.IsEnabled == b.IsEnabled &&
		a.Flags == b.Flags &&
		a.Status == b.Status &&
		a.StartAt == b.StartAt &&
		a.EndAt == b.EndAt &&
		a.PreChunkID == b.PreChunkID &&
		a.NextChunkID == b.NextChunkID &&
		a.ChunkType == b.ChunkType &&
		a.ParentChunkID == b.ParentChunkID &&
		bytes.Equal(a.RelationChunks, b.RelationChunks) &&
		bytes.Equal(a.IndirectRelationChunks, b.IndirectRelationChunks) &&
		bytes.Equal(a.Metadata, b.Metadata) &&
		a.ContentHash == b.ContentHash &&
		a.ImageInfo == b.ImageInfo
}

func shouldDeleteReparseStaleChunk(chunk *types.Chunk, desiredIDs map[string]struct{}) bool {
	switch chunk.ChunkType {
	case types.ChunkTypeText, types.ChunkTypeParentText:
		return true
	}
	if chunk.ParentChunkID == "" {
		return false
	}
	_, parentStillExists := desiredIDs[chunk.ParentChunkID]
	return !parentStillExists
}

func chunkIDs(chunks []*types.Chunk) []string {
	ids := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		ids = append(ids, chunk.ID)
	}
	return ids
}
