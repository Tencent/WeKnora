package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	filesvc "github.com/Tencent/WeKnora/internal/application/service/file"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	_ "github.com/duckdb/duckdb-go/v2"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

type tableSummaryChatResult struct {
	response *types.ChatResponse
	err      error
}

type tableSummaryChatAPI = chat.Chat

type tableSummaryChat struct {
	tableSummaryChatAPI
	results []tableSummaryChatResult
	calls   int
}

func (c *tableSummaryChat) Chat(context.Context, []chat.Message, *chat.ChatOptions) (*types.ChatResponse, error) {
	result := c.results[c.calls]
	c.calls++
	return result.response, result.err
}

type tableSummaryModelService struct {
	interfaces.ModelService
	model chat.Chat
}

func (s tableSummaryModelService) GetChatModel(context.Context, string) (chat.Chat, error) {
	return s.model, nil
}

func (s tableSummaryModelService) GetEmbeddingModel(context.Context, string) (embedding.Embedder, error) {
	return nil, nil // The recording index below does not call an embedding provider.
}

type tableSummaryTenantService struct {
	interfaces.TenantService
}

func (tableSummaryTenantService) GetTenantByID(_ context.Context, id uint64) (*types.Tenant, error) {
	return &types.Tenant{ID: id, RetrieverEngines: types.RetrieverEngines{Engines: []types.RetrieverEngineParams{{
		RetrieverType: types.VectorRetrieverType, RetrieverEngineType: types.PostgresRetrieverEngineType,
	}}}}, nil
}

type tableSummaryIndex struct {
	interfaces.RetrieveEngineService
	indexed []*types.IndexInfo
}

func (*tableSummaryIndex) EngineType() types.RetrieverEngineType {
	return types.PostgresRetrieverEngineType
}

func (*tableSummaryIndex) Support() []types.RetrieverType {
	return []types.RetrieverType{types.VectorRetrieverType}
}

func (e *tableSummaryIndex) BatchIndex(
	_ context.Context, _ embedding.Embedder, infos []*types.IndexInfo, _ []types.RetrieverType,
) error {
	e.indexed = append(e.indexed, infos...)
	return nil
}

type tableSummaryRegistry struct {
	interfaces.RetrieveEngineRegistry
	index *tableSummaryIndex
}

func (r tableSummaryRegistry) GetRetrieveEngineService(
	types.RetrieverEngineType,
) (interfaces.RetrieveEngineService, error) {
	return r.index, nil
}

// Exercise the handler boundary with a real CSV, DuckDB and chunk repository;
// model responses and vector indexing stay local through recording fixtures.
func newTableSummaryOutputFixture(t *testing.T, results ...tableSummaryChatResult) (
	*DataTableSummaryService, *asynq.Task, *documentWriteFixture, *tableSummaryChat, *tableSummaryIndex,
) {
	t.Helper()
	f := newDocumentWriteFixture(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "sales.csv")
	require.NoError(t, os.WriteFile(path, []byte("product,quantity\nbrush,3\nshampoo,2\n"), 0o600))
	require.NoError(t, f.db.Model(&types.Knowledge{}).Where("id = ?", "doc").Updates(map[string]any{
		"file_type": "csv", "file_path": path, "parse_status": types.ParseStatusCompleted,
	}).Error)
	db, err := sql.Open("duckdb", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	model := &tableSummaryChat{results: results}
	index := &tableSummaryIndex{}
	svc := &DataTableSummaryService{
		knowledgeService: f.svc, knowledgeBaseService: f.kbs, chunkService: f.chunks,
		modelService: tableSummaryModelService{model: model}, tenantService: tableSummaryTenantService{},
		fileService: filesvc.NewLocalFileService(dir, ""), sqlDB: db,
		retrieveEngine: tableSummaryRegistry{index: index},
	}
	payload, err := json.Marshal(DataTableSummaryPayload{TenantID: 7, KnowledgeID: "doc"})
	require.NoError(t, err)
	return svc, asynq.NewTask(types.TypeDataTableSummary, payload), f, model, index
}

func TestDataTableSummaryHandleRejectsInvalidOutput(t *testing.T) {
	providerErr := errors.New("provider unavailable")
	for stage, name := range []string{"table", "column"} {
		for _, tc := range []struct {
			name   string
			result tableSummaryChatResult
			want   error
		}{
			{name: "nil", want: errEmptySummaryOutput},
			{
				name: "whitespace", result: tableSummaryChatResult{response: &types.ChatResponse{Content: " \n\t "}},
				want: errEmptySummaryOutput,
			},
			{name: "provider error", result: tableSummaryChatResult{err: providerErr}, want: providerErr},
		} {
			t.Run(name+"/"+tc.name, func(t *testing.T) {
				results := []tableSummaryChatResult{
					{response: &types.ChatResponse{Content: "Product quantities"}},
					{response: &types.ChatResponse{Content: "Column details"}},
				}
				results[stage] = tc.result
				svc, task, f, model, index := newTableSummaryOutputFixture(t, results...)
				err := svc.Handle(context.Background(), task)
				require.ErrorIs(t, err, tc.want)
				require.NotErrorIs(t, err, asynq.SkipRetry)
				require.Equal(t, stage+1, model.calls, "generation must stop at the failing stage")
				require.Empty(t, index.indexed)
				chunks, err := f.chunkRepo.ListAllChunksByKnowledgeID(f.ctx, 7, "doc")
				require.NoError(t, err)
				require.Len(t, chunks, 1, "neither summary chunk may be published on failure")
				require.Equal(t, "original", chunks[0].Content)
				knowledge, err := f.svc.GetKnowledgeByID(f.ctx, "doc")
				require.NoError(t, err)
				require.Equal(t, types.ParseStatusCompleted, knowledge.ParseStatus)
			})
		}
	}
}

func TestDataTableSummaryHandlePreservesValidOutput(t *testing.T) {
	svc, task, f, model, index := newTableSummaryOutputFixture(t,
		tableSummaryChatResult{response: &types.ChatResponse{Content: " \nProduct quantities\n"}},
		tableSummaryChatResult{response: &types.ChatResponse{Content: "\n- product: item name\n- quantity: units\n"}},
	)
	require.NoError(t, svc.Handle(context.Background(), task))
	require.Equal(t, 2, model.calls)
	chunks, err := f.chunkRepo.ListChunksByKnowledgeIDAndTypes(f.ctx, 7, "doc", []types.ChunkType{
		types.ChunkTypeTableSummary, types.ChunkTypeTableColumn,
	})
	require.NoError(t, err)
	require.Len(t, chunks, 2)
	byType := make(map[types.ChunkType]*types.Chunk, len(chunks))
	for _, chunk := range chunks {
		byType[chunk.ChunkType] = chunk
		require.Equal(t, int(types.ChunkStatusIndexed), chunk.Status)
	}
	summary, column := byType[types.ChunkTypeTableSummary], byType[types.ChunkTypeTableColumn]
	require.NotNil(t, summary)
	require.NotNil(t, column)
	require.Equal(t, "# Table Summary\n\nTable name: dataset\n\n \nProduct quantities\n", summary.Content)
	require.Equal(t,
		"# Table Column Information\n\nTable name: dataset\n\n\n- product: item name\n- quantity: units\n",
		column.Content,
	)
	require.Equal(t, summary.ID, column.ParentChunkID)
	require.Len(t, index.indexed, 2)
	indexedContent := make(map[string]string, len(index.indexed))
	for _, info := range index.indexed {
		indexedContent[info.ChunkID] = info.Content
	}
	require.Equal(t, map[string]string{summary.ID: summary.Content, column.ID: column.Content}, indexedContent)
}
