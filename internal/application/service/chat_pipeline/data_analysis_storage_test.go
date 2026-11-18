package chatpipeline

import (
	"context"
	"database/sql"
	"testing"

	filesvc "github.com/Tencent/WeKnora/internal/application/service/file"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	_ "github.com/duckdb/duckdb-go/v2"
	"github.com/stretchr/testify/require"
)

type analysisStorageKnowledgeService struct {
	interfaces.KnowledgeService
	knowledge *types.Knowledge
}

func (s *analysisStorageKnowledgeService) GetKnowledgeByID(context.Context, string) (*types.Knowledge, error) {
	return s.knowledge, nil
}

func (s *analysisStorageKnowledgeService) GetKnowledgeByIDOnly(context.Context, string) (*types.Knowledge, error) {
	return s.knowledge, nil
}

type analysisStorageKBService struct {
	interfaces.KnowledgeBaseService
	kb *types.KnowledgeBase
}

func (s *analysisStorageKBService) GetKnowledgeBaseByID(context.Context, string) (*types.KnowledgeBase, error) {
	return s.kb, nil
}

type analysisStorageResolver struct {
	interfaces.StorageBackendResolver
	service interfaces.FileService
	calls   int
}

func (r *analysisStorageResolver) ResolveFileService(
	_ context.Context, _ *types.Tenant, _, _, _ string,
) (interfaces.FileService, string, error) {
	r.calls++
	return r.service, "local", nil
}

func TestQuickAnswerDataAnalysisUsesWorkspaceStorage(t *testing.T) {
	ctx := context.Background()
	backendID := "workspace-storage"
	workspaceFiles := filesvc.NewLocalFileService(t.TempDir(), "")
	path, err := workspaceFiles.SaveBytes(ctx, []byte("amount\n42\n58\n"), 7, "data.csv", false)
	require.NoError(t, err)
	knowledge := &types.Knowledge{
		ID: testKnowledgeID, KnowledgeBaseID: "kb", TenantID: 7,
		FileType: "csv", FileName: "data.csv", FilePath: "storage://" + backendID + "/" + path,
	}
	resolver := &analysisStorageResolver{
		service: filesvc.NewBackendScopedFileService(backendID, workspaceFiles),
	}
	db, err := sql.Open("duckdb", "")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	model := &scriptedChat{replies: []string{
		`{"needs_analysis":true,"sql":"SELECT COUNT(*) AS total FROM dataset"}`,
	}}
	plugin := NewPluginDataAnalysis(
		NewEventManager(), &stubModelService{model: model},
		&analysisStorageKBService{kb: &types.KnowledgeBase{ID: "kb", TenantID: 7}},
		&analysisStorageKnowledgeService{knowledge: knowledge},
		filesvc.NewLocalFileService(t.TempDir(), ""), nil, nil, db, resolver,
	)
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, &types.Tenant{
		ID: 7, DefaultStorageBackendID: &backendID,
	})
	turn := &types.ChatManage{}
	turn.SessionID = "storage-test"
	turn.Query = "count rows"
	turn.MergeResult = []*types.SearchResult{{KnowledgeID: knowledge.ID, KnowledgeFilename: knowledge.FileName}}
	continued := false
	pluginErr := plugin.OnEvent(ctx, types.DATA_ANALYSIS, turn, func() *PluginError {
		continued = true
		return nil
	})
	require.Nil(t, pluginErr)
	require.True(t, continued)
	require.NotZero(t, resolver.calls)
	require.Len(t, turn.MergeResult, 2)
	require.Equal(t, types.MatchTypeDataAnalysis, turn.MergeResult[1].MatchType)
	require.Contains(t, turn.MergeResult[1].Content, `{"total":"2"}`)
}
