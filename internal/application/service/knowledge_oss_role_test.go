package service

import (
	"context"
	"testing"

	filesvc "github.com/Tencent/WeKnora/internal/application/service/file"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type deniedRoleBackendResolver struct {
	interfaces.StorageBackendResolver
}

func (deniedRoleBackendResolver) ResolveFileService(
	context.Context, *types.Tenant, string, string, string,
) (interfaces.FileService, string, error) {
	return nil, "oss", types.ErrOSSRoleRequiresEnvironment
}

func TestOSSRoleDeniedKnowledgeStorageNeverUsesGlobalFallback(t *testing.T) {
	global := filesvc.NewLocalFileService(t.TempDir(), "")
	tenant := &types.Tenant{ID: 7, StorageEngineConfig: &types.StorageEngineConfig{
		DefaultProvider: "oss", OSS: &types.OSSEngineConfig{AuthType: types.OSSAuthECSRAMRole},
	}}
	ctx := context.WithValue(context.Background(), types.TenantInfoContextKey, tenant)
	kb := &types.KnowledgeBase{ID: "kb"}
	kb.SetStorageProvider("oss")
	for _, resolver := range []interfaces.StorageBackendResolver{nil, deniedRoleBackendResolver{}} {
		svc := &knowledgeService{fileSvc: global, storageResolver: resolver}
		// A mismatched scheme must not bypass a denied resolution either.
		paths := []string{"oss://bucket/7/file", "s3://bucket/7/file", "storage://backend/oss://bucket/7/file"}
		for _, path := range paths {
			resolved := svc.resolveFileServiceForPath(ctx, kb, path)
			require.NotNil(t, resolved)
			_, err := resolved.GetFile(ctx, path)
			require.ErrorIs(t, err, types.ErrOSSRoleRequiresEnvironment)
		}
	}
}
