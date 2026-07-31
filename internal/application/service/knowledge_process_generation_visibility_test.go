package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type generationVisibilityKBService struct {
	interfaces.KnowledgeBaseService
}

func (generationVisibilityKBService) GetKnowledgeBaseByID(context.Context, string) (*types.KnowledgeBase, error) {
	return &types.KnowledgeBase{
		ID:             "kb-1",
		TenantID:       7,
		SummaryModelID: "chat-model",
	}, nil
}

func TestRegenerateChunkQuestionsRejectsHiddenGenerationChunk(t *testing.T) {
	chunkRepo := &activeListingChunkRepo{chunks: []*types.Chunk{{
		ID:              "hidden-chunk",
		TenantID:        7,
		KnowledgeID:     "knowledge-1",
		KnowledgeBaseID: "kb-1",
		ChunkType:       types.ChunkTypeText,
		GenerationID:    "generation-building",
	}}}
	svc := &knowledgeService{
		chunkRepo: chunkRepo,
		repo: chunkVisibilityKnowledgeRepo{knowledge: &types.Knowledge{
			ID:                 "knowledge-1",
			TenantID:           7,
			ActiveGenerationID: "generation-active",
		}},
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))

	_, err := svc.RegenerateChunkQuestions(ctx, "hidden-chunk")
	if !errors.Is(err, ErrChunkNotFound) {
		t.Fatalf("RegenerateChunkQuestions hidden generation err = %v, want ErrChunkNotFound", err)
	}
}

func TestRegenerateKnowledgeSummaryUsesActiveGenerationChunks(t *testing.T) {
	chunkRepo := &activeListingChunkRepo{
		chunks: []*types.Chunk{{
			ID:           "hidden-chunk",
			TenantID:     7,
			KnowledgeID:  "knowledge-1",
			ChunkType:    types.ChunkTypeText,
			IsEnabled:    true,
			GenerationID: "generation-building",
		}},
		active: []*types.Chunk{},
	}
	svc := &knowledgeService{
		chunkRepo: chunkRepo,
		repo: chunkVisibilityKnowledgeRepo{knowledge: &types.Knowledge{
			ID:                 "knowledge-1",
			TenantID:           7,
			KnowledgeBaseID:    "kb-1",
			ActiveGenerationID: "generation-active",
		}},
		kbService: generationVisibilityKBService{},
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))

	_, err := svc.RegenerateKnowledgeSummary(ctx, "knowledge-1")
	if err == nil || !strings.Contains(err.Error(), "no enabled text chunks") {
		t.Fatalf("RegenerateKnowledgeSummary err = %v, want no enabled text chunks", err)
	}
	if chunkRepo.activeCalls != 1 {
		t.Fatalf("ListActiveChunksByKnowledgeID calls = %d, want 1", chunkRepo.activeCalls)
	}
	if chunkRepo.allCalls != 0 {
		t.Fatalf("ListChunksByKnowledgeID calls = %d, want 0 raw list calls", chunkRepo.allCalls)
	}
}
