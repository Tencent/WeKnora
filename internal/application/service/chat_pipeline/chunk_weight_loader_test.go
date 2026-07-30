package chatpipeline

import (
	"context"
	"reflect"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

func TestChunkWeightLoaderLoadsWeightsBeforeNext(t *testing.T) {
	repo := &weightLoaderChunkRepo{
		chunks: []*types.Chunk{
			{ID: "chunk-a", RecallWeight: 1.5},
		},
	}
	loader := &ChunkWeightLoader{chunkRepo: repo}
	cm := &types.ChatManage{
		PipelineState: types.PipelineState{
			SearchResult: []*types.SearchResult{
				{ID: "chunk-a", Score: 0.4},
				{ID: "chunk-b", Score: 0.9},
			},
		},
	}

	nextCalled := false
	err := loader.OnEvent(context.Background(), types.CHUNK_RERANK, cm, func() *PluginError {
		nextCalled = true
		if cm.SearchResult[0].RecallWeight != 1.5 {
			t.Fatalf("loaded weight = %v, want 1.5", cm.SearchResult[0].RecallWeight)
		}
		if cm.SearchResult[1].RecallWeight != 1.0 {
			t.Fatalf("default weight = %v, want 1.0", cm.SearchResult[1].RecallWeight)
		}
		return nil
	})

	if err != nil {
		t.Fatalf("OnEvent returned error: %v", err)
	}
	if !nextCalled {
		t.Fatal("next plugin was not called")
	}
	if !reflect.DeepEqual(repo.requestedIDs, []string{"chunk-a", "chunk-b"}) {
		t.Fatalf("requested IDs = %#v, want chunk-a/chunk-b", repo.requestedIDs)
	}
}

func TestChunkWeightLoaderSkipsRepositoryForHydratedWeights(t *testing.T) {
	repo := &weightLoaderChunkRepo{}
	loader := &ChunkWeightLoader{chunkRepo: repo}
	cm := &types.ChatManage{
		PipelineState: types.PipelineState{
			SearchResult: []*types.SearchResult{
				{ID: "chunk-a", Score: 0.4, RecallWeight: 1.5},
				{ID: "chunk-b", Score: 0.9, RecallWeight: 1.0},
			},
		},
	}

	nextCalled := false
	err := loader.OnEvent(context.Background(), types.CHUNK_RERANK, cm, func() *PluginError {
		nextCalled = true
		return nil
	})

	if err != nil {
		t.Fatalf("OnEvent returned error: %v", err)
	}
	if !nextCalled {
		t.Fatal("next plugin was not called")
	}
	if repo.requestedIDs != nil {
		t.Fatalf("repository should not be queried, requested IDs = %#v", repo.requestedIDs)
	}
}

type weightLoaderChunkRepo struct {
	interfaces.ChunkRepository

	chunks       []*types.Chunk
	requestedIDs []string
}

func (r *weightLoaderChunkRepo) ListChunksByIDOnly(ctx context.Context, ids []string) ([]*types.Chunk, error) {
	r.requestedIDs = append([]string(nil), ids...)
	return r.chunks, nil
}
