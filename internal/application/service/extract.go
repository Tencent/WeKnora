package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	chatpipline "github.com/Tencent/WeKnora/internal/application/service/chat_pipline"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

func NewChunkExtractTask(ctx context.Context, client *asynq.Client, tenantID uint, chunkID string, modelID string) error {
	if strings.ToLower(os.Getenv("NEO4J_ENABLE")) != "true" {
		logger.Debugf(ctx, "NOT SUPPORT RETRIEVE GRAPH")
		return nil
	}
	payload, err := json.Marshal(types.ExtractChunkPayload{
		TenantID: tenantID,
		ChunkID:  chunkID,
		ModelID:  modelID,
	})
	if err != nil {
		return err
	}
	task := asynq.NewTask(types.TypeChunkExtract, payload, asynq.MaxRetry(3))
	info, err := client.Enqueue(task)
	if err != nil {
		logger.Errorf(ctx, "failed to enqueue task: %v", err)
		return fmt.Errorf("failed to enqueue task: %v", err)
	}
	logger.Infof(ctx, "enqueued task: id=%s queue=%s chunk=%s", info.ID, info.Queue, chunkID)
	return nil
}

type ChunkExtractService struct {
	template          *types.PromptTemplateStructured
	modelService      interfaces.ModelService
	knowledgeBaseRepo interfaces.KnowledgeBaseRepository
	chunkRepo         interfaces.ChunkRepository
	graphEngine       interfaces.RetrieveGraphRepository
}

func NewChunkExtractService(
	config *config.Config,
	modelService interfaces.ModelService,
	knowledgeBaseRepo interfaces.KnowledgeBaseRepository,
	chunkRepo interfaces.ChunkRepository,
	graphEngine interfaces.RetrieveGraphRepository,
) interfaces.Extracter {
	generator := chatpipline.NewQAPromptGenerator(chatpipline.NewFormater(), config.ExtractManager.ExtractGraph)
	ctx := context.Background()
	logger.Debugf(ctx, "chunk extract system prompt: %s", generator.System(ctx))
	logger.Debugf(ctx, "chunk extract user prompt: %s", generator.User(ctx, "demo"))
	return &ChunkExtractService{
		template:          config.ExtractManager.ExtractGraph,
		modelService:      modelService,
		knowledgeBaseRepo: knowledgeBaseRepo,
		chunkRepo:         chunkRepo,
		graphEngine:       graphEngine,
	}
}

func (s *ChunkExtractService) Extract(ctx context.Context, t *asynq.Task) error {
	var p types.ExtractChunkPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		logger.Errorf(ctx, "failed to unmarshal task payload: %v", err)
		return err
	}
	ctx = logger.WithRequestID(ctx, uuid.New().String())
	ctx = logger.WithField(ctx, "extract", p.ChunkID)
	ctx = context.WithValue(ctx, types.TenantIDContextKey, p.TenantID)

	chunk, err := s.chunkRepo.GetChunkByID(ctx, p.TenantID, p.ChunkID)
	if err != nil {
		logger.Errorf(ctx, "failed to get chunk: %v", err)
		return err
	}
	kb, err := s.knowledgeBaseRepo.GetKnowledgeBaseByID(ctx, chunk.KnowledgeBaseID)
	if err != nil {
		logger.Errorf(ctx, "failed to get knowledge base: %v", err)
		return err
	}
	if kb.ExtractConfig == nil {
		logger.Warnf(ctx, "failed to get extract config")
		return err
	}

	chatModel, err := s.modelService.GetChatModel(ctx, p.ModelID)
	if err != nil {
		logger.Errorf(ctx, "failed to get chat model: %v", err)
		return err
	}

	template := &types.PromptTemplateStructured{
		Description: s.template.Description,
		Tags:        kb.ExtractConfig.Tags,
		Examples: []types.GraphData{
			{
				Text:     kb.ExtractConfig.Text,
				Node:     kb.ExtractConfig.Nodes,
				Relation: kb.ExtractConfig.Relations,
			},
		},
	}
	extractor := chatpipline.NewExtractor(chatModel, template)
	graph, err := extractor.Extract(ctx, chunk.Content)
	if err != nil {
		return err
	}

	chunk, err = s.chunkRepo.GetChunkByID(ctx, p.TenantID, p.ChunkID)
	if err != nil {
		logger.Warnf(ctx, "graph ignore chunk %s: %v", p.ChunkID, err)
		return nil
	}

	for _, node := range graph.Node {
		node.Chunks = []string{chunk.ID}
	}
	if err = s.graphEngine.AddGraph(ctx,
		types.NameSpace{KnowledgeBase: chunk.KnowledgeBaseID, Knowledge: chunk.KnowledgeID},
		[]*types.GraphData{graph},
	); err != nil {
		logger.Errorf(ctx, "failed to add graph: %v", err)
		return err
	}
	// gg, _ := json.Marshal(graph)
	// logger.Infof(ctx, "extracted graph: %s", string(gg))
	return nil
}
