package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

// imageVectorSyncEngine records which of the two sync strategies ran: the
// distinction is the point, since re-indexing an image vector chunk would
// rebuild it from `![image](url)` — a markdown link, not an image.
type imageVectorSyncEngine struct {
	interfaces.RetrieveEngineService
	enabledUpdates map[string]bool
	deletedIDs     []string
	indexed        []*types.IndexInfo
}

// Support and EngineType are what NewCompositeRetrieveEngine asks a registry
// entry before it will accept it for a retriever type.
func (e *imageVectorSyncEngine) Support() []types.RetrieverType {
	return []types.RetrieverType{types.VectorRetrieverType}
}

func (e *imageVectorSyncEngine) EngineType() types.RetrieverEngineType {
	return types.SQLiteRetrieverEngineType
}

func (e *imageVectorSyncEngine) BatchUpdateChunkEnabledStatus(_ context.Context, status map[string]bool) error {
	if e.enabledUpdates == nil {
		e.enabledUpdates = map[string]bool{}
	}
	for id, v := range status {
		e.enabledUpdates[id] = v
	}
	return nil
}

func (e *imageVectorSyncEngine) DeleteByChunkIDList(_ context.Context, ids []string, _ int, _ string) error {
	e.deletedIDs = append(e.deletedIDs, ids...)
	return nil
}

func (e *imageVectorSyncEngine) BatchIndex(
	_ context.Context, _ embedding.Embedder, infos []*types.IndexInfo, _ []types.RetrieverType,
) error {
	e.indexed = append(e.indexed, infos...)
	return nil
}

type imageVectorSyncRegistry struct {
	interfaces.RetrieveEngineRegistry
	engine interfaces.RetrieveEngineService
}

func (r imageVectorSyncRegistry) GetRetrieveEngineService(
	types.RetrieverEngineType,
) (interfaces.RetrieveEngineService, error) {
	return r.engine, nil
}

type imageVectorSyncKBRepo struct {
	interfaces.KnowledgeBaseRepository
	kb *types.KnowledgeBase
}

func (r imageVectorSyncKBRepo) GetKnowledgeBaseByID(context.Context, string) (*types.KnowledgeBase, error) {
	return r.kb, nil
}

type imageVectorSyncKnowledgeRepo struct {
	interfaces.KnowledgeRepository
}

func (r imageVectorSyncKnowledgeRepo) GetKnowledgeByID(
	_ context.Context, _ uint64, id string,
) (*types.Knowledge, error) {
	return &types.Knowledge{ID: id, KnowledgeBaseID: "kb-sync", TenantID: 1}, nil
}

// newImageVectorSyncService wires the minimum chunkService around a recording
// engine. kb.VectorStoreID is left nil so CreateRetrieveEngineForKB takes the
// unbound path, which needs no ownership checker.
func newImageVectorSyncService(engine *imageVectorSyncEngine) *chunkService {
	kb := &types.KnowledgeBase{
		ID:               "kb-sync",
		EmbeddingModelID: "model-1",
		// VectorEnabled makes NeedsEmbeddingModel true, which is the gate at the
		// top of syncChunkIndex.
		IndexingStrategy: types.IndexingStrategy{VectorEnabled: true, ImageVectorEnabled: true},
	}
	return &chunkService{
		kbRepository:   imageVectorSyncKBRepo{kb: kb},
		knowledgeRepo:  imageVectorSyncKnowledgeRepo{},
		modelService:   &capabilityModelService{embedder: &capabilityEmbedder{supportsImage: true}},
		retrieveEngine: imageVectorSyncRegistry{engine: engine},
	}
}

func imageVectorSyncContext() context.Context {
	// Engines are set explicitly rather than relying on the RETRIEVE_DRIVER
	// default, so the composite always wraps at least one engine.
	tenant := &types.Tenant{
		RetrieverEngines: types.RetrieverEngines{
			Engines: []types.RetrieverEngineParams{{
				RetrieverEngineType: types.SQLiteRetrieverEngineType,
				RetrieverType:       types.VectorRetrieverType,
			}},
		},
	}
	ctx := context.WithValue(context.Background(), types.TenantInfoContextKey, tenant)
	return context.WithValue(ctx, types.TenantIDContextKey, uint64(1))
}

func imageVectorChunk(isEnabled bool) *types.Chunk {
	return &types.Chunk{
		ID:              "chunk-image-vector",
		ChunkType:       types.ChunkTypeImageVector,
		KnowledgeID:     "knowledge-sync",
		KnowledgeBaseID: "kb-sync",
		TenantID:        1,
		IsEnabled:       isEnabled,
		// What the chunk actually stores. Anything derived from this is a
		// markdown link, not the picture.
		Content: "![image](https://example.test/red.png)",
	}
}

// Guards the regression where editing a chunk destroyed the image vectors of
// its images: their vector cannot be rebuilt from content, so re-syncing
// replaced the image embedding with that of a markdown link — no error, the
// image just stopped being retrievable.
func TestSyncChunkIndexPreservesImageVectors(t *testing.T) {
	for _, tc := range []struct {
		name      string
		isEnabled bool
	}{
		{"enabling", true},
		{"disabling", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine := &imageVectorSyncEngine{}
			svc := newImageVectorSyncService(engine)

			require.NoError(t, svc.syncChunkIndex(imageVectorSyncContext(), imageVectorChunk(tc.isEnabled)))

			require.Empty(t, engine.deletedIDs,
				"the image vector must not be deleted; the image bytes have not changed")
			require.Empty(t, engine.indexed,
				"the image vector must not be rebuilt from content — that produces the embedding of a markdown link")
			require.Equal(t, map[string]bool{"chunk-image-vector": tc.isEnabled}, engine.enabledUpdates,
				"visibility is all that changed, and retrieval filters on is_enabled")
		})
	}
}

// Control: the image vector branch must not swallow ordinary chunks, which do
// need their vector rebuilt from content.
func TestSyncChunkIndexStillReindexesTextChunks(t *testing.T) {
	engine := &imageVectorSyncEngine{}
	svc := newImageVectorSyncService(engine)

	chunk := imageVectorChunk(true)
	chunk.ChunkType = types.ChunkTypeText
	chunk.Content = "一段关于红色消防车的说明文字"

	require.NoError(t, svc.syncChunkIndex(imageVectorSyncContext(), chunk))

	require.Equal(t, []string{"chunk-image-vector"}, engine.deletedIDs, "text chunks still get re-embedded")
	require.Len(t, engine.indexed, 1)
	require.Contains(t, engine.indexed[0].Content, "红色消防车")
	require.Empty(t, engine.enabledUpdates, "a text chunk has nothing to toggle")
}
