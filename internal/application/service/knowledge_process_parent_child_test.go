package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

type parentChildKnowledgeRepo struct {
	interfaces.KnowledgeRepository
	knowledge *types.Knowledge
}

func (r *parentChildKnowledgeRepo) GetKnowledgeByID(
	context.Context, uint64, string,
) (*types.Knowledge, error) {
	return r.knowledge, nil
}

func (r *parentChildKnowledgeRepo) UpdateKnowledge(
	context.Context, *types.Knowledge,
) error {
	return nil
}

type parentChildChunkService struct {
	interfaces.ChunkService
	created []*types.Chunk
}

func (s *parentChildChunkService) DeleteChunksByKnowledgeID(context.Context, string) error {
	return nil
}

func (s *parentChildChunkService) CreateChunks(_ context.Context, chunks []*types.Chunk) error {
	s.created = append([]*types.Chunk(nil), chunks...)
	return nil
}

type parentChildModelService struct {
	interfaces.ModelService
	embedder embedding.Embedder
}

func (s parentChildModelService) GetEmbeddingModel(context.Context, string) (embedding.Embedder, error) {
	return s.embedder, nil
}

type parentChildEmbedder struct{}

func (parentChildEmbedder) Embed(context.Context, string) ([]float32, error) {
	return []float32{1}, nil
}

func (parentChildEmbedder) BatchEmbed(context.Context, []string) ([][]float32, error) {
	return [][]float32{{1}}, nil
}

func (parentChildEmbedder) BatchEmbedWithPool(
	context.Context, embedding.Embedder, []string,
) ([][]float32, error) {
	return [][]float32{{1}}, nil
}

func (parentChildEmbedder) GetModelName() string { return "parent-child-test" }
func (parentChildEmbedder) GetDimensions() int   { return 1 }
func (parentChildEmbedder) GetModelID() string   { return "parent-child-test" }

type parentChildRetrieveEngine struct {
	interfaces.RetrieveEngineService
	indexed []*types.IndexInfo
}

func (e *parentChildRetrieveEngine) EngineType() types.RetrieverEngineType {
	return types.PostgresRetrieverEngineType
}

func (e *parentChildRetrieveEngine) Support() []types.RetrieverType {
	return []types.RetrieverType{types.VectorRetrieverType}
}

func (e *parentChildRetrieveEngine) DeleteByKnowledgeIDList(
	context.Context, []string, int, string,
) error {
	return nil
}

func (e *parentChildRetrieveEngine) EstimateStorageSize(
	context.Context, embedding.Embedder, []*types.IndexInfo, []types.RetrieverType,
) int64 {
	return 0
}

func (e *parentChildRetrieveEngine) BatchIndex(
	_ context.Context,
	_ embedding.Embedder,
	infos []*types.IndexInfo,
	_ []types.RetrieverType,
) error {
	e.indexed = append([]*types.IndexInfo(nil), infos...)
	return nil
}

type parentChildRetrieveRegistry struct {
	interfaces.RetrieveEngineRegistry
	engine interfaces.RetrieveEngineService
}

func (r parentChildRetrieveRegistry) GetRetrieveEngineService(
	types.RetrieverEngineType,
) (interfaces.RetrieveEngineService, error) {
	return r.engine, nil
}

type parentChildGraphRepo struct {
	interfaces.RetrieveGraphRepository
}

func (parentChildGraphRepo) DelGraph(context.Context, []types.NameSpace) error {
	return nil
}

type parentChildTenantRepo struct {
	interfaces.TenantRepository
}

func (parentChildTenantRepo) AdjustStorageUsed(context.Context, uint64, int64) error {
	return nil
}

type parentChildTaskEnqueuer struct{}

func (parentChildTaskEnqueuer) Enqueue(*asynq.Task, ...asynq.Option) (*asynq.TaskInfo, error) {
	return nil, nil
}

func TestProcessChunksIndexesEveryTextChild(t *testing.T) {
	knowledge := &types.Knowledge{
		ID:              "knowledge-1",
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		ParseStatus:     types.ParseStatusProcessing,
	}
	chunkService := &parentChildChunkService{}
	retrieveEngine := &parentChildRetrieveEngine{}
	tenant := &types.Tenant{
		ID: 1,
		RetrieverEngines: types.RetrieverEngines{Engines: []types.RetrieverEngineParams{
			{
				RetrieverType:       types.VectorRetrieverType,
				RetrieverEngineType: types.PostgresRetrieverEngineType,
			},
		}},
	}
	ctx := context.WithValue(context.Background(), types.TenantInfoContextKey, tenant)
	svc := &knowledgeService{
		repo:           &parentChildKnowledgeRepo{knowledge: knowledge},
		chunkService:   chunkService,
		modelService:   parentChildModelService{embedder: parentChildEmbedder{}},
		retrieveEngine: parentChildRetrieveRegistry{engine: retrieveEngine},
		graphEngine:    parentChildGraphRepo{},
		tenantRepo:     parentChildTenantRepo{},
		task:           parentChildTaskEnqueuer{},
	}
	kb := &types.KnowledgeBase{
		ID:               "kb-1",
		TenantID:         1,
		EmbeddingModelID: "embedding-1",
		IndexingStrategy: types.IndexingStrategy{VectorEnabled: true},
	}
	chunks := []types.ParsedChunk{
		{Content: "linked child", Seq: 0, Start: 0, End: 12, ParentIndex: 0},
		{Content: "standalone child", Seq: 1, Start: 12, End: 28, ParentIndex: -1},
	}

	svc.processChunks(ctx, kb, knowledge, chunks, ProcessChunksOptions{
		ParentChunks: []types.ParsedParentChunk{
			{Content: "parent context", Seq: 0, Start: 0, End: 28},
		},
	})

	var textChunkIDs []string
	for _, chunk := range chunkService.created {
		if chunk.ChunkType == types.ChunkTypeText {
			textChunkIDs = append(textChunkIDs, chunk.ID)
		}
	}
	require.Len(t, textChunkIDs, 2)

	indexedSourceIDs := make([]string, 0, len(retrieveEngine.indexed))
	for _, info := range retrieveEngine.indexed {
		indexedSourceIDs = append(indexedSourceIDs, info.SourceID)
	}
	require.ElementsMatch(t, textChunkIDs, indexedSourceIDs)
}
