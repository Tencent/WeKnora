package service

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
	"github.com/Tencent/WeKnora/internal/types"
)

type chunkReusePlan struct {
	Create          []*types.Chunk
	Update          []*types.Chunk
	Index           []*types.Chunk
	Reuse           []*types.Chunk
	Delete          []*types.Chunk
	DeleteGenerated []*types.Chunk
}

type chunkReconcileOps struct {
	Create       func(context.Context, []*types.Chunk) error
	Index        func(context.Context, []*types.Chunk) error
	Update       func(context.Context, []*types.Chunk) error
	DeleteVector func(context.Context, []string) error
	DeleteImages func(context.Context) error
	HardDelete   func(context.Context, []string) error
}

func executeChunkReconciliation(
	ctx context.Context,
	plan *chunkReusePlan,
	ops chunkReconcileOps,
) error {
	if err := ops.Create(ctx, plan.Create); err != nil {
		return err
	}
	if err := ops.Index(ctx, plan.Index); err != nil {
		createdIDs := chunkIDs(plan.Create)
		_ = ops.DeleteVector(ctx, createdIDs)
		_ = ops.HardDelete(ctx, createdIDs)
		return err
	}
	if err := ops.Update(ctx, plan.Update); err != nil {
		return err
	}

	stale := append([]*types.Chunk{}, plan.Delete...)
	stale = append(stale, plan.DeleteGenerated...)
	staleIDs := chunkIDs(stale)
	if err := ops.DeleteVector(ctx, staleIDs); err != nil {
		return err
	}
	if ops.DeleteImages != nil {
		if err := ops.DeleteImages(ctx); err != nil {
			return err
		}
	}
	return ops.HardDelete(ctx, staleIDs)
}

func reparseStorageDelta(newTotal, previousTotal int64) int64 {
	return newTotal - previousTotal
}

func staleExtractedImageURLs(
	ctx context.Context,
	plan *chunkReusePlan,
	storedImages []docparser.StoredImage,
) []string {
	imageInfos := make([]string, 0, len(plan.Delete)+len(plan.DeleteGenerated))
	for _, chunk := range append(append([]*types.Chunk{}, plan.Delete...), plan.DeleteGenerated...) {
		if chunk != nil && chunk.ImageInfo != "" {
			imageInfos = append(imageInfos, chunk.ImageInfo)
		}
	}

	currentURLs := make(map[string]struct{}, len(storedImages))
	for _, image := range storedImages {
		if image.ServingURL != "" {
			currentURLs[image.ServingURL] = struct{}{}
		}
	}

	staleURLs := collectImageURLs(ctx, imageInfos)
	filtered := staleURLs[:0]
	for _, url := range staleURLs {
		if _, current := currentURLs[url]; !current {
			filtered = append(filtered, url)
		}
	}
	return filtered
}

func buildStableDocumentChunks(
	knowledge *types.Knowledge,
	parsedChunks []types.ParsedChunk,
	parsedParents []types.ParsedParentChunk,
) ([]*types.Chunk, []*types.Chunk) {
	allocator := types.NewChunkIDAllocator(knowledge.ID)
	parents := make([]*types.Chunk, 0, len(parsedParents))
	for _, parsedParent := range parsedParents {
		id, contentHash := allocator.Next(types.ChunkTypeParentText, parsedParent.Content)
		parents = append(parents, &types.Chunk{
			ID:              id,
			TenantID:        knowledge.TenantID,
			KnowledgeID:     knowledge.ID,
			KnowledgeBaseID: knowledge.KnowledgeBaseID,
			Content:         parsedParent.Content,
			ContentHash:     contentHash,
			ChunkIndex:      parsedParent.Seq,
			IsEnabled:       true,
			StartAt:         parsedParent.Start,
			EndAt:           parsedParent.End,
			ChunkType:       types.ChunkTypeParentText,
		})
	}
	linkAdjacentChunks(parents)

	text := make([]*types.Chunk, 0, len(parsedChunks))
	for index, parsedChunk := range parsedChunks {
		if types.NormalizeChunkContent(parsedChunk.Content) == "" {
			continue
		}

		id, contentHash := allocator.Next(types.ChunkTypeText, parsedChunk.Content)
		chunk := &types.Chunk{
			ID:              id,
			TenantID:        knowledge.TenantID,
			KnowledgeID:     knowledge.ID,
			KnowledgeBaseID: knowledge.KnowledgeBaseID,
			Content:         parsedChunk.Content,
			ContentHash:     contentHash,
			ContextHeader:   parsedChunk.ContextHeader,
			ChunkIndex:      parsedChunk.Seq,
			IsEnabled:       true,
			StartAt:         parsedChunk.Start,
			EndAt:           parsedChunk.End,
			ChunkType:       types.ChunkTypeText,
		}
		if parsedChunk.ParentIndex >= 0 && parsedChunk.ParentIndex < len(parents) {
			chunk.ParentChunkID = parents[parsedChunk.ParentIndex].ID
		}

		parsedChunks[index].ChunkID = chunk.ID
		text = append(text, chunk)
	}
	if len(parents) == 0 {
		linkAdjacentChunks(text)
	}

	desired := make([]*types.Chunk, 0, len(parents)+len(text))
	desired = append(desired, parents...)
	desired = append(desired, text...)
	return desired, text
}

func setDesiredEmbeddingFingerprints(chunks []*types.Chunk, modelKey string, dimensions int, title string) error {
	for _, chunk := range chunks {
		if chunk == nil {
			continue
		}
		fingerprint := types.EmbeddingFingerprint(
			modelKey,
			dimensions,
			types.EmbeddingInput(title, chunk.ContextHeader, chunk.Content),
		)
		metadata, err := types.WithChunkEmbeddingFingerprint(chunk.Metadata, fingerprint)
		if err != nil {
			return fmt.Errorf("set chunk %q embedding fingerprint: %w", chunk.ID, err)
		}
		chunk.Metadata = metadata
	}
	return nil
}

func planChunkReuse(desired, existing []*types.Chunk, indexingEnabled bool) (*chunkReusePlan, error) {
	plan := &chunkReusePlan{}
	existingByID := make(map[string]*types.Chunk, len(existing))
	for _, chunk := range existing {
		if chunk != nil {
			existingByID[chunk.ID] = chunk
		}
	}

	desiredByID := make(map[string]struct{}, len(desired))
	for _, chunk := range desired {
		if chunk == nil {
			continue
		}
		desiredByID[chunk.ID] = struct{}{}

		existingChunk, found := existingByID[chunk.ID]
		if !found {
			plan.Create = append(plan.Create, chunk)
			if indexingEnabled && chunk.ChunkType == types.ChunkTypeText {
				plan.Index = append(plan.Index, chunk)
			}
			continue
		}

		var desiredFingerprint string
		if indexingEnabled && chunk.ChunkType == types.ChunkTypeText {
			var err error
			desiredFingerprint, err = embeddingFingerprint(chunk.Metadata)
			if err != nil {
				return nil, err
			}
		}
		copyReparseOwnedFields(chunk, existingChunk)
		if indexingEnabled && chunk.ChunkType == types.ChunkTypeText {
			metadata, err := types.WithChunkEmbeddingFingerprint(chunk.Metadata, desiredFingerprint)
			if err != nil {
				return nil, fmt.Errorf("preserve chunk %q metadata: %w", chunk.ID, err)
			}
			chunk.Metadata = metadata
		}
		plan.Update = append(plan.Update, chunk)

		if !indexingEnabled || chunk.ChunkType != types.ChunkTypeText {
			continue
		}
		if existingChunk.Status == int(types.ChunkStatusIndexed) &&
			types.ChunkEmbeddingFingerprint(existingChunk.Metadata) == desiredFingerprint {
			plan.Reuse = append(plan.Reuse, chunk)
			continue
		}
		plan.Index = append(plan.Index, chunk)
	}

	for _, chunk := range existing {
		if chunk == nil {
			continue
		}
		if isGeneratedChunk(chunk.ChunkType) {
			plan.DeleteGenerated = append(plan.DeleteGenerated, chunk)
			continue
		}
		if _, found := desiredByID[chunk.ID]; !found && isBaseChunk(chunk.ChunkType) {
			plan.Delete = append(plan.Delete, chunk)
		}
	}

	return plan, nil
}

func chunkIDs(chunks []*types.Chunk) []string {
	ids := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		if chunk != nil {
			ids = append(ids, chunk.ID)
		}
	}
	return ids
}

func linkAdjacentChunks(chunks []*types.Chunk) {
	for index := 1; index < len(chunks); index++ {
		chunks[index-1].NextChunkID = chunks[index].ID
		chunks[index].PreChunkID = chunks[index-1].ID
	}
}

func embeddingFingerprint(metadata types.JSON) (string, error) {
	values, err := metadata.Map()
	if err != nil {
		return "", fmt.Errorf("read desired embedding fingerprint: %w", err)
	}
	fingerprint, _ := values[types.ChunkEmbeddingFingerprintMetadataKey].(string)
	return fingerprint, nil
}

func copyReparseOwnedFields(desired, existing *types.Chunk) {
	desired.SeqID = existing.SeqID
	desired.CreatedAt = existing.CreatedAt
	desired.Flags = existing.Flags
	desired.Status = existing.Status
	desired.RelationChunks = append(types.JSON(nil), existing.RelationChunks...)
	desired.IndirectRelationChunks = append(types.JSON(nil), existing.IndirectRelationChunks...)
	desired.ImageInfo = existing.ImageInfo
	desired.Metadata = append(types.JSON(nil), existing.Metadata...)
}

func isBaseChunk(chunkType types.ChunkType) bool {
	return chunkType == types.ChunkTypeText || chunkType == types.ChunkTypeParentText
}

func isGeneratedChunk(chunkType types.ChunkType) bool {
	switch chunkType {
	case types.ChunkTypeSummary,
		types.ChunkTypeImageOCR,
		types.ChunkTypeImageCaption,
		types.ChunkTypeEntity,
		types.ChunkTypeRelationship:
		return true
	default:
		return false
	}
}
