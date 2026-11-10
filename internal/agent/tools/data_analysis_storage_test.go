package tools

import (
	"context"
	"io"
	"testing"

	filesvc "github.com/Tencent/WeKnora/internal/application/service/file"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type dataAnalysisStorageResolver struct {
	interfaces.StorageBackendResolver
	service interfaces.FileService
	calls   int
}

func (r *dataAnalysisStorageResolver) ResolveFileService(
	_ context.Context, _ *types.Tenant, _, _, _ string,
) (interfaces.FileService, string, error) {
	r.calls++
	return r.service, "local", nil
}

func TestDataAnalysisResolvesWorkspaceStorage(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		name := "workspace_default"
		if explicit {
			name = "explicit_backend"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			backendID := "workspace-storage"
			workspaceFiles := filesvc.NewLocalFileService(t.TempDir(), "")
			filePath, err := workspaceFiles.SaveBytes(ctx, []byte("amount\n42\n"), 7, "data.csv", false)
			require.NoError(t, err)
			if explicit {
				filePath = "storage://" + backendID + "/" + filePath
			}
			resolver := &dataAnalysisStorageResolver{
				service: filesvc.NewBackendScopedFileService(backendID, workspaceFiles),
			}
			tool := NewDataAnalysisTool(
				&stubKnowledgeBaseService{kb: &types.KnowledgeBase{ID: "kb", TenantID: 7}},
				nil, nil, filesvc.NewLocalFileService(t.TempDir(), ""), nil, "storage-test", resolver,
			)
			ctx = context.WithValue(ctx, types.TenantInfoContextKey, &types.Tenant{
				ID: 7, DefaultStorageBackendID: &backendID,
			})
			knowledge := &types.Knowledge{ID: "document", KnowledgeBaseID: "kb", FilePath: filePath}
			reader, err := tool.resolveFileServiceForKnowledge(ctx, knowledge).GetFile(ctx, filePath)
			require.NoError(t, err)
			defer func() { _ = reader.Close() }()
			data, err := io.ReadAll(reader)
			require.NoError(t, err)
			require.Equal(t, "amount\n42\n", string(data))
			require.Equal(t, 1, resolver.calls)
		})
	}
}
