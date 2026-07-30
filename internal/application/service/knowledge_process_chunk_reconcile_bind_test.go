package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestBindReconciledChunkIDs_ReusesMatchedIDsAndRewritesReferences(t *testing.T) {
	parents, texts, err := buildIngestionTextChunks(
		42,
		"knowledge-id",
		"kb-id",
		[]types.ParsedChunk{
			{Content: "child one", Seq: 0, ParentIndex: 0},
			{Content: "child two", Seq: 1, ParentIndex: 1},
		},
		[]types.ParsedParentChunk{
			{Content: "parent one", Seq: 0},
			{Content: "parent two", Seq: 1},
		},
	)
	require.NoError(t, err)
	parsed := []types.ParsedChunk{{ChunkID: texts[0].ID}, {ChunkID: texts[1].ID}}

	oldParentID := parents[0].ID
	oldTextID := texts[0].ID
	plan := &ChunkReconcilePlan{Matched: []ChunkMatch{
		{Existing: &types.Chunk{ID: "existing-parent"}, Desired: parents[0]},
		{Existing: &types.Chunk{ID: "existing-text"}, Desired: texts[0]},
	}}

	require.NoError(t, BindReconciledChunkIDs(plan, parents, texts, parsed))
	require.Equal(t, "existing-parent", parents[0].ID)
	require.Equal(t, "existing-text", texts[0].ID)
	require.NotEqual(t, oldParentID, parents[0].ID)
	require.NotEqual(t, oldTextID, texts[0].ID)
	require.Equal(t, "existing-parent", texts[0].ParentChunkID)
	require.Equal(t, parents[1].ID, texts[1].ParentChunkID)
	require.Equal(t, "existing-parent", parents[1].PreChunkID)
	require.Equal(t, parents[1].ID, parents[0].NextChunkID)
	require.Equal(t, "existing-text", parsed[0].ChunkID)
	require.Equal(t, texts[1].ID, parsed[1].ChunkID)
}

func TestBindReconciledChunkIDs_RewritesFlatPreviousAndNextReferences(t *testing.T) {
	rows := []*types.Chunk{
		{ID: "temporary-a", StableIdentity: "stable-a", ChunkType: types.ChunkTypeText, NextChunkID: "temporary-b"},
		{ID: "temporary-b", StableIdentity: "stable-b", ChunkType: types.ChunkTypeText, PreChunkID: "temporary-a"},
	}
	plan := &ChunkReconcilePlan{Matched: []ChunkMatch{
		{Existing: &types.Chunk{ID: "existing-a"}, Desired: rows[0]},
		{Existing: &types.Chunk{ID: "existing-b"}, Desired: rows[1]},
	}}

	require.NoError(t, BindReconciledChunkIDs(plan, nil, rows, nil))
	require.Equal(t, "existing-b", rows[0].NextChunkID)
	require.Equal(t, "existing-a", rows[1].PreChunkID)
}

func TestBindReconciledChunkIDs_RejectsReferenceOutsideDesiredSet(t *testing.T) {
	row := &types.Chunk{
		ID:             "temporary",
		StableIdentity: "stable",
		ChunkType:      types.ChunkTypeText,
		NextChunkID:    "removed-row",
	}

	err := BindReconciledChunkIDs(&ChunkReconcilePlan{}, nil, []*types.Chunk{row}, nil)
	require.ErrorContains(t, err, "unknown temporary chunk ID")
}

func TestBindReconciledChunkIDs_RejectsMatchedChunkOutsideDesiredSet(t *testing.T) {
	desired := &types.Chunk{ID: "temporary", StableIdentity: "stable", ChunkType: types.ChunkTypeText}
	other := &types.Chunk{ID: "other", StableIdentity: "other-stable", ChunkType: types.ChunkTypeText}

	err := BindReconciledChunkIDs(
		&ChunkReconcilePlan{Matched: []ChunkMatch{{Existing: &types.Chunk{ID: "existing"}, Desired: other}}},
		nil,
		[]*types.Chunk{desired},
		nil,
	)
	require.ErrorContains(t, err, "not in the desired ingestion set")
}
