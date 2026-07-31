package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type folderIndexKBService struct {
	interfaces.KnowledgeBaseService
	kb *types.KnowledgeBase
}

func (s *folderIndexKBService) GetKnowledgeBaseByID(context.Context, string) (*types.KnowledgeBase, error) {
	return s.kb, nil
}

type folderIndexChunkRepository struct {
	interfaces.ChunkRepository
	chunks map[string][]*types.Chunk
}

func (r *folderIndexChunkRepository) ListAllChunksByKnowledgeID(
	_ context.Context,
	_ uint64,
	knowledgeID string,
) ([]*types.Chunk, error) {
	return r.chunks[knowledgeID], nil
}

type folderIndexRetrieveEngine struct {
	interfaces.RetrieveEngineService
	updated   map[string]string
	callCount int
}

func (e *folderIndexRetrieveEngine) EngineType() types.RetrieverEngineType {
	return types.SQLiteRetrieverEngineType
}

func (e *folderIndexRetrieveEngine) Support() []types.RetrieverType {
	return []types.RetrieverType{types.VectorRetrieverType}
}

func (e *folderIndexRetrieveEngine) BatchUpdateChunkFolderID(
	_ context.Context,
	chunkFolderMap map[string]string,
) error {
	if e.updated == nil {
		e.updated = make(map[string]string, len(chunkFolderMap))
	}
	e.callCount++
	for chunkID, folderID := range chunkFolderMap {
		e.updated[chunkID] = folderID
	}
	return nil
}

type folderIndexRegistry struct {
	interfaces.RetrieveEngineRegistry
	engine interfaces.RetrieveEngineService
}

func (r *folderIndexRegistry) GetRetrieveEngineService(
	_ types.RetrieverEngineType,
) (interfaces.RetrieveEngineService, error) {
	return r.engine, nil
}

func TestKnowledgeService_UpdateKnowledgeFolderIndexIncludesEveryChunkType(t *testing.T) {
	engine := &folderIndexRetrieveEngine{}
	service := &knowledgeService{
		kbService: &folderIndexKBService{kb: &types.KnowledgeBase{
			ID:       "kb-1",
			TenantID: 1,
		}},
		chunkRepo: &folderIndexChunkRepository{chunks: map[string][]*types.Chunk{
			"doc-1": {
				{ID: "text-chunk", ChunkType: types.ChunkTypeText},
				{ID: "summary-chunk", ChunkType: types.ChunkTypeSummary},
			},
		}},
		retrieveEngine: &folderIndexRegistry{engine: engine},
	}
	tenant := &types.Tenant{
		ID: 1,
		RetrieverEngines: types.RetrieverEngines{Engines: []types.RetrieverEngineParams{
			{
				RetrieverEngineType: types.SQLiteRetrieverEngineType,
				RetrieverType:       types.VectorRetrieverType,
			},
		}},
	}
	ctx := context.WithValue(context.Background(), types.TenantInfoContextKey, tenant)

	err := service.UpdateKnowledgeFolderIndex(ctx, "kb-1", []string{"doc-1"}, types.DocumentFolderRootID)

	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"text-chunk":    types.DocumentFolderRootID,
		"summary-chunk": types.DocumentFolderRootID,
	}, engine.updated)
	assert.Equal(t, 1, engine.callCount)
}

func TestKnowledgeService_UpdateKnowledgeFolderIndexBatchesLargeSubtrees(t *testing.T) {
	engine := &folderIndexRetrieveEngine{}
	chunks := make([]*types.Chunk, 0, folderIndexUpdateBatchSize+1)
	for index := 0; index <= folderIndexUpdateBatchSize; index++ {
		chunks = append(chunks, &types.Chunk{ID: fmt.Sprintf("chunk-%d", index)})
	}
	service := &knowledgeService{
		kbService: &folderIndexKBService{kb: &types.KnowledgeBase{
			ID:       "kb-1",
			TenantID: 1,
		}},
		chunkRepo: &folderIndexChunkRepository{chunks: map[string][]*types.Chunk{
			"doc-1": chunks,
		}},
		retrieveEngine: &folderIndexRegistry{engine: engine},
	}
	tenant := &types.Tenant{
		ID: 1,
		RetrieverEngines: types.RetrieverEngines{Engines: []types.RetrieverEngineParams{
			{
				RetrieverEngineType: types.SQLiteRetrieverEngineType,
				RetrieverType:       types.VectorRetrieverType,
			},
		}},
	}
	ctx := context.WithValue(context.Background(), types.TenantInfoContextKey, tenant)

	err := service.UpdateKnowledgeFolderIndex(
		ctx,
		"kb-1",
		[]string{"doc-1"},
		types.DocumentFolderRootID,
	)

	require.NoError(t, err)
	assert.Equal(t, folderIndexUpdateBatchSize+1, len(engine.updated))
	assert.Equal(t, 2, engine.callCount)
}
