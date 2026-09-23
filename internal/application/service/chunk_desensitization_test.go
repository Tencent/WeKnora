package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

func desensitizedMobileKB() *types.KnowledgeBase {
	return &types.KnowledgeBase{
		ID:               "kb",
		TenantID:         1,
		EmbeddingModelID: "embed-1",
		IndexingStrategy: types.IndexingStrategy{VectorEnabled: true},
		DesensitizationConfig: &types.DesensitizationConfig{
			Enabled:     true,
			Engine:      types.DesensitizationEngineBuiltin,
			EntityTypes: []string{types.DesensitizationEntityCNMobile},
		},
	}
}

func editorChunkContext() context.Context {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	tenant := &types.Tenant{
		ID: 1,
		RetrieverEngines: types.RetrieverEngines{Engines: []types.RetrieverEngineParams{{
			RetrieverType:       types.VectorRetrieverType,
			RetrieverEngineType: types.PostgresRetrieverEngineType,
		}}},
	}
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenant)
	return (&access.KBAccess{
		KnowledgeBase:     desensitizedMobileKB(),
		Caller:            types.CallerFromContext(ctx),
		EffectiveTenantID: 1,
		Permission:        types.OrgRoleEditor,
	}).Context(ctx)
}

type desensitizeChunkKBRepo struct {
	interfaces.KnowledgeBaseRepository
	kb *types.KnowledgeBase
}

func (r desensitizeChunkKBRepo) GetKnowledgeBaseByID(context.Context, string) (*types.KnowledgeBase, error) {
	return r.kb, nil
}

type capturingRetrieveEngine struct {
	interfaces.RetrieveEngineService
	indexed []*types.IndexInfo
	deleted []string
}

func (e *capturingRetrieveEngine) EngineType() types.RetrieverEngineType {
	return types.PostgresRetrieverEngineType
}

func (e *capturingRetrieveEngine) Support() []types.RetrieverType {
	return []types.RetrieverType{types.VectorRetrieverType}
}

func (e *capturingRetrieveEngine) DeleteByChunkIDList(_ context.Context, ids []string, _ int, _ string) error {
	e.deleted = append(e.deleted, ids...)
	return nil
}

func (e *capturingRetrieveEngine) BatchIndex(
	_ context.Context, _ embedding.Embedder, infos []*types.IndexInfo, _ []types.RetrieverType,
) error {
	e.indexed = append([]*types.IndexInfo(nil), infos...)
	return nil
}

type capturingRetrieveRegistry struct {
	interfaces.RetrieveEngineRegistry
	engine interfaces.RetrieveEngineService
}

func (r capturingRetrieveRegistry) GetRetrieveEngineService(
	types.RetrieverEngineType,
) (interfaces.RetrieveEngineService, error) {
	return r.engine, nil
}

type stubEmbedder struct{}

func (stubEmbedder) Embed(context.Context, string) ([]float32, error) { return []float32{1}, nil }

func (stubEmbedder) BatchEmbed(context.Context, []string) ([][]float32, error) {
	return [][]float32{{1}}, nil
}

func (stubEmbedder) BatchEmbedWithPool(context.Context, embedding.Embedder, []string) ([][]float32, error) {
	return [][]float32{{1}}, nil
}

func (stubEmbedder) GetModelName() string { return "stub" }

func (stubEmbedder) GetDimensions() int { return 1 }

func (stubEmbedder) GetModelID() string { return "stub" }

type stubEmbeddingModelService struct {
	interfaces.ModelService
}

func (stubEmbeddingModelService) GetEmbeddingModel(context.Context, string) (embedding.Embedder, error) {
	return stubEmbedder{}, nil
}

func TestUpdateDocumentChunkMasksBodyBeforePersist(t *testing.T) {
	const phone = "13800138000"
	kb := desensitizedMobileKB()
	kb.IndexingStrategy = types.IndexingStrategy{}
	repo := &editableChunkRepo{chunk: &types.Chunk{
		ID: "chunk", TenantID: 1, KnowledgeID: "knowledge", KnowledgeBaseID: "kb",
		Content: "old body", SourceContent: "old body", ContentRevision: 0,
		ChunkType: types.ChunkTypeText, IsEnabled: true, IndexStatus: "ready",
	}}
	service := &chunkService{
		chunkRepository: repo,
		knowledgeRepo:   editableChunkKnowledgeRepo{},
		kbRepository:    desensitizeChunkKBRepo{kb: kb},
	}
	newContent := "联系 " + phone
	updated, err := service.UpdateDocumentChunk(editorChunkContext(), "chunk", &newContent, nil, nil)
	require.NoError(t, err)
	require.NotContains(t, updated.Content, phone)
	require.NotContains(t, repo.chunk.Content, phone)
	require.Contains(t, updated.Content, "<手机号>")
}

func TestUpsertGeneratedQuestionMasksBeforePersist(t *testing.T) {
	const phone = "13800138000"
	kb := desensitizedMobileKB()
	kb.IndexingStrategy = types.IndexingStrategy{}
	repo := &editableChunkRepo{chunk: &types.Chunk{
		ID: "chunk", TenantID: 1, KnowledgeID: "knowledge", KnowledgeBaseID: "kb",
		Content: "body", SourceContent: "body", ContentRevision: 1,
		ChunkType: types.ChunkTypeText, IsEnabled: true, IndexStatus: "ready",
	}}
	service := &chunkService{
		chunkRepository: repo,
		knowledgeRepo:   editableChunkKnowledgeRepo{},
		kbRepository:    desensitizeChunkKBRepo{kb: kb},
	}
	got, err := service.UpsertGeneratedQuestion(editorChunkContext(), "chunk", "", "电话是"+phone)
	require.NoError(t, err)
	require.NotContains(t, got.Question, phone)
	require.Contains(t, got.Question, "<手机号>")
	meta, err := repo.chunk.DocumentMetadata()
	require.NoError(t, err)
	require.NotContains(t, meta.GeneratedQuestions[0].Question, phone)
}

func TestSyncChunkIndexMasksBodyAndQuestionBeforeBatchIndex(t *testing.T) {
	const phone = "13800138000"
	kb := desensitizedMobileKB()
	engine := &capturingRetrieveEngine{}
	metadata := &types.DocumentChunkMetadata{
		GeneratedQuestions: []types.GeneratedQuestion{{ID: "q1", Question: "谁的电话" + phone}},
	}
	metadataJSON, err := json.Marshal(metadata)
	require.NoError(t, err)
	chunk := &types.Chunk{
		ID: "chunk", TenantID: 1, KnowledgeID: "knowledge", KnowledgeBaseID: "kb",
		Content: "联系 " + phone, ContextHeader: "标题 " + phone,
		ChunkType: types.ChunkTypeText, IsEnabled: true, Metadata: metadataJSON,
	}
	service := &chunkService{
		chunkRepository: &editableChunkRepo{chunk: chunk},
		knowledgeRepo: &editableChunkKnowledgeRepoWithTitle{
			title: "劳动合同-张三-" + phone,
		},
		kbRepository:   desensitizeChunkKBRepo{kb: kb},
		modelService:   stubEmbeddingModelService{},
		retrieveEngine: capturingRetrieveRegistry{engine: engine},
	}

	require.NoError(t, service.syncChunkIndex(editorChunkContext(), chunk))
	require.Equal(t, []string{"chunk"}, engine.deleted)
	require.NotEmpty(t, engine.indexed)
	for _, item := range engine.indexed {
		require.NotContains(t, item.Content, phone, "IndexInfo.Content=%q", item.Content)
		require.Contains(t, item.Content, "<手机号>")
	}
	require.Equal(t, "劳动合同-张三-"+phone, mustKnowledgeTitle(t, service))
}

type editableChunkKnowledgeRepoWithTitle struct {
	interfaces.KnowledgeRepository
	title string
}

func (r editableChunkKnowledgeRepoWithTitle) GetKnowledgeByID(
	context.Context, uint64, string,
) (*types.Knowledge, error) {
	return &types.Knowledge{
		ID: "knowledge", TenantID: 1, KnowledgeBaseID: "kb", Title: r.title,
	}, nil
}

func mustKnowledgeTitle(t *testing.T, service *chunkService) string {
	t.Helper()
	knowledge, err := service.knowledgeRepo.GetKnowledgeByID(context.Background(), 1, "knowledge")
	require.NoError(t, err)
	return knowledge.Title
}

func TestSyncChunkIndexDoesNotDeleteUntilMaskingSucceeds(t *testing.T) {
	const phone = "13800138000"
	kb := desensitizedMobileKB()
	kb.DesensitizationConfig.Engine = types.DesensitizationEnginePresidio
	engine := &capturingRetrieveEngine{}
	chunk := &types.Chunk{
		ID: "chunk", TenantID: 1, KnowledgeID: "knowledge", KnowledgeBaseID: "kb",
		Content: "联系 " + phone, ChunkType: types.ChunkTypeText, IsEnabled: true,
	}
	service := &chunkService{
		knowledgeRepo:  editableChunkKnowledgeRepo{},
		kbRepository:   desensitizeChunkKBRepo{kb: kb},
		modelService:   stubEmbeddingModelService{},
		retrieveEngine: capturingRetrieveRegistry{engine: engine},
	}
	err := service.syncChunkIndex(editorChunkContext(), chunk)
	require.Error(t, err)
	require.Empty(t, engine.deleted)
	require.Empty(t, engine.indexed)
}
