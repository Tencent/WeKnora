package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// This exercises the reconciliation segment used by processChunks end to end:
// build desired rows -> plan -> bind final IDs -> build mutation -> transaction.
func TestProcessChunksReconciliationMainFlowPreservesMatchedRowIDs(t *testing.T) {
	ctx := context.Background()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.Chunk{}))
	chunkRepo := repository.NewChunkRepository(db)

	const tenantID uint64 = 42
	const knowledgeID = "knowledge-main-flow"
	const kbID = "kb-main-flow"

	firstParsed := []types.ParsedChunk{
		{Content: "alpha", Seq: 0, ParentIndex: -1},
		{Content: "beta", Seq: 1, ParentIndex: -1},
	}
	firstParents, firstTexts, err := buildIngestionTextChunks(
		tenantID, knowledgeID, kbID, firstParsed, nil,
	)
	require.NoError(t, err)
	firstDesired := append(firstParents, firstTexts...)
	firstPlan, err := PlanIngestionChunkReconcile(nil, firstDesired)
	require.NoError(t, err)
	require.NoError(t, BindReconciledChunkIDs(firstPlan, firstParents, firstTexts, firstParsed))
	firstMutation, err := BuildIngestionChunkMutation(nil, firstPlan)
	require.NoError(t, err)
	require.NoError(t, chunkRepo.ApplyIngestionChunkReconcile(ctx, tenantID, knowledgeID, firstMutation))

	firstActive, err := chunkRepo.ListActiveIngestionChunksByKnowledgeID(ctx, tenantID, knowledgeID)
	require.NoError(t, err)
	require.Len(t, firstActive, 2)
	idsByIdentity := map[string]string{}
	for _, chunk := range firstActive {
		idsByIdentity[chunk.StableIdentity] = chunk.ID
	}

	secondParsed := []types.ParsedChunk{
		{Content: "alpha", Seq: 0, ParentIndex: -1}, // matched
		{Content: "gamma", Seq: 1, ParentIndex: -1}, // added; beta removed
	}
	secondParents, secondTexts, err := buildIngestionTextChunks(
		tenantID, knowledgeID, kbID, secondParsed, nil,
	)
	require.NoError(t, err)
	secondDesired := append(secondParents, secondTexts...)
	secondPlan, err := PlanIngestionChunkReconcile(firstActive, secondDesired)
	require.NoError(t, err)
	require.Len(t, secondPlan.Matched, 1)
	require.Len(t, secondPlan.Added, 1)
	require.Len(t, secondPlan.Removed, 1)
	require.NoError(t, BindReconciledChunkIDs(secondPlan, secondParents, secondTexts, secondParsed))
	secondMutation, err := BuildIngestionChunkMutation(firstActive, secondPlan)
	require.NoError(t, err)
	require.NoError(t, chunkRepo.ApplyIngestionChunkReconcile(ctx, tenantID, knowledgeID, secondMutation))

	secondActive, err := chunkRepo.ListActiveIngestionChunksByKnowledgeID(ctx, tenantID, knowledgeID)
	require.NoError(t, err)
	require.Len(t, secondActive, 2)
	require.Equal(t,
		idsByIdentity[secondPlan.Matched[0].Existing.StableIdentity],
		secondPlan.Matched[0].Desired.ID,
	)

	var allRows []types.Chunk
	require.NoError(t, db.Unscoped().Where("knowledge_id = ?", knowledgeID).Find(&allRows).Error)
	require.Len(t, allRows, 3)
	var deleted int
	for _, row := range allRows {
		if row.DeletedAt.Valid {
			deleted++
		}
	}
	require.Equal(t, 1, deleted)
}
