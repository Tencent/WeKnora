package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/artifact"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
)

type graphExtractChunkRepo struct {
	interfaces.ChunkRepository
	chunk *types.Chunk
}

func (r graphExtractChunkRepo) GetChunkByID(context.Context, uint64, string) (*types.Chunk, error) {
	return r.chunk, nil
}

type graphExtractKBRepo struct {
	interfaces.KnowledgeBaseRepository
	kb *types.KnowledgeBase
}

func (r graphExtractKBRepo) GetKnowledgeBaseByID(context.Context, string) (*types.KnowledgeBase, error) {
	return r.kb, nil
}

type retiredGraphGenerationRepo struct {
	interfaces.KnowledgeGenerationRepository
}

func (r retiredGraphGenerationRepo) Get(context.Context, uint64, string) (*types.KnowledgeGeneration, error) {
	return &types.KnowledgeGeneration{
		ID:          "generation-retired",
		TenantID:    7,
		KnowledgeID: "knowledge-1",
		Attempt:     1,
		State:       types.KnowledgeGenerationStateRetired,
	}, nil
}

func (r retiredGraphGenerationRepo) LatestAttempt(context.Context, uint64, string) (int, error) {
	return 1, nil
}

type graphExtractModelService struct {
	interfaces.ModelService
	chatCalls int
}

func (s *graphExtractModelService) GetChatModel(context.Context, string) (chat.Chat, error) {
	s.chatCalls++
	return nil, errors.New("chat model should not be requested for retired generation")
}

type activeGraphGenerationRepo struct {
	interfaces.KnowledgeGenerationRepository
	latest      int
	generations map[string]*types.KnowledgeGeneration
}

func (r *activeGraphGenerationRepo) Get(_ context.Context, _ uint64, generationID string) (*types.KnowledgeGeneration, error) {
	return r.generations[generationID], nil
}

func (r *activeGraphGenerationRepo) LatestAttempt(context.Context, uint64, string) (int, error) {
	return r.latest, nil
}

type graphExtractCountingModelService struct {
	interfaces.ModelService
	model chat.Chat
}

func (s graphExtractCountingModelService) GetChatModel(context.Context, string) (chat.Chat, error) {
	return s.model, nil
}

type graphExtractRecordingGraphRepo struct {
	interfaces.RetrieveGraphRepository
	namespaces []types.NameSpace
	graphs     []*types.GraphData
}

func (r *graphExtractRecordingGraphRepo) AddGraph(
	_ context.Context,
	namespace types.NameSpace,
	graphs []*types.GraphData,
) error {
	r.namespaces = append(r.namespaces, namespace)
	r.graphs = append(r.graphs, graphs...)
	return nil
}

func TestChunkExtractSkipsRetiredGenerationBeforeChatModel(t *testing.T) {
	modelService := &graphExtractModelService{}
	svc := &ChunkExtractService{
		config:            &config.Config{},
		modelService:      modelService,
		knowledgeBaseRepo: graphExtractKBRepo{kb: &types.KnowledgeBase{ID: "kb-1", SummaryModelID: "chat-1"}},
		generationRepo:    retiredGraphGenerationRepo{},
		chunkRepo: graphExtractChunkRepo{chunk: &types.Chunk{
			ID:              "chunk-1",
			TenantID:        7,
			KnowledgeID:     "knowledge-1",
			KnowledgeBaseID: "kb-1",
			Content:         "graph source",
			ChunkType:       types.ChunkTypeText,
			GenerationID:    "generation-retired",
		}},
	}
	payload, err := json.Marshal(types.ExtractChunkPayload{
		TenantID:     7,
		KnowledgeID:  "knowledge-1",
		ChunkID:      "chunk-1",
		ModelID:      "chat-1",
		Attempt:      1,
		GenerationID: "generation-retired",
	})
	if err != nil {
		t.Fatal(err)
	}

	err = svc.Handle(context.Background(), asynq.NewTask(types.TypeChunkExtract, payload))

	if err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}
	if modelService.chatCalls != 0 {
		t.Fatalf("chat model calls = %d, want 0", modelService.chatCalls)
	}
}

func TestChunkExtractReusesGraphArtifactAcrossGenerationRebind(t *testing.T) {
	const graphJSON = `[
  {"entity": "Alice", "entity_attributes": ["person"]},
  {"entity": "Bob", "entity_attributes": ["person"]},
  {"entity1": "Alice", "entity2": "Bob", "relation": "knows"}
]`
	model := &fakeArtifactChat{out: []string{graphJSON, `[]`}}
	generationRepo := &activeGraphGenerationRepo{
		latest: 1,
		generations: map[string]*types.KnowledgeGeneration{
			"generation-1": {
				ID:          "generation-1",
				TenantID:    7,
				KnowledgeID: "knowledge-1",
				Attempt:     1,
				State:       types.KnowledgeGenerationStateBuilding,
			},
			"generation-2": {
				ID:          "generation-2",
				TenantID:    7,
				KnowledgeID: "knowledge-1",
				Attempt:     2,
				State:       types.KnowledgeGenerationStateBuilding,
			},
		},
	}
	chunkRepo := graphExtractChunkRepo{chunk: &types.Chunk{
		ID:              "chunk-generation-1",
		TenantID:        7,
		KnowledgeID:     "knowledge-1",
		KnowledgeBaseID: "kb-1",
		Content:         "same graph source",
		ChunkType:       types.ChunkTypeText,
		GenerationID:    "generation-1",
	}}
	graphRepo := &graphExtractRecordingGraphRepo{}
	svc := &ChunkExtractService{
		config: &config.Config{ArtifactCache: &config.ArtifactCacheConfig{Stages: map[string]bool{
			"graph_extract": true,
		}}},
		template: &types.PromptTemplateStructured{
			Description: "Extract entities and relations as JSON.",
			Tags:        []string{"knows"},
		},
		modelService: graphExtractCountingModelService{model: model},
		knowledgeBaseRepo: graphExtractKBRepo{kb: &types.KnowledgeBase{
			ID:               "kb-1",
			SummaryModelID:   "chat-1",
			IndexingStrategy: types.IndexingStrategy{GraphEnabled: true},
			ExtractConfig:    &types.ExtractConfig{Enabled: true},
		}},
		generationRepo: generationRepo,
		chunkRepo:      chunkRepo,
		graphEngine:    graphRepo,
		artifactRuntime: artifact.NewRuntime(newMultimodalArtifactStore(), artifact.RuntimeOptions{
			ReadEnabled: true, WriteEnabled: true, MaxInlineBytes: 4096,
		}),
	}

	run := func(attempt int, generationID string) {
		t.Helper()
		payload, err := json.Marshal(types.ExtractChunkPayload{
			TenantID:     7,
			KnowledgeID:  "knowledge-1",
			ChunkID:      chunkRepo.chunk.ID,
			ModelID:      "chat-1",
			Attempt:      attempt,
			GenerationID: generationID,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := svc.Handle(context.Background(), asynq.NewTask(types.TypeChunkExtract, payload)); err != nil {
			t.Fatalf("Handle returned error: %v", err)
		}
	}

	run(1, "generation-1")
	chunkRepo.chunk.ID = "chunk-generation-2"
	chunkRepo.chunk.GenerationID = "generation-2"
	generationRepo.latest = 2
	run(2, "generation-2")

	if model.calls != 1 {
		t.Fatalf("graph_extract provider calls = %d, want 1 total and 0 on repeated content", model.calls)
	}
	if len(graphRepo.namespaces) != 2 {
		t.Fatalf("graph writes = %d, want 2 generation-scoped materializations", len(graphRepo.namespaces))
	}
	if graphRepo.namespaces[0].Generation != "generation-1" || graphRepo.namespaces[1].Generation != "generation-2" {
		t.Fatalf("graph namespaces = %+v, want writes rebound to each generation", graphRepo.namespaces)
	}
	if got := graphRepo.graphs[1].Node[0].Chunks; len(got) != 1 || got[0] != "chunk-generation-2" {
		t.Fatalf("second cached graph chunks = %+v, want current generation chunk id", got)
	}
}
