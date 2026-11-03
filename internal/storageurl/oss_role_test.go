package storageurl

import (
	"context"
	"testing"

	filesvc "github.com/Tencent/WeKnora/internal/application/service/file"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type deniedOSSRoleResolver struct {
	interfaces.StorageBackendResolver
}

func (deniedOSSRoleResolver) ResolveFileService(
	context.Context, *types.Tenant, string, string, string,
) (interfaces.FileService, string, error) {
	return nil, "oss", types.ErrOSSRoleRequiresEnvironment
}

func TestOSSRoleDeniedResolutionCannotUseGlobalIdentity(t *testing.T) {
	global := filesvc.NewLocalFileService(t.TempDir(), "")
	tenant := &types.Tenant{ID: 7, StorageEngineConfig: &types.StorageEngineConfig{
		DefaultProvider: "oss", OSS: &types.OSSEngineConfig{AuthType: types.OSSAuthECSRAMRole},
	}}
	for _, resolved := range []interfaces.FileService{
		BuildFileServiceForProvider(tenant, "oss", global),
		NewFileServiceResolver(tenant, global, deniedOSSRoleResolver{}).ResolveFileService("oss://bucket/7/a.txt"),
	} {
		require.NotNil(t, resolved)
		_, err := resolved.SaveBytes(context.Background(), []byte("forbidden"), 7, "a.txt", false)
		require.ErrorIs(t, err, types.ErrOSSRoleRequiresEnvironment)
	}
}
