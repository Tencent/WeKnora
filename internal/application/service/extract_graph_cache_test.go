package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type graphCacheModelService struct {
	interfaces.ModelService
	chatModel *chatArtifactFakeModel
	model     *types.Model
}

func (s *graphCacheModelService) GetChatModel(context.Context, string) (chat.Chat, error) {
	return s.chatModel, nil
}

func (s *graphCacheModelService) GetModelByID(context.Context, string) (*types.Model, error) {
	return s.model, nil
}

type graphCacheChunkRepository struct {
	interfaces.ChunkRepository
	chunk     *types.Chunk
	calls     int
	failAfter int
	err       error
}

func (r *graphCacheChunkRepository) GetChunkByID(context.Context, uint64, string) (*types.Chunk, error) {
	r.calls++
	if r.failAfter > 0 && r.calls > r.failAfter {
		return nil, r.err
	}
	copy := *r.chunk
	return &copy, nil
}

type graphCacheKnowledgeBaseRepository struct {
	interfaces.KnowledgeBaseRepository
	kb *types.KnowledgeBase
}

func (r *graphCacheKnowledgeBaseRepository) GetKnowledgeBaseByID(
	context.Context,
	string,
) (*types.KnowledgeBase, error) {
	return r.kb, nil
}

type graphCacheRepository struct {
	interfaces.RetrieveGraphRepository
	graphs   []*types.GraphData
	attempts []int
}

func (r *graphCacheRepository) AddGraph(
	_ context.Context,
	_ types.NameSpace,
	graphs []*types.GraphData,
) error {
	return errors.New("legacy AddGraph must not be used by the chunk extraction worker")
}

func (r *graphCacheRepository) ReplaceGraphChunk(
	_ context.Context,
	_ types.NameSpace,
	_ string,
	attempt int,
	graph *types.GraphData,
) error {
	r.attempts = append(r.attempts, attempt)
	r.graphs = append(r.graphs, graph)
	return nil
}

func TestChunkExtractServiceReusesGraphArtifact(t *testing.T) {
	service, modelService, chunkRepo, graphRepo, _ := newGraphCacheChunkExtractService()
	task := newGraphCacheExtractTask(t)

	require.NoError(t, service.Handle(context.Background(), task))
	require.NoError(t, service.Handle(context.Background(), task))

	assert.Equal(t, 1, modelService.chatModel.calls)
	assert.Equal(t, 4, chunkRepo.calls)
	require.Len(t, graphRepo.graphs, 2)
	assert.Equal(t, []int{3, 3}, graphRepo.attempts)
	for _, graph := range graphRepo.graphs {
		require.Len(t, graph.Node, 2)
		require.Len(t, graph.Relation, 1)
		assert.Equal(t, []string{"chunk-1"}, graph.Node[0].Chunks)
		assert.Equal(t, []string{"chunk-1"}, graph.Relation[0].Chunks)
	}
}

func TestChunkExtractServiceBypassesCacheForUnsafeModelRevision(t *testing.T) {
	service, modelService, _, graphRepo, store := newGraphCacheChunkExtractService()
	modelService.model.UpdatedAt = time.Time{}
	task := newGraphCacheExtractTask(t)

	require.NoError(t, service.Handle(context.Background(), task))
	require.NoError(t, service.Handle(context.Background(), task))

	assert.Equal(t, 2, modelService.chatModel.calls)
	assert.Zero(t, store.getCalls)
	assert.Zero(t, store.putCalls)
	assert.Len(t, graphRepo.graphs, 2)
}

func TestChunkExtractServiceStoreFailureDoesNotWriteGraph(t *testing.T) {
	service, modelService, _, graphRepo, store := newGraphCacheChunkExtractService()
	want := errors.New("store unavailable")
	store.getErr = want

	err := service.Handle(context.Background(), newGraphCacheExtractTask(t))

	require.ErrorIs(t, err, want)
	assert.Zero(t, modelService.chatModel.calls)
	assert.Empty(t, graphRepo.graphs)
}

func TestChunkExtractServiceDoesNotWriteGraphAfterChunkDisappears(t *testing.T) {
	service, modelService, chunkRepo, graphRepo, store := newGraphCacheChunkExtractService()
	chunkRepo.failAfter = 1
	chunkRepo.err = sql.ErrNoRows

	err := service.Handle(context.Background(), newGraphCacheExtractTask(t))

	require.NoError(t, err)
	assert.Equal(t, 1, modelService.chatModel.calls)
	assert.Equal(t, 1, store.putCalls)
	assert.Empty(t, graphRepo.graphs)
}

func TestNewChunkExtractServiceInjectsArtifactStore(t *testing.T) {
	store := newChatArtifactFakeStore()

	handler := NewChunkExtractService(
		&config.Config{ExtractManager: &config.ExtractManagerConfig{}},
		nil, nil, nil, nil, nil, store, nil,
	)

	service, ok := handler.(*ChunkExtractService)
	require.True(t, ok)
	assert.Same(t, store, service.artifactStore)
}

func newGraphCacheChunkExtractService() (
	*ChunkExtractService,
	*graphCacheModelService,
	*graphCacheChunkRepository,
	*graphCacheRepository,
	*chatArtifactFakeStore,
) {
	modelService := &graphCacheModelService{
		chatModel: &chatArtifactFakeModel{
			modelID:   "model-1",
			modelName: "chat",
			response: &types.ChatResponse{Content: `[
				{"entity":"Alpha","entity_attributes":["person"]},
				{"entity":"Beta","entity_attributes":["person"]},
				{"entity1":"Alpha","entity2":"Beta","relation":"knows"}
			]`},
		},
		model: &types.Model{
			ID:        "model-1",
			Name:      "chat",
			UpdatedAt: time.Unix(100, 0),
		},
	}
	chunkRepo := &graphCacheChunkRepository{chunk: &types.Chunk{
		ID:              "chunk-1",
		TenantID:        7,
		KnowledgeID:     "knowledge-1",
		KnowledgeBaseID: "kb-1",
		Content:         "same content",
	}}
	graphRepo := &graphCacheRepository{}
	store := newChatArtifactFakeStore()
	return &ChunkExtractService{
		template: &types.PromptTemplateStructured{
			Description: "Extract entities using these relation tags: %s",
		},
		modelService: modelService,
		knowledgeBaseRepo: &graphCacheKnowledgeBaseRepository{kb: &types.KnowledgeBase{
			ID: "kb-1",
			ExtractConfig: &types.ExtractConfig{
				Enabled: true,
				Tags:    []string{"knows"},
			},
		}},
		chunkRepo:     chunkRepo,
		graphEngine:   graphRepo,
		artifactStore: store,
	}, modelService, chunkRepo, graphRepo, store
}

func newGraphCacheExtractTask(t *testing.T) *asynq.Task {
	t.Helper()
	payload, err := json.Marshal(types.ExtractChunkPayload{
		TenantID: 7,
		ChunkID:  "chunk-1",
		ModelID:  "model-1",
		Attempt:  3,
	})
	require.NoError(t, err)
	return asynq.NewTask(types.TypeChunkExtract, payload)
}
