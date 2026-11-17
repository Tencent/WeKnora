package service

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// reembedChunkRepo serves ListChunksByID from a map, so a test can change or
// delete chunks after the task listed them.
type reembedChunkRepo struct {
	interfaces.ChunkRepository
	current map[string]*types.Chunk
	lookups [][]string
}

func (r *reembedChunkRepo) ListChunksByID(_ context.Context, _ uint64, ids []string) ([]*types.Chunk, error) {
	r.lookups = append(r.lookups, ids)
	out := make([]*types.Chunk, 0, len(ids))
	for _, id := range ids {
		if c, ok := r.current[id]; ok {
			out = append(out, c)
		}
	}
	return out, nil
}

func reembedChunks(n int) []*types.Chunk {
	chunks := make([]*types.Chunk, 0, n)
	for i := 0; i < n; i++ {
		chunks = append(chunks, &types.Chunk{ID: fmt.Sprintf("c%03d", i), Content: "listed"})
	}
	return chunks
}

func TestReembedWindowsReindexesTheCurrentChunksPerWindow(t *testing.T) {
	listed := reembedChunks(reembedWindow + 50)
	repo := &reembedChunkRepo{current: map[string]*types.Chunk{}}
	for _, c := range listed {
		repo.current[c.ID] = &types.Chunk{ID: c.ID, Content: "edited"}
	}
	delete(repo.current, "c000") // deleted while the task ran
	svc := &knowledgeService{chunkRepo: repo}

	var windows [][]*types.Chunk
	err := svc.reembedWindows(context.Background(), 7, listed, func(w []*types.Chunk) error {
		windows = append(windows, w)
		return nil
	})

	require.NoError(t, err)
	require.Len(t, repo.lookups, 2)
	require.Len(t, repo.lookups[0], reembedWindow)
	require.Len(t, repo.lookups[1], 50)
	require.Len(t, windows, 2)
	require.Len(t, windows[0], reembedWindow-1, "a deleted chunk is not re-indexed")
	for _, c := range windows[0] {
		require.Equal(t, "edited", c.Content, "the window is re-indexed from the chunks read right before it")
	}
}

func TestReembedWindowsSkipsWindowsWhoseChunksAreGone(t *testing.T) {
	svc := &knowledgeService{chunkRepo: &reembedChunkRepo{current: map[string]*types.Chunk{}}}
	called := false
	err := svc.reembedWindows(context.Background(), 7, reembedChunks(3), func([]*types.Chunk) error {
		called = true
		return nil
	})
	require.NoError(t, err)
	require.False(t, called)
}

func TestReembedWindowsStopsAtTheFirstFailure(t *testing.T) {
	listed := reembedChunks(reembedWindow + 1)
	repo := &reembedChunkRepo{current: map[string]*types.Chunk{}}
	for _, c := range listed {
		repo.current[c.ID] = c
	}
	svc := &knowledgeService{chunkRepo: repo}
	calls := 0
	err := svc.reembedWindows(context.Background(), 7, listed, func([]*types.Chunk) error {
		calls++
		return errors.New("embedding rate limited")
	})
	require.ErrorContains(t, err, "embedding rate limited")
	require.Equal(t, 1, calls)
}
