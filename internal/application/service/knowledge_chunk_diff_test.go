package service

import (
	"fmt"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func rebuildTestChunk(id, content string, chunkType types.ChunkType) *types.Chunk {
	return &types.Chunk{
		ID:              id,
		TenantID:        1,
		KnowledgeID:     "knowledge-1",
		KnowledgeBaseID: "kb-1",
		Content:         content,
		ChunkType:       chunkType,
		ChunkIndex:      1,
		IsEnabled:       true,
		Flags:           types.ChunkFlagRecommended,
		StartAt:         10,
		EndAt:           20,
	}
}

func TestClassifyChunkCandidates(t *testing.T) {
	unchanged := rebuildTestChunk("unchanged", "same", types.ChunkTypeText)
	unchanged.Status = int(types.ChunkStatusIndexed)
	unchanged.Metadata = types.JSON(`{"summary":"derived"}`)
	unchanged.RelationChunks = types.JSON(`["related"]`)

	metadataOld := rebuildTestChunk("metadata", "same metadata content", types.ChunkTypeText)
	metadataOld.NextChunkID = "old-next"
	metadataCandidate := rebuildTestChunk("metadata", "same metadata content", types.ChunkTypeText)
	metadataCandidate.NextChunkID = "new-next"

	changedOld := rebuildTestChunk("changed", "old content", types.ChunkTypeImageOCR)
	changedCandidate := rebuildTestChunk("changed", "new content", types.ChunkTypeImageOCR)
	stale := rebuildTestChunk("stale", "removed", types.ChunkTypeImageCaption)
	newCandidate := rebuildTestChunk("new", "brand new", types.ChunkTypeParentText)

	unchangedCandidate := rebuildTestChunk("unchanged", "same", types.ChunkTypeText)
	diff := classifyChunkCandidates(
		[]*types.Chunk{unchanged, metadataOld, changedOld, stale},
		[]*types.Chunk{unchangedCandidate, metadataCandidate, changedCandidate, newCandidate},
	)

	require.Len(t, diff.Unchanged, 1)
	assert.Same(t, unchanged, diff.Unchanged[0])
	require.Len(t, diff.MetadataOnly, 1)
	assert.Same(t, metadataOld, diff.MetadataOnly[0].Existing)
	assert.Same(t, metadataCandidate, diff.MetadataOnly[0].Candidate)
	assert.ElementsMatch(t, []string{"changed", "new"}, chunkIDs(diff.ChangedNew))
	assert.Equal(t, []string{"stale"}, chunkIDs(diff.Stale))
	assert.NotEmpty(t, unchangedCandidate.ContentHash)

	classes := make(map[string]string, len(diff.Results))
	for _, result := range diff.Results {
		classes[result.ChunkID] = result.Classification
		assert.NotEmpty(t, result.ContentFingerprint)
		assert.NotEmpty(t, result.MetadataFingerprint)
	}
	assert.Equal(t, types.RebuildChunkClassUnchanged, classes["unchanged"])
	assert.Equal(t, types.RebuildChunkClassMetadataOnly, classes["metadata"])
	assert.Equal(t, types.RebuildChunkClassChangedNew, classes["changed"])
	assert.Equal(t, types.RebuildChunkClassChangedNew, classes["new"])
	assert.Equal(t, types.RebuildChunkClassStale, classes["stale"])
}

func TestChunkMetadataFingerprintIgnoresDerivedFields(t *testing.T) {
	oldChunk := rebuildTestChunk("same-id", "same content", types.ChunkTypeText)
	oldChunk.SeqID = 42
	oldChunk.Status = int(types.ChunkStatusIndexed)
	oldChunk.Metadata = types.JSON(`{"questions":["q1"]}`)
	oldChunk.RelationChunks = types.JSON(`["r1"]`)
	oldChunk.IndirectRelationChunks = types.JSON(`["r2"]`)
	oldChunk.CreatedAt = time.Unix(100, 0)
	oldChunk.UpdatedAt = time.Unix(200, 0)

	candidate := rebuildTestChunk("same-id", "same content", types.ChunkTypeText)
	diff := classifyChunkCandidates([]*types.Chunk{oldChunk}, []*types.Chunk{candidate})

	assert.Len(t, diff.Unchanged, 1)
	assert.Empty(t, diff.MetadataOnly)
	assert.Empty(t, diff.ChangedNew)
	assert.Empty(t, diff.Stale)
}

func TestClassifyChunkCandidatesReusesOldIDByCanonicalContent(t *testing.T) {
	oldChunk := rebuildTestChunk("old-id", "before ![diagram](local://old/image.png) after", types.ChunkTypeText)
	candidate := rebuildTestChunk("new-id", "before ![diagram](local://new/image.png) after", types.ChunkTypeText)

	diff := classifyChunkCandidates([]*types.Chunk{oldChunk}, []*types.Chunk{candidate})

	require.Len(t, diff.MetadataOnly, 1)
	assert.Equal(t, "old-id", candidate.ID)
	assert.Equal(t, "old-id", diff.IDRewrites["new-id"])
	assert.Empty(t, diff.ChangedNew)
	assert.Empty(t, diff.Stale)
}

func TestClassifyChunkCandidatesRewritesCandidateReferences(t *testing.T) {
	oldParent := rebuildTestChunk("old-parent", "parent", types.ChunkTypeParentText)
	oldChild := rebuildTestChunk("old-child", "child", types.ChunkTypeText)
	oldChild.ParentChunkID = oldParent.ID

	parent := rebuildTestChunk("new-parent", "parent", types.ChunkTypeParentText)
	child := rebuildTestChunk("new-child", "child", types.ChunkTypeText)
	child.ParentChunkID = parent.ID
	parent.NextChunkID = child.ID
	child.PreChunkID = parent.ID

	diff := classifyChunkCandidates([]*types.Chunk{oldParent, oldChild}, []*types.Chunk{parent, child})

	assert.Equal(t, oldParent.ID, parent.ID)
	assert.Equal(t, oldChild.ID, child.ID)
	assert.Equal(t, oldChild.ID, parent.NextChunkID)
	assert.Equal(t, oldParent.ID, child.PreChunkID)
	assert.Equal(t, oldParent.ID, child.ParentChunkID)
	assert.Empty(t, diff.ChangedNew)
	assert.Empty(t, diff.Stale)
}

func TestClassifyChunkCandidatesMatchesDuplicateContentOneToOne(t *testing.T) {
	oldFirst := rebuildTestChunk("old-first", "duplicate", types.ChunkTypeText)
	oldFirst.StartAt = 0
	oldFirst.ChunkIndex = 0
	oldSecond := rebuildTestChunk("old-second", "duplicate", types.ChunkTypeText)
	oldSecond.StartAt = 100
	oldSecond.ChunkIndex = 1
	first := rebuildTestChunk("new-first", "duplicate", types.ChunkTypeText)
	first.StartAt = 5
	first.ChunkIndex = 0
	second := rebuildTestChunk("new-second", "duplicate", types.ChunkTypeText)
	second.StartAt = 105
	second.ChunkIndex = 1

	diff := classifyChunkCandidates([]*types.Chunk{oldSecond, oldFirst}, []*types.Chunk{second, first})

	assert.Equal(t, "old-second", second.ID)
	assert.Equal(t, "old-first", first.ID)
	assert.Empty(t, diff.ChangedNew)
	assert.Empty(t, diff.Stale)
}

func TestClassifyChunkCandidatesPreservesLaterIDsAfterInsertion(t *testing.T) {
	oldA := rebuildTestChunk("old-a", "alpha", types.ChunkTypeText)
	oldA.StartAt = 0
	oldB := rebuildTestChunk("old-b", "beta", types.ChunkTypeText)
	oldB.StartAt = 100
	inserted := rebuildTestChunk("new-inserted", "inserted", types.ChunkTypeText)
	inserted.StartAt = 0
	a := rebuildTestChunk("new-a", "alpha", types.ChunkTypeText)
	a.StartAt = 50
	b := rebuildTestChunk("new-b", "beta", types.ChunkTypeText)
	b.StartAt = 150

	diff := classifyChunkCandidates([]*types.Chunk{oldA, oldB}, []*types.Chunk{inserted, a, b})

	assert.Equal(t, "old-a", a.ID)
	assert.Equal(t, "old-b", b.ID)
	assert.Equal(t, []string{"new-inserted"}, chunkIDs(diff.ChangedNew))
	assert.Empty(t, diff.Stale)
}

func TestMergeChunkSourceMetadataPreservesDerivedFields(t *testing.T) {
	createdAt := time.Unix(100, 0)
	existing := rebuildTestChunk("chunk-1", "old", types.ChunkTypeText)
	existing.SeqID = 55
	existing.Status = int(types.ChunkStatusIndexed)
	existing.Metadata = types.JSON(`{"summary":"keep"}`)
	existing.RelationChunks = types.JSON(`["direct"]`)
	existing.IndirectRelationChunks = types.JSON(`["indirect"]`)
	existing.CreatedAt = createdAt
	existing.TagID = "old-tag"

	candidate := rebuildTestChunk("chunk-1", "new", types.ChunkTypeText)
	candidate.TagID = "new-tag"
	candidate.NextChunkID = "new-next"
	candidate.ImageInfo = `[{"url":"image.png"}]`
	candidate.ContentHash = chunkContentFingerprint(candidate)

	merged := mergeChunkSourceMetadata(existing, candidate)
	require.NotNil(t, merged)
	assert.Equal(t, candidate.Content, merged.Content)
	assert.Equal(t, candidate.TagID, merged.TagID)
	assert.Equal(t, candidate.NextChunkID, merged.NextChunkID)
	assert.Equal(t, candidate.ImageInfo, merged.ImageInfo)
	assert.Equal(t, candidate.ContentHash, merged.ContentHash)
	assert.Equal(t, existing.SeqID, merged.SeqID)
	assert.Equal(t, existing.Status, merged.Status)
	assert.Equal(t, existing.Metadata, merged.Metadata)
	assert.Equal(t, existing.RelationChunks, merged.RelationChunks)
	assert.Equal(t, existing.IndirectRelationChunks, merged.IndirectRelationChunks)
	assert.Equal(t, createdAt, merged.CreatedAt)
	assert.True(t, merged.UpdatedAt.After(createdAt))
}

func TestImageChunksForImage(t *testing.T) {
	imageIndex := 3
	otherIndex := 4
	matching := rebuildTestChunk("ocr", "ocr", types.ChunkTypeImageOCR)
	matching.ParentChunkID = "parent-1"
	matching.ImageInfo = `[{"image_index":3,"url":"provider://bucket/image.png"}]`
	originalMatching := rebuildTestChunk("caption", "caption", types.ChunkTypeImageCaption)
	originalMatching.ParentChunkID = "parent-1"
	originalMatching.ImageInfo = `[{"image_index":3,"original_url":"provider://bucket/image.png"}]`
	other := rebuildTestChunk("other", "other", types.ChunkTypeImageOCR)
	other.ParentChunkID = "parent-1"
	other.ImageInfo = `[{"image_index":4,"url":"provider://bucket/image.png"}]`
	wrongParent := rebuildTestChunk("wrong-parent", "other", types.ChunkTypeImageCaption)
	wrongParent.ParentChunkID = "parent-2"
	wrongParent.ImageInfo = `[{"image_index":3,"url":"provider://bucket/image.png"}]`
	text := rebuildTestChunk("text", "text", types.ChunkTypeText)

	matched := imageChunksForImage(
		[]*types.Chunk{matching, originalMatching, other, wrongParent, text},
		"provider://bucket/image.png",
		"parent-1",
		imageIndex,
	)
	assert.ElementsMatch(t, []string{"ocr", "caption"}, chunkIDs(matched))

	legacy := rebuildTestChunk("legacy", "legacy", types.ChunkTypeImageOCR)
	legacy.ParentChunkID = "parent-1"
	legacy.ImageInfo = `[{"url":"provider://bucket/legacy.png"}]`
	assert.Equal(t, []string{"legacy"}, chunkIDs(imageChunksForImage(
		[]*types.Chunk{legacy}, "provider://bucket/legacy.png", "parent-1", otherIndex,
	)))

	assert.Equal(t, []string{"ocr", "caption"}, chunkIDs(imageChunksForImage(
		[]*types.Chunk{matching, originalMatching}, "provider://bucket/reparsed.png", "parent-1", imageIndex,
	)))
}

func TestBuildRebuildPostProcessPlanSelective(t *testing.T) {
	run := &types.KnowledgeRebuildRun{
		OldConfigFingerprint: "same",
		NewConfigFingerprint: "same",
	}
	kb := &types.KnowledgeBase{
		EmbeddingModelID: "embedding",
		IndexingStrategy: types.IndexingStrategy{
			VectorEnabled: true,
			WikiEnabled:   true,
			GraphEnabled:  true,
		},
	}
	eff := types.EffectiveProcessConfig{
		QuestionGenerationConfig: types.QuestionGenerationConfig{Enabled: true},
		GraphEnabled:             true,
	}
	chunks := []*types.Chunk{
		rebuildTestChunk("unchanged", "unchanged", types.ChunkTypeText),
		rebuildTestChunk("changed", "changed", types.ChunkTypeText),
		rebuildTestChunk("image", "caption", types.ChunkTypeImageCaption),
	}
	results := []*types.KnowledgeRebuildChunkResult{
		rebuildChunkPlanResult("unchanged", types.ChunkTypeText, types.RebuildChunkClassUnchanged),
		rebuildChunkPlanResult("changed", types.ChunkTypeText, types.RebuildChunkClassChangedNew),
		rebuildChunkPlanResult("image", types.ChunkTypeImageCaption, types.RebuildChunkClassUnchanged),
		rebuildChunkPlanResult("stale", types.ChunkTypeText, types.RebuildChunkClassStale),
	}

	plan := buildRebuildPostProcessPlan(run, results, chunks, kb, eff)
	assert.ElementsMatch(t, []string{"unchanged", "changed", "image"}, chunkIDs(plan.CurrentTextLike))
	assert.Equal(t, []string{"changed"}, chunkIDs(plan.QuestionTargets))
	assert.Equal(t, []string{"changed"}, chunkIDs(plan.GraphTargets))
	assert.True(t, plan.ContentChanged)
	assert.False(t, plan.ConfigChanged)
	assert.True(t, plan.SummaryRequired)
	assert.True(t, plan.WikiRequired)

	run.NewConfigFingerprint = "changed-config"
	plan = buildRebuildPostProcessPlan(run, results, chunks, kb, eff)
	assert.ElementsMatch(t, []string{"unchanged", "changed"}, chunkIDs(plan.QuestionTargets))
	assert.ElementsMatch(t, []string{"unchanged", "changed", "image"}, chunkIDs(plan.GraphTargets))
}

func TestBuildRebuildPostProcessPlanSkipsAllDerivedWorkWhenUnchanged(t *testing.T) {
	run := &types.KnowledgeRebuildRun{OldConfigFingerprint: "same", NewConfigFingerprint: "same"}
	kb := &types.KnowledgeBase{
		EmbeddingModelID: "embedding",
		IndexingStrategy: types.IndexingStrategy{VectorEnabled: true, WikiEnabled: true, GraphEnabled: true},
	}
	eff := types.EffectiveProcessConfig{
		QuestionGenerationConfig: types.QuestionGenerationConfig{Enabled: true},
		GraphEnabled:             true,
	}
	chunks := []*types.Chunk{rebuildTestChunk("same", "same", types.ChunkTypeText)}
	results := []*types.KnowledgeRebuildChunkResult{
		rebuildChunkPlanResult("same", types.ChunkTypeText, types.RebuildChunkClassUnchanged),
	}

	plan := buildRebuildPostProcessPlan(run, results, chunks, kb, eff)

	assert.False(t, plan.ContentChanged)
	assert.False(t, plan.ConfigChanged)
	assert.Empty(t, plan.QuestionTargets)
	assert.Empty(t, plan.GraphTargets)
	assert.False(t, plan.SummaryRequired)
	assert.False(t, plan.WikiRequired)
}

func TestPlanSelectiveQuestionBatchesPreservesRealNeighbors(t *testing.T) {
	var all []*types.Chunk
	for i := 0; i < 8; i++ {
		chunk := rebuildTestChunk(fmt.Sprintf("chunk-%d", i), "content", types.ChunkTypeText)
		chunk.StartAt = i * 10
		all = append(all, chunk)
	}
	targets := []*types.Chunk{all[2], all[3], all[6]}
	batches := planSelectiveQuestionBatches(all, targets, 20)
	require.Len(t, batches, 2)
	assert.Equal(t, []string{"chunk-2", "chunk-3"}, chunkIDs(batches[0].Chunks))
	assert.Equal(t, "chunk-1", batches[0].PrevChunkID)
	assert.Equal(t, "chunk-4", batches[0].NextChunkID)
	assert.Equal(t, []string{"chunk-6"}, chunkIDs(batches[1].Chunks))
	assert.Equal(t, "chunk-5", batches[1].PrevChunkID)
	assert.Equal(t, "chunk-7", batches[1].NextChunkID)
}

func rebuildChunkPlanResult(id string, chunkType types.ChunkType, classification string) *types.KnowledgeRebuildChunkResult {
	return &types.KnowledgeRebuildChunkResult{
		ChunkID:        id,
		ChunkType:      chunkType,
		Classification: classification,
	}
}

func BenchmarkClassifyChunkCandidates(b *testing.B) {
	for _, size := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprintf("chunks_%d", size), func(b *testing.B) {
			oldChunks := make([]*types.Chunk, size)
			candidates := make([]*types.Chunk, size)
			for i := 0; i < size; i++ {
				id := fmt.Sprintf("chunk-%d", i)
				oldChunks[i] = rebuildTestChunk(id, fmt.Sprintf("content-%d", i), types.ChunkTypeText)
				candidates[i] = rebuildTestChunk(id, fmt.Sprintf("content-%d", i), types.ChunkTypeText)
			}
			candidates[size/2].NextChunkID = "metadata-change"
			candidates[size-1].Content = "content-change"
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = classifyChunkCandidates(oldChunks, candidates)
			}
		})
	}
}

func BenchmarkBuildRebuildPostProcessPlan(b *testing.B) {
	for _, size := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprintf("chunks_%d", size), func(b *testing.B) {
			run := &types.KnowledgeRebuildRun{OldConfigFingerprint: "same", NewConfigFingerprint: "same"}
			kb := &types.KnowledgeBase{
				EmbeddingModelID: "embedding",
				IndexingStrategy: types.IndexingStrategy{VectorEnabled: true, WikiEnabled: true, GraphEnabled: true},
			}
			eff := types.EffectiveProcessConfig{
				QuestionGenerationConfig: types.QuestionGenerationConfig{Enabled: true},
				GraphEnabled:             true,
			}
			chunks := make([]*types.Chunk, size)
			results := make([]*types.KnowledgeRebuildChunkResult, size)
			for i := 0; i < size; i++ {
				id := fmt.Sprintf("chunk-%d", i)
				chunks[i] = rebuildTestChunk(id, "content", types.ChunkTypeText)
				classification := types.RebuildChunkClassUnchanged
				if i == size/2 {
					classification = types.RebuildChunkClassChangedNew
				}
				results[i] = rebuildChunkPlanResult(id, types.ChunkTypeText, classification)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = buildRebuildPostProcessPlan(run, results, chunks, kb, eff)
			}
		})
	}
}
