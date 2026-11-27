package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/hibiken/asynq"

	"github.com/Tencent/WeKnora/internal/application/service/retriever"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// reembedWindow bounds how many chunks are re-read, deleted and re-indexed
// together, which keeps each embedding batch, the staleness of the chunks
// read and the gap between the delete and the re-index small.
const reembedWindow = 100

// ProcessKnowledgeListReembed rebuilds the index entries of a list of
// knowledge from the chunks and generated questions stored in the database.
// Only the embedding model is called: nothing is re-parsed, and chunks,
// questions, summaries, OCR text, captions and images are left as they are.
func (s *knowledgeService) ProcessKnowledgeListReembed(ctx context.Context, t *asynq.Task) error {
	var payload types.KnowledgeListReembedPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		logger.Errorf(ctx, "Failed to unmarshal knowledge list re-embed payload: %v", err)
		return err
	}
	ctx = payload.Initiator.Apply(ctx)

	ctx, ids, err := s.reparseTaskScope(ctx, types.KnowledgeListReparsePayload{
		KnowledgeBaseID: payload.KnowledgeBaseID,
		TenantID:        payload.TenantID,
		KnowledgeIDs:    payload.KnowledgeIDs,
	})
	if err != nil || len(ids) == 0 {
		return err
	}
	tenant, err := s.tenantRepo.GetTenantByID(ctx, payload.TenantID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get tenant %d: %v", payload.TenantID, err)
		return err
	}
	ctx = context.WithValue(ctx, types.TenantIDContextKey, payload.TenantID)
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenant)

	failures := make([]error, 0)
	for _, id := range ids {
		if err := s.reembedKnowledge(ctx, id); err != nil {
			failures = append(failures, fmt.Errorf("knowledge %s: %w", secutils.SanitizeForLog(id), err))
		}
	}
	logger.Infof(ctx, "Knowledge list re-embed task finished: %d knowledge, %d failed", len(ids), len(failures))
	// Re-embedding is idempotent, so a failed task is left for asynq to retry
	// as a whole instead of being marked non-retryable.
	return errors.Join(failures...)
}

// reembedKnowledge rebuilds the index entries of one knowledge.
func (s *knowledgeService) reembedKnowledge(ctx context.Context, knowledgeID string) error {
	tenantID := types.MustTenantIDFromContext(ctx)
	knowledge, err := s.repo.GetKnowledgeByID(ctx, tenantID, knowledgeID)
	if err != nil {
		return err
	}
	// A document that is still being parsed is indexed by its own pipeline.
	if knowledge.ParseStatus != types.ParseStatusCompleted {
		logger.Infof(ctx, "Skipping re-embed of knowledge %s in parse status %s", knowledge.ID, knowledge.ParseStatus)
		return nil
	}
	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, knowledge.KnowledgeBaseID)
	if err != nil {
		return err
	}
	if !kb.NeedsEmbeddingModel() {
		return nil
	}

	if kb.Type == types.KnowledgeBaseTypeFAQ {
		chunks, err := s.chunkRepo.ListChunksByKnowledgeIDAndTypes(
			ctx, tenantID, knowledge.ID, []types.ChunkType{types.ChunkTypeFAQ})
		if err != nil {
			return err
		}
		embeddingModel, err := s.modelService.GetEmbeddingModel(ctx, kb.EmbeddingModelID)
		if err != nil {
			return err
		}
		engine, err := retriever.CreateRetrieveEngineForKB(
			ctx, s.retrieveEngine, s.ownership, tenantID, kb.VectorStoreID)
		if err != nil {
			return err
		}
		return s.reembedWindows(ctx, tenantID, chunks, func(window []*types.Chunk) error {
			return s.reembedFAQChunks(ctx, engine, embeddingModel, kb, window)
		})
	}

	// Text chunks only: OCR, caption, summary and image vector chunks have a
	// single index entry of their own, which re-indexing would not change.
	// updateChunkVector writes the chunk text and its stored questions, and
	// leaves disabled chunks without entries, as the chunk enable switch does.
	chunks, err := s.chunkRepo.ListChunksByKnowledgeID(ctx, tenantID, knowledge.ID)
	if err != nil {
		return err
	}
	return s.reembedWindows(ctx, tenantID, chunks, func(window []*types.Chunk) error {
		return s.updateChunkVector(ctx, kb.ID, window)
	})
}

// reembedWindows re-reads the chunks one window at a time right before
// re-indexing them, so an edit or delete made while the task runs is not
// overwritten with an earlier copy.
func (s *knowledgeService) reembedWindows(
	ctx context.Context, tenantID uint64, chunks []*types.Chunk, reindex func([]*types.Chunk) error,
) error {
	for window := range slices.Chunk(chunks, reembedWindow) {
		ids := make([]string, 0, len(window))
		for _, chunk := range window {
			ids = append(ids, chunk.ID)
		}
		current, err := s.chunkRepo.ListChunksByID(ctx, tenantID, ids)
		if err != nil {
			return err
		}
		if len(current) == 0 {
			continue
		}
		if err := reindex(current); err != nil {
			return err
		}
	}
	return nil
}

// reembedFAQChunks replaces the index entries of FAQ chunks with the entries
// FAQ indexing builds for them, without touching the knowledge row.
func (s *knowledgeService) reembedFAQChunks(
	ctx context.Context,
	engine *retriever.CompositeRetrieveEngine,
	embedder embedding.Embedder,
	kb *types.KnowledgeBase,
	chunks []*types.Chunk,
) error {
	ids := make([]string, 0, len(chunks))
	infos := make([]*types.IndexInfo, 0, len(chunks))
	for _, chunk := range chunks {
		entries, err := s.buildFAQIndexInfoList(ctx, kb, chunk)
		if err != nil {
			return err
		}
		ids = append(ids, chunk.ID)
		infos = append(infos, entries...)
	}
	if err := engine.DeleteByChunkIDList(ctx, ids, embedder.GetDimensions(), types.KnowledgeTypeFAQ); err != nil {
		return err
	}
	return engine.BatchIndex(ctx, embedder, infos)
}
