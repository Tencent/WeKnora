package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type feedbackOptInKnowledgeBaseRepository struct {
	interfaces.KnowledgeBaseRepository
	knowledgeBases []*types.KnowledgeBase
}

func (r *feedbackOptInKnowledgeBaseRepository) GetKnowledgeBaseByIDs(
	_ context.Context, _ []string,
) ([]*types.KnowledgeBase, error) {
	return r.knowledgeBases, nil
}

func TestIsSearchableChunkSkipsUnsynchronizedEdits(t *testing.T) {
	service := &knowledgeBaseService{}
	for _, status := range []string{"processing", "failed"} {
		chunk := &types.Chunk{ChunkType: types.ChunkTypeText, IndexStatus: status, IsEnabled: true}
		if service.isSearchableChunk(chunk) {
			t.Fatalf("chunk with index status %q should not be searchable", status)
		}
	}
	for _, status := range []string{"", "ready"} {
		chunk := &types.Chunk{ChunkType: types.ChunkTypeText, IndexStatus: status, IsEnabled: true}
		if !service.isSearchableChunk(chunk) {
			t.Fatalf("chunk with index status %q should be searchable", status)
		}
	}
}

func TestIsSearchableChunkSkipsDisabledChunk(t *testing.T) {
	service := &knowledgeBaseService{}
	chunk := &types.Chunk{
		ChunkType:   types.ChunkTypeFAQ,
		IndexStatus: "ready",
		IsEnabled:   false,
	}
	if service.isSearchableChunk(chunk) {
		t.Fatal("disabled FAQ chunk should never be searchable")
	}
}

func TestBuildSearchResultCarriesCanonicalScopeAndKnowledgeBaseOptIn(t *testing.T) {
	service := &knowledgeBaseService{}
	chunk := &types.Chunk{
		ID: "chunk", TenantID: 23, KnowledgeBaseID: "kb", KnowledgeID: "knowledge",
		ChunkType: types.ChunkTypeText, RecallWeight: 1.2, IsEnabled: true,
	}
	knowledge := &types.Knowledge{ID: "knowledge", KnowledgeBaseID: "kb"}

	result := service.buildSearchResult(
		chunk, knowledge, 0.8, chunk.RecallWeight, true, types.MatchTypeEmbedding, "",
	)

	if result.TenantID != chunk.TenantID {
		t.Fatalf("tenant scope = %d, want %d", result.TenantID, chunk.TenantID)
	}
	if result.KnowledgeBaseID != chunk.KnowledgeBaseID {
		t.Fatalf("KB scope = %q, want %q", result.KnowledgeBaseID, chunk.KnowledgeBaseID)
	}
	if !result.FeedbackWeightEnabled {
		t.Fatal("explicit KB feedback-weight opt-in was lost")
	}
}

func TestLoadFeedbackWeightOptInsDefaultsMissingKnowledgeBasesToDisabled(t *testing.T) {
	service := &knowledgeBaseService{repo: &feedbackOptInKnowledgeBaseRepository{
		knowledgeBases: []*types.KnowledgeBase{
			{
				ID: "kb-enabled",
				IndexingStrategy: types.IndexingStrategy{
					VectorEnabled: true, FeedbackWeightEnabled: true,
				},
			},
			{ID: "kb-disabled", IndexingStrategy: types.IndexingStrategy{VectorEnabled: true}},
		},
	}}
	optIns := service.loadFeedbackWeightOptIns(context.Background(), map[string]*types.Knowledge{
		"knowledge-enabled":  {ID: "knowledge-enabled", KnowledgeBaseID: "kb-enabled"},
		"knowledge-disabled": {ID: "knowledge-disabled", KnowledgeBaseID: "kb-disabled"},
		"knowledge-missing":  {ID: "knowledge-missing", KnowledgeBaseID: "kb-missing"},
	})

	if !optIns["kb-enabled"] {
		t.Fatal("enabled KB opt-in was lost")
	}
	if optIns["kb-disabled"] || optIns["kb-missing"] {
		t.Fatal("disabled or missing KB must fail closed")
	}
}
