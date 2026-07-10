package service

import (
	"sort"

	"github.com/Tencent/WeKnora/internal/types"
)

type rebuildPostProcessPlan struct {
	CurrentTextLike []*types.Chunk
	CurrentText     []*types.Chunk
	QuestionTargets []*types.Chunk
	GraphTargets    []*types.Chunk
	SummaryRequired bool
	WikiRequired    bool
	ContentChanged  bool
	ConfigChanged   bool
}

func buildRebuildPostProcessPlan(
	run *types.KnowledgeRebuildRun,
	results []*types.KnowledgeRebuildChunkResult,
	chunks []*types.Chunk,
	kb *types.KnowledgeBase,
	eff types.EffectiveProcessConfig,
) rebuildPostProcessPlan {
	plan := rebuildPostProcessPlan{}
	if run == nil {
		return plan
	}
	plan.ConfigChanged = run.OldConfigFingerprint != run.NewConfigFingerprint

	chunkByID := make(map[string]*types.Chunk, len(chunks))
	for _, chunk := range chunks {
		if chunk != nil && chunk.ID != "" {
			chunkByID[chunk.ID] = chunk
		}
	}
	changed := make(map[string]struct{})
	for _, result := range results {
		if result == nil {
			continue
		}
		isTextLike := result.ChunkType == types.ChunkTypeText ||
			result.ChunkType == types.ChunkTypeImageOCR ||
			result.ChunkType == types.ChunkTypeImageCaption
		if !isTextLike {
			continue
		}
		if result.Classification == types.RebuildChunkClassChangedNew ||
			result.Classification == types.RebuildChunkClassStale {
			plan.ContentChanged = true
		}
		if result.Classification == types.RebuildChunkClassChangedNew {
			changed[result.ChunkID] = struct{}{}
		}
		if result.Classification == types.RebuildChunkClassStale {
			continue
		}
		if chunk := chunkByID[result.ChunkID]; chunk != nil {
			plan.CurrentTextLike = append(plan.CurrentTextLike, chunk)
			if chunk.ChunkType == types.ChunkTypeText {
				plan.CurrentText = append(plan.CurrentText, chunk)
			}
		}
	}

	sort.Slice(plan.CurrentTextLike, func(i, j int) bool {
		if plan.CurrentTextLike[i].StartAt == plan.CurrentTextLike[j].StartAt {
			return plan.CurrentTextLike[i].ChunkIndex < plan.CurrentTextLike[j].ChunkIndex
		}
		return plan.CurrentTextLike[i].StartAt < plan.CurrentTextLike[j].StartAt
	})
	sort.Slice(plan.CurrentText, func(i, j int) bool {
		if plan.CurrentText[i].StartAt == plan.CurrentText[j].StartAt {
			return plan.CurrentText[i].ChunkIndex < plan.CurrentText[j].ChunkIndex
		}
		return plan.CurrentText[i].StartAt < plan.CurrentText[j].StartAt
	})

	forceAll := plan.ConfigChanged
	if kb != nil && kb.NeedsEmbeddingModel() && eff.QuestionGenerationConfig.Enabled {
		for _, chunk := range plan.CurrentText {
			_, isChanged := changed[chunk.ID]
			if forceAll || isChanged {
				plan.QuestionTargets = append(plan.QuestionTargets, chunk)
			}
		}
	}
	if eff.GraphEnabled {
		for _, chunk := range plan.CurrentTextLike {
			_, isChanged := changed[chunk.ID]
			if forceAll || isChanged {
				plan.GraphTargets = append(plan.GraphTargets, chunk)
			}
		}
	}
	plan.SummaryRequired = plan.ContentChanged || plan.ConfigChanged
	plan.WikiRequired = kb != nil && kb.IndexingStrategy.WikiEnabled && plan.SummaryRequired
	return plan
}

type selectiveQuestionBatch struct {
	Chunks      []*types.Chunk
	PrevChunkID string
	NextChunkID string
}

func planSelectiveQuestionBatches(
	allChunks, targets []*types.Chunk,
	batchSize int,
) []selectiveQuestionBatch {
	if len(allChunks) == 0 || len(targets) == 0 {
		return nil
	}
	if batchSize <= 0 {
		batchSize = 1
	}
	targetIDs := make(map[string]struct{}, len(targets))
	for _, chunk := range targets {
		if chunk != nil {
			targetIDs[chunk.ID] = struct{}{}
		}
	}
	var batches []selectiveQuestionBatch
	for start := 0; start < len(allChunks); {
		if _, ok := targetIDs[allChunks[start].ID]; !ok {
			start++
			continue
		}
		runEnd := start
		for runEnd < len(allChunks) {
			if _, ok := targetIDs[allChunks[runEnd].ID]; !ok {
				break
			}
			runEnd++
		}
		for batchStart := start; batchStart < runEnd; batchStart += batchSize {
			batchEnd := batchStart + batchSize
			if batchEnd > runEnd {
				batchEnd = runEnd
			}
			batch := selectiveQuestionBatch{Chunks: allChunks[batchStart:batchEnd]}
			if batchStart > 0 {
				batch.PrevChunkID = allChunks[batchStart-1].ID
			}
			if batchEnd < len(allChunks) {
				batch.NextChunkID = allChunks[batchEnd].ID
			}
			batches = append(batches, batch)
		}
		start = runEnd
	}
	return batches
}
