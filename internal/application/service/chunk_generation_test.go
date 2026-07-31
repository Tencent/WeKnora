package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type activeListingChunkRepo struct {
	interfaces.ChunkRepository

	activeCalls int
	allCalls    int
	byIDCalls   int
	updateCalls int
	chunks      []*types.Chunk
	active      []*types.Chunk
}

type chunkVisibilityKnowledgeRepo struct {
	interfaces.KnowledgeRepository

	knowledge *types.Knowledge
}

func (r chunkVisibilityKnowledgeRepo) GetKnowledgeByID(
	_ context.Context, tenantID uint64, id string,
) (*types.Knowledge, error) {
	if r.knowledge == nil || r.knowledge.TenantID != tenantID || r.knowledge.ID != id {
		return nil, ErrChunkNotFound
	}
	return r.knowledge, nil
}

func (r *activeListingChunkRepo) ListActiveChunksByKnowledgeID(
	_ context.Context, tenantID uint64, knowledgeID string,
) ([]*types.Chunk, error) {
	r.activeCalls++
	_ = tenantID
	_ = knowledgeID
	if r.active != nil {
		return r.active, nil
	}
	return r.chunks, nil
}

func (r *activeListingChunkRepo) ListChunksByKnowledgeID(
	context.Context, uint64, string,
) ([]*types.Chunk, error) {
	r.allCalls++
	return r.chunks, nil
}

func (r *activeListingChunkRepo) GetChunkByID(
	_ context.Context, tenantID uint64, id string,
) (*types.Chunk, error) {
	r.byIDCalls++
	for _, chunk := range r.chunks {
		if chunk.TenantID == tenantID && chunk.ID == id {
			return chunk, nil
		}
	}
	return nil, ErrChunkNotFound
}

func (r *activeListingChunkRepo) UpdateChunk(_ context.Context, _ *types.Chunk) error {
	r.updateCalls++
	return nil
}

func TestChunkServiceListChunksByKnowledgeIDUsesActiveGenerationView(t *testing.T) {
	repo := &activeListingChunkRepo{chunks: []*types.Chunk{{
		ID:           "active-chunk",
		TenantID:     7,
		KnowledgeID:  "knowledge-1",
		GenerationID: "generation-active",
		ChunkType:    types.ChunkTypeText,
	}}}
	svc := &chunkService{chunkRepository: repo}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))

	got, err := svc.ListChunksByKnowledgeID(ctx, "knowledge-1")
	if err != nil {
		t.Fatalf("ListChunksByKnowledgeID: %v", err)
	}
	if len(got) != 1 || got[0].ID != "active-chunk" {
		t.Fatalf("chunks = %+v, want active chunk only", got)
	}
	if repo.activeCalls != 1 {
		t.Fatalf("ListActiveChunksByKnowledgeID calls = %d, want 1", repo.activeCalls)
	}
	if repo.allCalls != 0 {
		t.Fatalf("ListChunksByKnowledgeID calls = %d, want 0 ordinary full-list calls", repo.allCalls)
	}
}

func TestChunkServiceGetChunkByIDRejectsHiddenGenerationChunk(t *testing.T) {
	repo := &activeListingChunkRepo{chunks: []*types.Chunk{{
		ID:           "hidden-chunk",
		TenantID:     7,
		KnowledgeID:  "knowledge-1",
		GenerationID: "generation-building",
		ChunkType:    types.ChunkTypeText,
	}}}
	svc := &chunkService{
		chunkRepository: repo,
		knowledgeRepo: chunkVisibilityKnowledgeRepo{knowledge: &types.Knowledge{
			ID:                 "knowledge-1",
			TenantID:           7,
			ActiveGenerationID: "generation-active",
		}},
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))

	_, err := svc.GetChunkByID(ctx, "hidden-chunk")
	if !errors.Is(err, ErrChunkNotFound) {
		t.Fatalf("GetChunkByID hidden generation err = %v, want ErrChunkNotFound", err)
	}
	if repo.byIDCalls != 1 {
		t.Fatalf("GetChunkByID repo calls = %d, want 1", repo.byIDCalls)
	}
}

func TestChunkServiceGetChunkByIDAllowsActiveGenerationChunk(t *testing.T) {
	repo := &activeListingChunkRepo{chunks: []*types.Chunk{{
		ID:           "active-chunk",
		TenantID:     7,
		KnowledgeID:  "knowledge-1",
		GenerationID: "generation-active",
		ChunkType:    types.ChunkTypeText,
	}}}
	svc := &chunkService{
		chunkRepository: repo,
		knowledgeRepo: chunkVisibilityKnowledgeRepo{knowledge: &types.Knowledge{
			ID:                 "knowledge-1",
			TenantID:           7,
			ActiveGenerationID: "generation-active",
		}},
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))

	chunk, err := svc.GetChunkByID(ctx, "active-chunk")
	if err != nil {
		t.Fatalf("GetChunkByID active generation: %v", err)
	}
	if chunk.ID != "active-chunk" {
		t.Fatalf("chunk ID = %q, want active-chunk", chunk.ID)
	}
}

func TestChunkServiceGetChunkByIDAllowsLegacyChunkOnlyWhenKnowledgeHasNoActiveGeneration(t *testing.T) {
	repo := &activeListingChunkRepo{chunks: []*types.Chunk{{
		ID:          "legacy-chunk",
		TenantID:    7,
		KnowledgeID: "knowledge-1",
		ChunkType:   types.ChunkTypeText,
	}}}
	svc := &chunkService{
		chunkRepository: repo,
		knowledgeRepo: chunkVisibilityKnowledgeRepo{knowledge: &types.Knowledge{
			ID:       "knowledge-1",
			TenantID: 7,
		}},
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))

	chunk, err := svc.GetChunkByID(ctx, "legacy-chunk")
	if err != nil {
		t.Fatalf("GetChunkByID legacy generation: %v", err)
	}
	if chunk.ID != "legacy-chunk" {
		t.Fatalf("chunk ID = %q, want legacy-chunk", chunk.ID)
	}
}

func TestChunkServiceUpsertGeneratedQuestionRejectsHiddenGenerationChunk(t *testing.T) {
	repo := &activeListingChunkRepo{chunks: []*types.Chunk{{
		ID:           "hidden-chunk",
		TenantID:     7,
		KnowledgeID:  "knowledge-1",
		ChunkType:    types.ChunkTypeText,
		GenerationID: "generation-building",
	}}}
	svc := &chunkService{
		chunkRepository: repo,
		knowledgeRepo: chunkVisibilityKnowledgeRepo{knowledge: &types.Knowledge{
			ID:                 "knowledge-1",
			TenantID:           7,
			ActiveGenerationID: "generation-active",
		}},
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))

	_, err := svc.UpsertGeneratedQuestion(ctx, "hidden-chunk", "", "What is hidden?")
	if !errors.Is(err, ErrChunkNotFound) {
		t.Fatalf("UpsertGeneratedQuestion hidden generation err = %v, want ErrChunkNotFound", err)
	}
	if repo.updateCalls != 0 {
		t.Fatalf("UpdateChunk calls = %d, want 0 for hidden generation", repo.updateCalls)
	}
}
