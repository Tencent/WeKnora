package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Tencent/WeKnora/internal/application/service/retriever"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// Manual chunk surgery: create / merge / split on parsed documents (#5675).
// All three paths mirror UpdateDocumentChunk semantics: explicit KB write
// grant via loadKnowledgeWrite, optimistic revision snapshots on edited
// survivors, index_status=processing -> ready|failed transitions, and
// retrieval-index synchronization after every persisted write.

const manualChunkMergeSeparator = "\n\n"

func validateManualChunkContent(content string) (string, error) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return "", fmt.Errorf("chunk content cannot be empty")
	}
	if len(trimmed) > maxEditableChunkLength {
		return "", fmt.Errorf("chunk content exceeds %d bytes", maxEditableChunkLength)
	}
	return trimmed, nil
}

func (s *chunkService) saveChunkIndexStatus(ctx context.Context, chunk *types.Chunk) error {
	return s.chunkRepository.UpdateChunkFieldsByIDs(ctx, chunk.TenantID, []string{chunk.ID},
		map[string]interface{}{"index_status": chunk.IndexStatus})
}

// CreateManualChunk appends a user-authored text chunk to a document.
func (s *chunkService) CreateManualChunk(
	ctx context.Context, knowledgeID string, content string, isEnabled *bool,
) (*types.Chunk, error) {
	knowledge, _, err := loadKnowledgeWrite(ctx, s.knowledgeRepo, s.kbRepository, knowledgeID)
	if err != nil {
		return nil, err
	}
	trimmed, err := validateManualChunkContent(content)
	if err != nil {
		return nil, err
	}
	enabled := true
	if isEnabled != nil {
		enabled = *isEnabled
	}
	nextIndex, err := s.chunkRepository.NextChunkIndex(ctx, knowledge.TenantID, knowledge.ID)
	if err != nil {
		return nil, err
	}
	actorID, _ := types.UserIDFromContext(ctx)
	chunk := &types.Chunk{
		ID: uuid.NewString(), TenantID: knowledge.TenantID,
		KnowledgeID: knowledge.ID, KnowledgeBaseID: knowledge.KnowledgeBaseID,
		ChunkIndex: nextIndex,
		Content:    trimmed, SourceContent: trimmed, ContentRevision: 0,
		ChunkType: types.ChunkTypeText, IsEnabled: enabled,
		IndexStatus: "processing", LastEditorID: actorID, UpdatedAt: time.Now(),
	}
	if err := s.chunkRepository.CreateChunks(ctx, []*types.Chunk{chunk}); err != nil {
		return nil, err
	}
	if err := s.syncEditedChunkIndex(ctx, chunk); err != nil {
		chunk.IndexStatus = "failed"
		_ = s.saveChunkIndexStatus(ctx, chunk)
		logger.Warnf(ctx, "Manual chunk %s saved but reindex failed: %v", chunk.ID, err)
		return chunk, nil
	}
	chunk.IndexStatus = "ready"
	if err := s.saveChunkIndexStatus(ctx, chunk); err != nil {
		return nil, err
	}
	return chunk, nil
}

func (s *chunkService) loadOrderedChunksForMerge(
	ctx context.Context, chunkIDs []string, expectedRevisions []int,
) ([]*types.Chunk, error) {
	tenant, err := writeExecutionTenant(ctx)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	unique := make([]string, 0, len(chunkIDs))
	for _, id := range chunkIDs {
		if !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}
	stored, err := s.chunkRepository.ListChunksByID(ctx, tenant, unique)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]*types.Chunk, len(stored))
	for _, row := range stored {
		byID[row.ID] = row
	}
	ordered := make([]*types.Chunk, 0, len(unique))
	for _, id := range unique {
		row, ok := byID[id]
		if !ok {
			return nil, apperrors.NewNotFoundError("chunk not found")
		}
		ordered = append(ordered, row)
	}
	for i, row := range ordered {
		if expectedRevisions != nil && i < len(expectedRevisions) {
			if row.ContentRevision != expectedRevisions[i] {
				return nil, ErrChunkRevisionConflict
			}
		}
	}
	return ordered, nil
}

// MergeChunks folds 2..N adjacent text chunks of one document into the
// first one. The survivor keeps the first id, concatenates bodies in
// document order, bumps its revision with a pre-merge snapshot, and the
// absorbed rows are deleted afterwards.
func (s *chunkService) MergeChunks(
	ctx context.Context, knowledgeID string, chunkIDs []string, expectedRevisions []int,
) (*types.Chunk, error) {
	if len(chunkIDs) < 2 {
		return nil, fmt.Errorf("merging requires at least two chunks")
	}
	if _, _, err := loadKnowledgeWrite(ctx, s.knowledgeRepo, s.kbRepository, knowledgeID); err != nil {
		return nil, err
	}
	chunks, err := s.loadOrderedChunksForMerge(ctx, chunkIDs, expectedRevisions)
	if err != nil {
		return nil, err
	}
	sort.Slice(chunks, func(i, j int) bool { return chunks[i].ChunkIndex < chunks[j].ChunkIndex })
	survivor := chunks[0]
	for i, chunk := range chunks {
		if chunk.KnowledgeID != knowledgeID {
			return nil, apperrors.NewForbiddenError("chunk does not belong to the target document")
		}
		if chunk.ChunkType != types.ChunkTypeText {
			return nil, fmt.Errorf("only text chunks can be merged")
		}
		if i > 0 && chunk.ChunkIndex != chunks[i-1].ChunkIndex+1 {
			return nil, fmt.Errorf("chunks must be adjacent in the document order")
		}
	}
	partsBody := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		partsBody = append(partsBody, chunk.Content)
	}
	merged := strings.Join(partsBody, manualChunkMergeSeparator)
	if len(merged) > maxEditableChunkLength {
		return nil, fmt.Errorf("merged content exceeds %d bytes", maxEditableChunkLength)
	}
	actorID, _ := types.UserIDFromContext(ctx)
	now := time.Now()
	oldRevision := survivor.ContentRevision
	snapshot := &types.ChunkRevision{
		ID: uuid.NewString(), TenantID: survivor.TenantID,
		KnowledgeBaseID: survivor.KnowledgeBaseID, KnowledgeID: survivor.KnowledgeID,
		ChunkID: survivor.ID, Revision: oldRevision, Content: survivor.Content,
		IsEnabled: survivor.IsEnabled, EditorID: survivor.LastEditorID,
		EditSource: "user", EditedAt: survivor.UpdatedAt, CreatedAt: now,
	}
	if survivor.SourceContent == "" {
		survivor.SourceContent = survivor.Content
	}
	survivor.SourceLocators = nil
	survivor.Content = merged
	survivor.ContentRevision++
	survivor.LastEditorID = actorID
	survivor.IndexStatus = "processing"
	survivor.UpdatedAt = now
	if err := s.chunkRepository.SaveChunkRevision(ctx, survivor, snapshot, oldRevision); err != nil {
		return nil, err
	}
	absorbedIDs := make([]string, 0, len(chunks)-1)
	for _, chunk := range chunks[1:] {
		absorbedIDs = append(absorbedIDs, chunk.ID)
	}
	if err := s.deleteChunkIndexes(ctx, chunks[1:]); err != nil {
		logger.Warnf(ctx, "Merged chunk %s saved but absorbed index cleanup failed: %v", survivor.ID, err)
	}
	// Absorbed rows are contiguous right after the survivor, so the rows that
	// follow them shift down by the absorbed count to keep chunk_index
	// contiguous; a later merge of the next pair depends on that.
	compactFrom := survivor.ChunkIndex + 1 + len(absorbedIDs)
	if err := s.chunkRepository.MergeChunkRows(
		ctx, survivor.TenantID, survivor.KnowledgeID, absorbedIDs, compactFrom, -len(absorbedIDs),
	); err != nil {
		return nil, err
	}
	if err := s.syncEditedChunkIndex(ctx, survivor); err != nil {
		survivor.IndexStatus = "failed"
		_ = s.saveChunkIndexStatus(ctx, survivor)
		return survivor, nil
	}
	survivor.IndexStatus = "ready"
	if err := s.saveChunkIndexStatus(ctx, survivor); err != nil {
		return nil, err
	}
	return survivor, nil
}

// SplitChunk divides one text chunk at a rune offset. The original row
// keeps its id and the prefix body; the suffix becomes a new row.
func (s *chunkService) SplitChunk(
	ctx context.Context, chunkID string, splitAt int, expectedRevision *int, isEnabled *bool,
) ([]*types.Chunk, error) {
	chunk, err := s.writableChunk(ctx, chunkID)
	if err != nil {
		return nil, err
	}
	if chunk.ChunkType != types.ChunkTypeText {
		return nil, fmt.Errorf("only text chunks can be split")
	}
	if expectedRevision != nil && *expectedRevision != chunk.ContentRevision {
		return nil, ErrChunkRevisionConflict
	}
	runes := []rune(chunk.Content)
	if splitAt <= 0 || splitAt >= len(runes) {
		return nil, fmt.Errorf("split offset must be inside the chunk body")
	}
	prefix := strings.TrimSpace(string(runes[:splitAt]))
	suffix := strings.TrimSpace(string(runes[splitAt:]))
	if prefix == "" || suffix == "" {
		return nil, fmt.Errorf("split would produce an empty half")
	}
	actorID, _ := types.UserIDFromContext(ctx)
	now := time.Now()
	oldRevision := chunk.ContentRevision
	snapshot := &types.ChunkRevision{
		ID: uuid.NewString(), TenantID: chunk.TenantID,
		KnowledgeBaseID: chunk.KnowledgeBaseID, KnowledgeID: chunk.KnowledgeID,
		ChunkID: chunk.ID, Revision: oldRevision, Content: chunk.Content,
		IsEnabled: chunk.IsEnabled, EditorID: chunk.LastEditorID,
		EditSource: "user", EditedAt: chunk.UpdatedAt, CreatedAt: now,
	}
	suffixEnabled := chunk.IsEnabled
	if isEnabled != nil {
		suffixEnabled = *isEnabled
	}
	tail := &types.Chunk{
		ID: uuid.NewString(), TenantID: chunk.TenantID,
		KnowledgeID: chunk.KnowledgeID, KnowledgeBaseID: chunk.KnowledgeBaseID,
		Content: suffix, SourceContent: suffix, ContentRevision: 0,
		ChunkType: types.ChunkTypeText, IsEnabled: suffixEnabled,
		ChunkIndex: chunk.ChunkIndex + 1, StartAt: chunk.StartAt + splitAt, EndAt: chunk.EndAt,
		IndexStatus: "processing", LastEditorID: actorID, UpdatedAt: now,
	}
	if chunk.SourceContent == "" {
		chunk.SourceContent = chunk.Content
	}
	chunk.SourceLocators = nil
	chunk.Content = prefix
	chunk.ContentRevision++
	chunk.LastEditorID = actorID
	chunk.IndexStatus = "processing"
	chunk.UpdatedAt = now
	if err := s.chunkRepository.SaveChunkRevision(ctx, chunk, snapshot, oldRevision); err != nil {
		return nil, err
	}
	// Make room for the suffix: every chunk after the split point moves one
	// slot down so chunk_index stays contiguous and unique.
	if err := s.chunkRepository.SplitChunkRows(
		ctx, chunk.TenantID, chunk.KnowledgeID, tail.ChunkIndex, tail,
	); err != nil {
		return nil, err
	}
	for _, half := range []*types.Chunk{chunk, tail} {
		if err := s.syncEditedChunkIndex(ctx, half); err != nil {
			half.IndexStatus = "failed"
			_ = s.saveChunkIndexStatus(ctx, half)
			continue
		}
		half.IndexStatus = "ready"
		if err := s.saveChunkIndexStatus(ctx, half); err != nil {
			return nil, err
		}
	}
	return []*types.Chunk{chunk, tail}, nil
}

// deleteChunkIndexes removes the retrieval entries of chunks that are
// being removed without deleting their rows (merge absorption).
func (s *chunkService) deleteChunkIndexes(ctx context.Context, chunks []*types.Chunk) error {
	for _, chunk := range chunks {
		kb, err := s.kbRepository.GetKnowledgeBaseByID(ctx, chunk.KnowledgeBaseID)
		if err != nil {
			return err
		}
		if !kb.NeedsEmbeddingModel() {
			continue
		}
		embedder, err := s.modelService.GetEmbeddingModel(ctx, kb.EmbeddingModelID)
		if err != nil {
			return err
		}
		engine, err := retriever.CreateRetrieveEngineForKB(
			ctx, s.retrieveEngine, s.ownership, chunk.TenantID, kb.VectorStoreID)
		if err != nil {
			return err
		}
		if err := engine.DeleteByChunkIDList(ctx, []string{chunk.ID}, embedder.GetDimensions(), kb.Type); err != nil {
			return err
		}
	}
	return nil
}
