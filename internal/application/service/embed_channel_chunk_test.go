package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type stubChunkForEmbed struct {
	interfaces.ChunkService
	chunks map[string]*types.Chunk
}

func (s *stubChunkForEmbed) GetChunkByIDOnly(_ context.Context, id string) (*types.Chunk, error) {
	chunk, ok := s.chunks[id]
	if !ok {
		return nil, ErrChunkNotFound
	}
	return chunk, nil
}

func newEmbedChunkService(agent *types.CustomAgent, chunks map[string]*types.Chunk) *embedChannelService {
	return &embedChannelService{
		agentService: &stubAgentForEmbed{agent: agent},
		chunkService: &stubChunkForEmbed{chunks: chunks},
	}
}

func TestChunkAllowedForEmbedAllowedKB(t *testing.T) {
	svc := newEmbedChunkService(
		&types.CustomAgent{
			Config: types.CustomAgentConfig{
				KBSelectionMode: "selected",
				KnowledgeBases:  []string{"kb-allowed"},
			},
		},
		map[string]*types.Chunk{
			"chunk-1": {ID: "chunk-1", TenantID: 5, KnowledgeBaseID: "kb-allowed"},
		},
	)
	ch := &types.EmbedChannel{ID: "ch-1", TenantID: 5, AgentID: "agent-1"}

	if !svc.chunkAllowedForEmbed(context.Background(), ch, &types.Chunk{TenantID: 5, KnowledgeBaseID: "kb-allowed"}) {
		t.Fatal("chunk in allowed KB should be permitted")
	}

	chunk, err := svc.EmbedChunk(context.Background(), ch, "chunk-1")
	if err != nil {
		t.Fatalf("EmbedChunk() = %v", err)
	}
	if chunk == nil || chunk.ID != "chunk-1" {
		t.Fatalf("unexpected chunk: %#v", chunk)
	}
}

func TestChunkAllowedForEmbedCrossTenantDenied(t *testing.T) {
	svc := newEmbedChunkService(
		&types.CustomAgent{
			Config: types.CustomAgentConfig{
				KBSelectionMode: "selected",
				KnowledgeBases:  []string{"kb-allowed"},
			},
		},
		map[string]*types.Chunk{
			"chunk-x": {ID: "chunk-x", TenantID: 999, KnowledgeBaseID: "kb-allowed"},
		},
	)
	ch := &types.EmbedChannel{ID: "ch-1", TenantID: 5, AgentID: "agent-1"}
	chunk := &types.Chunk{ID: "chunk-x", TenantID: 999, KnowledgeBaseID: "kb-allowed"}

	if svc.chunkAllowedForEmbed(context.Background(), ch, chunk) {
		t.Fatal("cross-tenant chunk must be denied before KB checks")
	}

	_, err := svc.EmbedChunk(context.Background(), ch, "chunk-x")
	if !errors.Is(err, ErrEmbedChunkForbidden) {
		t.Fatalf("EmbedChunk() = %v, want ErrEmbedChunkForbidden", err)
	}
}

func TestChunkAllowedForEmbedWrongKBDenied(t *testing.T) {
	svc := newEmbedChunkService(
		&types.CustomAgent{
			Config: types.CustomAgentConfig{
				KBSelectionMode: "selected",
				KnowledgeBases:  []string{"kb-allowed"},
			},
		},
		map[string]*types.Chunk{
			"chunk-other": {ID: "chunk-other", TenantID: 5, KnowledgeBaseID: "kb-other"},
		},
	)
	ch := &types.EmbedChannel{ID: "ch-1", TenantID: 5, AgentID: "agent-1"}
	chunk := &types.Chunk{ID: "chunk-other", TenantID: 5, KnowledgeBaseID: "kb-other"}

	if svc.chunkAllowedForEmbed(context.Background(), ch, chunk) {
		t.Fatal("chunk outside allowed KB list must be denied")
	}

	_, err := svc.EmbedChunk(context.Background(), ch, "chunk-other")
	if !errors.Is(err, ErrEmbedChunkForbidden) {
		t.Fatalf("EmbedChunk() = %v, want ErrEmbedChunkForbidden", err)
	}
}

func TestChunkAllowedForEmbedAllModePermitsInTenant(t *testing.T) {
	svc := newEmbedChunkService(
		&types.CustomAgent{
			Config: types.CustomAgentConfig{KBSelectionMode: "all"},
		},
		map[string]*types.Chunk{
			"chunk-all": {ID: "chunk-all", TenantID: 5, KnowledgeBaseID: "kb-any"},
		},
	)
	ch := &types.EmbedChannel{ID: "ch-1", TenantID: 5, AgentID: "agent-1"}

	if !svc.chunkAllowedForEmbed(context.Background(), ch, &types.Chunk{TenantID: 5, KnowledgeBaseID: "kb-any"}) {
		t.Fatal("all-mode agent should permit in-tenant chunks")
	}
}
