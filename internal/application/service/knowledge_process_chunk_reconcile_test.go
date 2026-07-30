package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/contentkey"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestPlanIngestionChunkReconcile_ClassifiesDeterministicDiff(t *testing.T) {
	existingMatched := reconcileTestChunk("row-old", "stable-matched", types.ChunkTypeText)
	existingRemoved := reconcileTestChunk("row-removed", "stable-removed", types.ChunkTypeText)
	existingLegacy := reconcileTestChunk("row-legacy", "", types.ChunkTypeParentText)
	desiredMatched := reconcileTestChunk("temporary-new-id", "stable-matched", types.ChunkTypeText)
	desiredAdded := reconcileTestChunk("row-added", "stable-added", types.ChunkTypeParentText)

	plan, err := PlanIngestionChunkReconcile(
		[]*types.Chunk{existingRemoved, existingLegacy, existingMatched},
		[]*types.Chunk{desiredAdded, desiredMatched},
	)
	require.NoError(t, err)
	require.Equal(t, []ChunkMatch{{Existing: existingMatched, Desired: desiredMatched}}, plan.Matched)
	require.Equal(t, []*types.Chunk{desiredAdded}, plan.Added)
	require.Equal(t, []*types.Chunk{existingRemoved}, plan.Removed)
	require.Equal(t, []*types.Chunk{existingLegacy}, plan.Legacy)
}

func TestPlanIngestionChunkReconcile_EmptyInputsReturnNonNilSlices(t *testing.T) {
	plan, err := PlanIngestionChunkReconcile(nil, nil)
	require.NoError(t, err)
	require.NotNil(t, plan.Matched)
	require.NotNil(t, plan.Added)
	require.NotNil(t, plan.Removed)
	require.NotNil(t, plan.Legacy)
	require.Empty(t, plan.Matched)
	require.Empty(t, plan.Added)
	require.Empty(t, plan.Removed)
	require.Empty(t, plan.Legacy)
}

func TestPlanIngestionChunkReconcile_DoesNotMutateInputs(t *testing.T) {
	existing := reconcileTestChunk("existing-id", "stable-id", types.ChunkTypeText)
	existing.Content = "old content"
	existing.ChunkIndex = 8
	desired := reconcileTestChunk("desired-id", "stable-id", types.ChunkTypeText)
	desired.Content = "new content"
	desired.ChunkIndex = 3

	existingBefore := *existing
	desiredBefore := *desired
	_, err := PlanIngestionChunkReconcile([]*types.Chunk{existing}, []*types.Chunk{desired})
	require.NoError(t, err)
	require.Equal(t, existingBefore, *existing)
	require.Equal(t, desiredBefore, *desired)
}

func TestPlanIngestionChunkReconcile_DoesNotMatchByPositionOrContent(t *testing.T) {
	existing := reconcileTestChunk("existing-id", "stable-old", types.ChunkTypeText)
	existing.Content = "same content"
	existing.ChunkIndex = 4
	existing.StartAt = 10
	existing.EndAt = 20
	existing.SeqID = 99
	desired := reconcileTestChunk("desired-id", "stable-new", types.ChunkTypeText)
	desired.Content = existing.Content
	desired.ChunkIndex = existing.ChunkIndex
	desired.StartAt = existing.StartAt
	desired.EndAt = existing.EndAt
	desired.SeqID = existing.SeqID

	plan, err := PlanIngestionChunkReconcile([]*types.Chunk{existing}, []*types.Chunk{desired})
	require.NoError(t, err)
	require.Empty(t, plan.Matched)
	require.Equal(t, []*types.Chunk{desired}, plan.Added)
	require.Equal(t, []*types.Chunk{existing}, plan.Removed)
}

func TestPlanIngestionChunkReconcile_RequiresCompatibleScopeTypeAndVersion(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*types.Chunk)
	}{
		{name: "tenant", mutate: func(c *types.Chunk) { c.TenantID++ }},
		{name: "knowledge", mutate: func(c *types.Chunk) { c.KnowledgeID = "other-knowledge" }},
		{name: "chunk type", mutate: func(c *types.Chunk) { c.ChunkType = types.ChunkTypeParentText }},
		{name: "identity version", mutate: func(c *types.Chunk) { c.IdentityVersion = "chunk-identity-v2" }},
		{name: "empty existing identity version", mutate: func(_ *types.Chunk) {}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			existing := reconcileTestChunk("existing-id", "stable-id", types.ChunkTypeText)
			desired := reconcileTestChunk("desired-id", "stable-id", types.ChunkTypeText)
			if tt.name == "empty existing identity version" {
				existing.IdentityVersion = ""
			} else {
				tt.mutate(desired)
			}

			plan, err := PlanIngestionChunkReconcile([]*types.Chunk{existing}, []*types.Chunk{desired})
			require.NoError(t, err)
			require.Empty(t, plan.Matched)
			require.Equal(t, []*types.Chunk{desired}, plan.Added)
			require.Equal(t, []*types.Chunk{existing}, plan.Removed)
		})
	}
}

func TestPlanIngestionChunkReconcile_RejectsDuplicateActiveIdentity(t *testing.T) {
	first := reconcileTestChunk("row-a", "stable-id", types.ChunkTypeText)
	second := reconcileTestChunk("row-b", "stable-id", types.ChunkTypeText)

	plan, err := PlanIngestionChunkReconcile([]*types.Chunk{first, second}, nil)
	require.ErrorContains(t, err, "duplicate active chunk stable identity")
	require.Nil(t, plan)
}

func TestPlanIngestionChunkReconcile_RejectsDuplicateDesiredIdentity(t *testing.T) {
	first := reconcileTestChunk("desired-a", "stable-id", types.ChunkTypeText)
	second := reconcileTestChunk("desired-b", "stable-id", types.ChunkTypeText)

	plan, err := PlanIngestionChunkReconcile(nil, []*types.Chunk{first, second})
	require.ErrorContains(t, err, "duplicate desired chunk stable identity")
	require.Nil(t, plan)
}

func TestPlanIngestionChunkReconcile_AllowsSameIdentityInDifferentScope(t *testing.T) {
	first := reconcileTestChunk("row-a", "stable-id", types.ChunkTypeText)
	second := reconcileTestChunk("row-b", "stable-id", types.ChunkTypeText)
	second.KnowledgeID = "other-knowledge"

	plan, err := PlanIngestionChunkReconcile([]*types.Chunk{first, second}, nil)
	require.NoError(t, err)
	require.Equal(t, []*types.Chunk{first, second}, plan.Removed)
}

func TestPlanIngestionChunkReconcile_RejectsInvalidInputs(t *testing.T) {
	tests := []struct {
		name     string
		existing []*types.Chunk
		desired  []*types.Chunk
		contains string
	}{
		{name: "nil existing", existing: []*types.Chunk{nil}, contains: "existing chunk at index 0 is nil"},
		{name: "nil desired", desired: []*types.Chunk{nil}, contains: "desired chunk at index 0 is nil"},
		{
			name:     "unmanaged existing",
			existing: []*types.Chunk{reconcileTestChunk("id", "stable", types.ChunkTypeSummary)},
			contains: "unmanaged chunk type",
		},
		{
			name:     "unmanaged desired",
			desired:  []*types.Chunk{reconcileTestChunk("id", "stable", types.ChunkTypeImageOCR)},
			contains: "unmanaged chunk type",
		},
		{
			name:     "desired without stable identity",
			desired:  []*types.Chunk{reconcileTestChunk("id", "", types.ChunkTypeText)},
			contains: "empty stable identity",
		},
		{
			name: "desired without identity version",
			desired: []*types.Chunk{func() *types.Chunk {
				chunk := reconcileTestChunk("id", "stable", types.ChunkTypeText)
				chunk.IdentityVersion = ""
				return chunk
			}()},
			contains: "empty identity version",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := PlanIngestionChunkReconcile(tt.existing, tt.desired)
			require.ErrorContains(t, err, tt.contains)
			require.Nil(t, plan)
		})
	}
}

func TestReconcilePlanningBindingAndMutation_StableIDsAcrossRebuild(t *testing.T) {
	firstParents, firstTexts, err := buildIngestionTextChunks(
		42,
		"knowledge-id",
		"kb-id",
		[]types.ParsedChunk{{Content: "child", Seq: 0, ParentIndex: 0}},
		[]types.ParsedParentChunk{{Content: "parent", Seq: 0}},
	)
	require.NoError(t, err)
	secondParsed := []types.ParsedChunk{{Content: "child", Seq: 0, ParentIndex: 0}}
	secondParents, secondTexts, err := buildIngestionTextChunks(
		42,
		"knowledge-id",
		"kb-id",
		secondParsed,
		[]types.ParsedParentChunk{{Content: "parent", Seq: 0}},
	)
	require.NoError(t, err)

	existing := append(append([]*types.Chunk{}, firstParents...), firstTexts...)
	desired := append(append([]*types.Chunk{}, secondParents...), secondTexts...)
	plan, err := PlanIngestionChunkReconcile(existing, desired)
	require.NoError(t, err)
	require.Len(t, plan.Matched, 2)
	require.Empty(t, plan.Added)
	require.Empty(t, plan.Removed)

	require.NoError(t, BindReconciledChunkIDs(plan, secondParents, secondTexts, secondParsed))
	require.Equal(t, firstParents[0].ID, secondParents[0].ID)
	require.Equal(t, firstTexts[0].ID, secondTexts[0].ID)
	require.Equal(t, firstParents[0].ID, secondTexts[0].ParentChunkID)
	require.Equal(t, firstTexts[0].ID, secondParsed[0].ChunkID)

	mutation, err := BuildIngestionChunkMutation(existing, plan, 7)
	require.NoError(t, err)
	require.Equal(t, 7, mutation.ExpectedAttempt)
	require.Len(t, mutation.ExpectedActive, 2)
	require.Len(t, mutation.Matched, 2)
	require.Empty(t, mutation.Added)
	require.Empty(t, mutation.RemovedIDs)
}

func TestBuildIngestionChunkMutation_RetiresRemovedAndLegacyAfterCommit(t *testing.T) {
	removed := reconcileTestChunk("removed", "stable-removed", types.ChunkTypeText)
	legacy := reconcileTestChunk("legacy", "", types.ChunkTypeParentText)
	plan := &ChunkReconcilePlan{Removed: []*types.Chunk{removed}, Legacy: []*types.Chunk{legacy}}

	mutation, err := BuildIngestionChunkMutation([]*types.Chunk{removed, legacy}, plan)
	require.NoError(t, err)
	require.Equal(t, []string{"removed", "legacy"}, mutation.RemovedIDs)
}

func reconcileTestChunk(id, stableIdentity string, chunkType types.ChunkType) *types.Chunk {
	return &types.Chunk{
		ID:              id,
		TenantID:        42,
		KnowledgeID:     "knowledge-id",
		KnowledgeBaseID: "kb-id",
		ChunkType:       chunkType,
		StableIdentity:  stableIdentity,
		IdentityVersion: contentkey.ChunkIdentityVersion,
	}
}
