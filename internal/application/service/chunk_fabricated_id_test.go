package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseFabricatedChunkID(t *testing.T) {
	cases := []struct {
		name    string
		id      string
		wantKB  string
		wantIdx int
		wantOK  bool
	}{
		{"valid", "f91259e7-4f95-46c2-a928-6a558cc0d3d3_chunk_119",
			"f91259e7-4f95-46c2-a928-6a558cc0d3d3", 119, true},
		{"uppercase uuid", "F91259E7-4F95-46C2-A928-6A558CC0D3D3_chunk_0",
			"F91259E7-4F95-46C2-A928-6A558CC0D3D3", 0, true},
		{"real uuid untouched", "f91259e7-4f95-46c2-a928-6a558cc0d3d3", "", 0, false},
		{"non-uuid prefix", "knowledge_1_chunk_2", "", 0, false},
		{"missing index", "f91259e7-4f95-46c2-a928-6a558cc0d3d3_chunk_", "", 0, false},
		{"negative index", "f91259e7-4f95-46c2-a928-6a558cc0d3d3_chunk_-1", "", 0, false},
		{"empty", "", "", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kb, idx, ok := parseFabricatedChunkID(tc.id)
			assert.Equal(t, tc.wantOK, ok)
			assert.Equal(t, tc.wantKB, kb)
			assert.Equal(t, tc.wantIdx, idx)
		})
	}
}

// fakeRepo records whether the fallback path is taken.
type fakeChunkRepoForFallback struct {
	interfaces.ChunkRepository
	calledByID       bool
	calledByFallback bool
}

func (f *fakeChunkRepoForFallback) GetChunkByIDOnly(_ context.Context, id string) (*types.Chunk, error) {
	f.calledByID = true
	return nil, ErrChunkNotFound
}

func (f *fakeChunkRepoForFallback) GetChunkByKnowledgeAndIndexOnly(
	_ context.Context, knowledgeID string, chunkIndex int,
) (*types.Chunk, error) {
	f.calledByFallback = true
	return &types.Chunk{ID: "real-uuid", KnowledgeID: knowledgeID, ChunkIndex: chunkIndex}, nil
}

func TestGetChunkByIDOnlyFallback(t *testing.T) {
	repo := &fakeChunkRepoForFallback{}
	svc := &chunkService{chunkRepository: repo}

	// Fabricated id -> fallback resolves the real chunk.
	chunk, err := svc.GetChunkByIDOnly(context.Background(),
		"f91259e7-4f95-46c2-a928-6a558cc0d3d3_chunk_119")
	require.NoError(t, err)
	require.NotNil(t, chunk)
	assert.Equal(t, "real-uuid", chunk.ID)
	assert.True(t, repo.calledByID)
	assert.True(t, repo.calledByFallback)

	// Ordinary missing uuid -> no fallback, still ErrChunkNotFound.
	repo2 := &fakeChunkRepoForFallback{}
	svc2 := &chunkService{chunkRepository: repo2}
	_, err = svc2.GetChunkByIDOnly(context.Background(),
		"fef2f4fe-0ab7-4339-a006-72d6369dae63")
	assert.ErrorIs(t, err, ErrChunkNotFound)
	assert.False(t, repo2.calledByFallback)
}
