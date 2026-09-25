package service

import (
	"context"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/application/service/retriever"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

type faqStatusSyncPlan struct {
	Pairs   []types.FAQChunkSyncPair
	SrcByID map[string]*types.FAQChunkStatus
	DstByID map[string]*types.FAQChunkStatus
}

func (s *knowledgeService) buildFAQStatusSyncPlan(
	ctx context.Context,
	srcTenantID, dstTenantID uint64,
	matched []types.FAQChunkSyncPair,
	resolveTag func(srcTagID string) string,
) (*faqStatusSyncPlan, error) {
	if len(matched) == 0 {
		return &faqStatusSyncPlan{}, nil
	}
	srcIDs := make([]string, 0, len(matched))
	dstIDs := make([]string, 0, len(matched))
	for _, p := range matched {
		srcIDs = append(srcIDs, p.SrcChunkID)
		dstIDs = append(dstIDs, p.DstChunkID)
	}
	srcByID, err := s.chunkRepo.ListFAQChunkStatusByIDs(ctx, srcTenantID, srcIDs)
	if err != nil {
		return nil, err
	}
	dstByID, err := s.chunkRepo.ListFAQChunkStatusByIDs(ctx, dstTenantID, dstIDs)
	if err != nil {
		return nil, err
	}
	pairs := make([]types.FAQChunkSyncPair, 0)
	for _, p := range matched {
		src, dst := srcByID[p.SrcChunkID], dstByID[p.DstChunkID]
		if src == nil || dst == nil {
			continue
		}
		mappedTag := ""
		if src.TagID != "" {
			mappedTag = resolveTag(src.TagID)
		}
		if types.FAQChunkNeedsStatusSync(src, dst, mappedTag) {
			pairs = append(pairs, p)
		}
	}
	return &faqStatusSyncPlan{Pairs: pairs, SrcByID: srcByID, DstByID: dstByID}, nil
}

func (s *knowledgeService) syncFAQChunkStatusBatch(
	ctx context.Context,
	dstKB *types.KnowledgeBase,
	pairs []types.FAQChunkSyncPair,
	srcByID, dstByID map[string]*types.FAQChunkStatus,
	resolveTag func(srcTagID string) string,
) error {
	if len(pairs) == 0 {
		return nil
	}
	tenantID := types.MustTenantIDFromContext(ctx)
	enabledUpdates := make(map[string]bool)
	recommendedUpdates := make(map[string]bool)
	tagUpdates := make(map[string]string)
	rows := make([]*types.Chunk, 0, len(pairs))
	metadataIDs := make([]string, 0)

	for _, p := range pairs {
		src, dst := srcByID[p.SrcChunkID], dstByID[p.DstChunkID]
		if src == nil || dst == nil {
			continue
		}
		mappedTag := ""
		if src.TagID != "" {
			mappedTag = resolveTag(src.TagID)
		}
		if !types.FAQChunkNeedsStatusSync(src, dst, mappedTag) {
			continue
		}
		rows = append(rows, &types.Chunk{
			ID: dst.ID, IsEnabled: src.IsEnabled, Flags: src.Flags,
			TagID: mappedTag, UpdatedAt: time.Now(),
		})
		if dst.IsEnabled != src.IsEnabled {
			enabledUpdates[dst.ID] = src.IsEnabled
		}
		srcRec := src.Flags.HasFlag(types.ChunkFlagRecommended)
		dstRec := dst.Flags.HasFlag(types.ChunkFlagRecommended)
		if srcRec != dstRec {
			recommendedUpdates[dst.ID] = srcRec
		}
		if mappedTag != dst.TagID {
			tagUpdates[dst.ID] = mappedTag
		}
		if types.NormalizeAnswerStrategy(src.AnswerStrategy) != types.NormalizeAnswerStrategy(dst.AnswerStrategy) {
			metadataIDs = append(metadataIDs, dst.ID)
		}
	}
	if len(rows) > 0 {
		if err := s.chunkRepo.UpdateChunks(ctx, rows); err != nil {
			return err
		}
	}
	for _, dstID := range metadataIDs {
		src := srcForDst(pairs, srcByID, dstID)
		dst := dstByID[dstID]
		if src == nil || dst == nil {
			continue
		}
		patched, err := types.PatchFAQAnswerStrategy(dst.Metadata, src.Metadata)
		if err != nil {
			return fmt.Errorf("patch answer_strategy for %s: %w", dstID, err)
		}
		full, err := s.chunkRepo.GetChunkByID(ctx, tenantID, dstID)
		if err != nil {
			return err
		}
		full.Metadata = patched
		full.UpdatedAt = time.Now()
		if err := s.chunkRepo.UpdateChunk(ctx, full); err != nil {
			return err
		}
	}
	if len(enabledUpdates) == 0 && len(tagUpdates) == 0 && len(recommendedUpdates) == 0 {
		return nil
	}
	engine, err := retriever.CreateRetrieveEngineForKB(
		ctx, s.retrieveEngine, s.ownership, tenantID, dstKB.VectorStoreID)
	if err != nil {
		return err
	}
	if len(enabledUpdates) > 0 {
		if err := engine.BatchUpdateChunkEnabledStatus(ctx, enabledUpdates); err != nil {
			return err
		}
	}
	if len(tagUpdates) > 0 {
		if err := engine.BatchUpdateChunkTagID(ctx, tagUpdates); err != nil {
			return err
		}
	}
	if len(recommendedUpdates) > 0 {
		// Vector-store recommended flag sync is not yet available on all backends in
		// this branch; DB flags were already updated via UpdateChunks above.
		logger.Warnf(ctx, "FAQ clone sync: skipped vector recommended update for %d chunks (DB flags updated)", len(recommendedUpdates))
	}
	return nil
}

func srcForDst(pairs []types.FAQChunkSyncPair, srcByID map[string]*types.FAQChunkStatus, dstID string) *types.FAQChunkStatus {
	for _, p := range pairs {
		if p.DstChunkID == dstID {
			return srcByID[p.SrcChunkID]
		}
	}
	return nil
}
