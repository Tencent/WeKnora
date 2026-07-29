package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/contentkey"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestProcessChunks_AssignsStableIdentityAcrossRebuilds(t *testing.T) {
	firstParents, firstRows, err := buildIngestionTextChunks(
		42,
		"knowledge-id",
		"kb-id",
		[]types.ParsedChunk{{Content: "content", Seq: 0, ParentIndex: -1}},
		nil,
	)
	require.NoError(t, err)
	require.Empty(t, firstParents)
	require.Len(t, firstRows, 1)

	_, secondRows, err := buildIngestionTextChunks(
		42,
		"knowledge-id",
		"kb-id",
		[]types.ParsedChunk{{Content: "content", Seq: 0, ParentIndex: -1}},
		nil,
	)
	require.NoError(t, err)
	require.Len(t, secondRows, 1)

	first := firstRows[0]
	second := secondRows[0]
	require.NotEmpty(t, first.StableIdentity)
	require.Equal(t, contentkey.ChunkIdentityVersion, first.IdentityVersion)
	require.Equal(t, first.StableIdentity, second.StableIdentity)
	require.NotEqual(t, first.ID, second.ID)
	require.NotEqual(t, first.ID, first.StableIdentity)
	_, err = uuid.Parse(first.ID)
	require.NoError(t, err)
	_, err = uuid.Parse(first.StableIdentity)
	require.NoError(t, err)
}

func TestProcessChunks_DuplicateTextGetsStableDistinctIdentities(t *testing.T) {
	parsed := func() []types.ParsedChunk {
		return []types.ParsedChunk{
			{Content: "same paragraph", Seq: 0, ParentIndex: -1},
			{Content: "middle paragraph", Seq: 1, ParentIndex: -1},
			{Content: "same paragraph", Seq: 2, ParentIndex: -1},
		}
	}

	_, first, err := buildIngestionTextChunks(42, "knowledge-id", "kb-id", parsed(), nil)
	require.NoError(t, err)
	_, second, err := buildIngestionTextChunks(42, "knowledge-id", "kb-id", parsed(), nil)
	require.NoError(t, err)

	require.Len(t, first, 3)
	require.NotEqual(t, first[0].StableIdentity, first[2].StableIdentity)
	require.Equal(t, stableIdentities(first), stableIdentities(second))
	for _, row := range first {
		require.NotEmpty(t, row.StableIdentity)
		require.Equal(t, contentkey.ChunkIdentityVersion, row.IdentityVersion)
	}
}

func TestProcessChunks_EmptyChunksDoNotShiftStableIdentityMapping(t *testing.T) {
	withBlank := func() []types.ParsedChunk {
		return []types.ParsedChunk{
			{Content: "chunk A", Seq: 0, ParentIndex: -1},
			{Content: " \n\t ", Seq: 1, ParentIndex: -1},
			{Content: "chunk B", Seq: 2, ParentIndex: -1},
		}
	}
	withoutBlank := []types.ParsedChunk{
		{Content: "chunk A", Seq: 0, ParentIndex: -1},
		{Content: "chunk B", Seq: 2, ParentIndex: -1},
	}

	parsedWithBlank := withBlank()
	_, first, err := buildIngestionTextChunks(
		42,
		"knowledge-id",
		"kb-id",
		parsedWithBlank,
		nil,
	)
	require.NoError(t, err)
	require.Len(t, first, 2)
	require.Equal(t, "", parsedWithBlank[1].ChunkID)
	require.Equal(t, "chunk A", first[0].Content)
	require.Equal(t, "chunk B", first[1].Content)

	_, expected, err := buildIngestionTextChunks(
		42,
		"knowledge-id",
		"kb-id",
		withoutBlank,
		nil,
	)
	require.NoError(t, err)
	require.Equal(t, stableIdentities(expected), stableIdentities(first))

	_, repeated, err := buildIngestionTextChunks(
		42,
		"knowledge-id",
		"kb-id",
		withBlank(),
		nil,
	)
	require.NoError(t, err)
	require.Equal(t, stableIdentities(first), stableIdentities(repeated))
}

func TestProcessChunks_AssignsStableParentChildIdentities(t *testing.T) {
	parents := func() []types.ParsedParentChunk {
		return []types.ParsedParentChunk{
			{Content: "same parent", Seq: 0},
			{Content: "same parent", Seq: 1},
		}
	}
	children := func() []types.ParsedChunk {
		return []types.ParsedChunk{
			{Content: "same child", Seq: 0, ParentIndex: 0},
			{Content: "same child", Seq: 1, ParentIndex: 1},
		}
	}

	firstParents, firstChildren, err := buildIngestionTextChunks(
		42,
		"knowledge-id",
		"kb-id",
		children(),
		parents(),
	)
	require.NoError(t, err)
	secondParents, secondChildren, err := buildIngestionTextChunks(
		42,
		"knowledge-id",
		"kb-id",
		children(),
		parents(),
	)
	require.NoError(t, err)

	require.Len(t, firstParents, 2)
	require.Len(t, firstChildren, 2)
	require.Equal(t, stableIdentities(firstParents), stableIdentities(secondParents))
	require.Equal(t, stableIdentities(firstChildren), stableIdentities(secondChildren))
	require.NotEqual(t, firstParents[0].StableIdentity, firstParents[1].StableIdentity)
	require.NotEqual(t, firstChildren[0].StableIdentity, firstChildren[1].StableIdentity)

	for i := range firstChildren {
		require.Equal(t, firstParents[i].ID, firstChildren[i].ParentChunkID)
		require.NotEqual(t, firstParents[i].StableIdentity, firstChildren[i].ParentChunkID)
		require.NotEqual(t, firstChildren[i].ParentChunkID, secondChildren[i].ParentChunkID)
	}
}

func stableIdentities(chunks []*types.Chunk) []string {
	result := make([]string, len(chunks))
	for i, chunk := range chunks {
		result[i] = chunk.StableIdentity
	}
	return result
}
