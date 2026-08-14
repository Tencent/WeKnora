package chatpipeline

import (
	"context"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type expandChunkRepo struct {
	interfaces.ChunkRepository
	chunks   map[string]*types.Chunk
	children map[string][]*types.Chunk
}

func (r *expandChunkRepo) ListChunksByParentIDs(
	_ context.Context, _ uint64, parentIDs []string,
) ([]*types.Chunk, error) {
	var chunks []*types.Chunk
	for _, parentID := range parentIDs {
		chunks = append(chunks, r.children[parentID]...)
	}
	return chunks, nil
}

func (r *expandChunkRepo) ListChunksByID(
	_ context.Context, _ uint64, ids []string,
) ([]*types.Chunk, error) {
	chunks := make([]*types.Chunk, 0, len(ids))
	for _, id := range ids {
		if chunk := r.chunks[id]; chunk != nil {
			chunks = append(chunks, chunk)
		}
	}
	return chunks, nil
}

func TestExpandShortContextKeepsSourceCoordinates(t *testing.T) {
	repo := &expandChunkRepo{chunks: map[string]*types.Chunk{
		"prev": {ID: "prev", KnowledgeID: "doc", ChunkType: types.ChunkTypeText, Content: "edited previous body", NextChunkID: "base"},
		"base": {ID: "base", KnowledgeID: "doc", ChunkType: types.ChunkTypeText, Content: "edited base body", PreChunkID: "prev", NextChunkID: "next", StartAt: 100, EndAt: 120},
		"next": {ID: "next", KnowledgeID: "doc", ChunkType: types.ChunkTypeText, Content: "edited next body", PreChunkID: "base"},
	}}
	plugin := &PluginMerge{chunkRepo: repo}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	result := &types.SearchResult{
		ID: "base", KnowledgeID: "doc", ChunkType: string(types.ChunkTypeText),
		Content: "edited base body", StartAt: 100, EndAt: 120,
	}

	got := plugin.expandShortContextWithNeighbors(ctx, &types.ChatManage{}, []*types.SearchResult{result})
	if len(got) != 1 {
		t.Fatalf("result count = %d, want 1", len(got))
	}
	if got[0].StartAt != 100 || got[0].EndAt != 120 {
		t.Fatalf("source coordinates changed to [%d,%d)", got[0].StartAt, got[0].EndAt)
	}
	for _, want := range []string{"edited previous body", "edited base body", "edited next body"} {
		if !strings.Contains(got[0].Content, want) {
			t.Fatalf("expanded content missing %q: %q", want, got[0].Content)
		}
	}
}
