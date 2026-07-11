package service

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

type rebuildRunCtxKey struct{}

func withRebuildRun(ctx context.Context, runID string) context.Context {
	if runID == "" {
		return ctx
	}
	return context.WithValue(ctx, rebuildRunCtxKey{}, runID)
}

func rebuildRunFromCtx(ctx context.Context) string {
	runID, _ := ctx.Value(rebuildRunCtxKey{}).(string)
	return runID
}

func multimodalPendingKey(knowledgeID, rebuildRunID string) string {
	if rebuildRunID == "" {
		return fmt.Sprintf("multimodal:pending:%s", knowledgeID)
	}
	return fmt.Sprintf("multimodal:pending:%s:%s", knowledgeID, rebuildRunID)
}

type rebuildConfigSnapshot struct {
	EffectiveConfig  types.EffectiveProcessConfig `json:"effective_config"`
	ParserOverrides  map[string]string            `json:"parser_overrides,omitempty"`
	EmbeddingModelID string                       `json:"embedding_model_id"`
	SummaryModelID   string                       `json:"summary_model_id"`
	WikiEnabled      bool                         `json:"wiki_enabled"`
}

func rebuildConfigFingerprint(
	kb *types.KnowledgeBase,
	eff types.EffectiveProcessConfig,
	overrides *types.KnowledgeProcessOverrides,
	_ *string,
) string {
	// This fingerprint is intentionally the downstream-artifact fingerprint,
	// not a whole-pipeline fingerprint. Embedding has its own model/dimension
	// cache and VLM invalidation is decided from the actual OCR/Caption chunk
	// diff. Including either here would incorrectly force summary, question,
	// graph and Wiki work when their inputs did not change.
	eff.VLMConfig = types.VLMConfig{}
	eff.EnableMultimodel = false
	snapshot := rebuildConfigSnapshot{EffectiveConfig: eff}
	if overrides != nil {
		snapshot.ParserOverrides = overrides.ParserEngineOverrides
	}
	if kb != nil {
		snapshot.EmbeddingModelID = ""
		snapshot.SummaryModelID = kb.SummaryModelID
		snapshot.WikiEnabled = kb.IsWikiEnabled()
	}
	return jsonStableHash(snapshot)
}

func (s *knowledgeService) startRebuildRun(
	ctx context.Context,
	knowledge *types.Knowledge,
	kb *types.KnowledgeBase,
	attempt int,
	oldOverrides, newOverrides *types.KnowledgeProcessOverrides,
) (*types.KnowledgeRebuildRun, error) {
	if s.rebuildRunRepo == nil {
		return nil, nil
	}
	oldEff := ResolveProcessConfig(kb, oldOverrides)
	newEff := ResolveProcessConfig(kb, newOverrides)
	oldChunkCount := -1
	if s.chunkService != nil {
		chunks, err := s.chunkService.ListChunksByKnowledgeID(ctx, knowledge.ID)
		if err != nil {
			logger.Warnf(ctx, "[RebuildRun] Failed to snapshot old chunks for %s: %v", knowledge.ID, err)
		} else {
			oldChunkCount = len(chunks)
		}
	}
	run := &types.KnowledgeRebuildRun{
		TenantID:             knowledge.TenantID,
		KnowledgeID:          knowledge.ID,
		Attempt:              attempt,
		Status:               types.RebuildRunStatusPending,
		OldParseStatus:       knowledge.ParseStatus,
		OldEnableStatus:      knowledge.EnableStatus,
		OldEmbeddingModelID:  knowledge.EmbeddingModelID,
		OldChunkCount:        oldChunkCount,
		OldConfigFingerprint: rebuildConfigFingerprint(kb, oldEff, oldOverrides, &knowledge.EmbeddingModelID),
		NewConfigFingerprint: rebuildConfigFingerprint(kb, newEff, newOverrides, nil),
	}
	if err := s.rebuildRunRepo.Start(ctx, run); err != nil {
		return nil, fmt.Errorf("start rebuild run: %w", err)
	}
	logger.Infof(ctx, "[RebuildRun] Started run=%s knowledge=%s attempt=%d old_chunks=%d",
		run.ID, knowledge.ID, attempt, oldChunkCount)
	return run, nil
}

func (s *knowledgeService) currentRebuildRun(
	ctx context.Context,
	tenantID uint64,
	knowledgeID, runID string,
	attempt int,
) bool {
	if runID == "" || s.rebuildRunRepo == nil {
		return true
	}
	current, err := s.rebuildRunRepo.IsCurrent(ctx, tenantID, knowledgeID, runID, attempt)
	if err != nil {
		logger.Warnf(ctx, "[RebuildRun] Failed to validate run=%s knowledge=%s: %v", runID, knowledgeID, err)
		return false
	}
	return current
}

func (s *knowledgeService) bindRebuildRunAttempt(
	ctx context.Context, tenantID uint64, runID string, attempt int,
) {
	if runID == "" || attempt <= 0 || s.rebuildRunRepo == nil {
		return
	}
	if err := s.rebuildRunRepo.BindAttempt(ctx, tenantID, runID, attempt); err != nil {
		logger.Warnf(ctx, "[RebuildRun] Failed to bind run=%s to attempt=%d: %v", runID, attempt, err)
	}
}

func (s *knowledgeService) setRebuildRunStatus(
	ctx context.Context,
	tenantID uint64,
	runID, status, errorMessage string,
) {
	if runID == "" || s.rebuildRunRepo == nil {
		return
	}
	if err := s.rebuildRunRepo.SetStatus(ctx, tenantID, runID, status, errorMessage); err != nil {
		logger.Warnf(ctx, "[RebuildRun] Failed to set run=%s status=%s: %v", runID, status, err)
	}
}

func (s *knowledgeService) recordRebuildParseResult(
	ctx context.Context,
	payload types.DocumentProcessPayload,
	cacheKey string,
	cacheHit, success, terminal bool,
	err error,
) {
	if payload.RebuildRunID == "" || s.rebuildRunRepo == nil {
		return
	}
	errorMessage := ""
	if err != nil {
		errorMessage = err.Error()
	}
	if recordErr := s.rebuildRunRepo.RecordParseResult(
		ctx,
		payload.TenantID,
		payload.RebuildRunID,
		cacheKey,
		cacheHit,
		success,
		terminal,
		errorMessage,
	); recordErr != nil {
		logger.Warnf(ctx, "[RebuildRun] Failed to record parse result run=%s: %v", payload.RebuildRunID, recordErr)
	}
}

func (s *knowledgeService) replaceRebuildChunkResults(
	ctx context.Context,
	tenantID uint64,
	runID string,
	results []*types.KnowledgeRebuildChunkResult,
) error {
	if runID == "" || s.rebuildRunRepo == nil {
		return nil
	}
	if err := s.rebuildRunRepo.ReplaceChunkResults(ctx, tenantID, runID, results); err != nil {
		return fmt.Errorf("replace rebuild chunk results: %w", err)
	}
	return nil
}

func (s *knowledgeService) upsertRebuildChunkResults(
	ctx context.Context,
	tenantID uint64,
	runID string,
	results []*types.KnowledgeRebuildChunkResult,
) error {
	if runID == "" || s.rebuildRunRepo == nil || len(results) == 0 {
		return nil
	}
	if err := s.rebuildRunRepo.UpsertChunkResults(ctx, tenantID, runID, results); err != nil {
		return fmt.Errorf("upsert rebuild chunk results: %w", err)
	}
	return nil
}

func (s *knowledgeService) beginRebuildImages(ctx context.Context, tenantID uint64, runID string, total int) {
	if runID == "" || s.rebuildRunRepo == nil {
		return
	}
	if err := s.rebuildRunRepo.BeginImages(ctx, tenantID, runID, total); err != nil {
		logger.Warnf(ctx, "[RebuildRun] Failed to begin image stage run=%s total=%d: %v", runID, total, err)
	}
}

func (s *knowledgeService) recordRebuildImageResult(
	ctx context.Context,
	tenantID uint64,
	runID string,
	imageIndex int,
	ocrCacheHit, captionCacheHit, success bool,
	err error,
) {
	if runID == "" || s.rebuildRunRepo == nil {
		return
	}
	errorMessage := ""
	if err != nil {
		errorMessage = err.Error()
	}
	if _, recordErr := s.rebuildRunRepo.RecordImageResult(
		ctx, tenantID, runID, imageIndex, "", "", ocrCacheHit, captionCacheHit, success, errorMessage,
	); recordErr != nil {
		logger.Warnf(ctx, "[RebuildRun] Failed to record image result run=%s image=%d: %v",
			runID, imageIndex, recordErr)
	}
}
