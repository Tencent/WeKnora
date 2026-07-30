package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type dataSchemaFeedbackKnowledgeService struct {
	interfaces.KnowledgeService
	knowledge *types.Knowledge
}

func (s *dataSchemaFeedbackKnowledgeService) GetKnowledgeByIDOnly(_ context.Context, _ string) (*types.Knowledge, error) {
	return s.knowledge, nil
}

type dataSchemaFeedbackChunkRepo struct {
	interfaces.ChunkRepository
	chunks []*types.Chunk
}

func (r *dataSchemaFeedbackChunkRepo) ListPagedChunksByKnowledgeID(
	context.Context,
	uint64,
	string,
	*types.Pagination,
	[]types.ChunkType,
	[]string,
	string,
	string,
	string,
	string,
	*bool,
) ([]*types.Chunk, int64, error) {
	return r.chunks, int64(len(r.chunks)), nil
}

func TestDataSchemaCarriesOnlyChunksActuallyExposedToModel(t *testing.T) {
	repo := &dataSchemaFeedbackChunkRepo{chunks: []*types.Chunk{
		{ID: "summary-old", ChunkType: types.ChunkTypeTableSummary, Content: "old summary"},
		{ID: "summary-used", KnowledgeID: "document-1", KnowledgeBaseID: "kb-1", ChunkType: types.ChunkTypeTableSummary, Content: "used summary", RecallWeight: 1.5},
		{ID: "columns-used", KnowledgeID: "document-1", KnowledgeBaseID: "kb-1", ChunkType: types.ChunkTypeTableColumn, Content: "used columns", RecallWeight: 0.5},
	}}
	tool := NewDataSchemaTool(
		&dataSchemaFeedbackKnowledgeService{knowledge: &types.Knowledge{
			ID:              "document-1",
			KnowledgeBaseID: "kb-1",
			TenantID:        7,
			Title:           "Dataset",
		}},
		repo,
	)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"knowledge_id":"document-1"}`))

	require.NoError(t, err)
	require.True(t, result.Success)
	require.Equal(t, "used summary\n\nused columns", result.Output)
	require.Equal(t, []string{"summary-used", "columns-used"}, []string{
		result.KnowledgeReferences[0].ID,
		result.KnowledgeReferences[1].ID,
	})
	require.Equal(t, "Dataset", result.KnowledgeReferences[0].KnowledgeTitle)
	require.Equal(t, 1.5, result.KnowledgeReferences[0].RecallWeight)
	require.Equal(t, 0.5, result.KnowledgeReferences[1].RecallWeight)
}
