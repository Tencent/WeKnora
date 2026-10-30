package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type pathRoutingFileService struct {
	interfaces.FileService
	provider string
}

type pathRoutingStorageResolver struct {
	local, s3 interfaces.FileService
	providers []string
}

func (r *pathRoutingStorageResolver) ResolveFileService(_ context.Context, _ *types.Tenant, _, provider, _ string) (interfaces.FileService, string, error) {
	r.providers = append(r.providers, provider)
	if provider == "s3" {
		return r.s3, provider, nil
	}
	return r.local, provider, nil
}

func (*pathRoutingStorageResolver) ResolveBackend(context.Context, *types.Tenant, string, string) (*types.StorageBackend, error) {
	return nil, nil
}

func TestResolveFileServiceForPathUsesPathProviderOnConfigMismatch(t *testing.T) {
	localSvc := &pathRoutingFileService{provider: "local"}
	s3Svc := &pathRoutingFileService{provider: "s3"}
	resolver := &pathRoutingStorageResolver{local: localSvc, s3: s3Svc}
	tenant := &types.Tenant{StorageEngineConfig: &types.StorageEngineConfig{DefaultProvider: "local"}}
	ctx := context.WithValue(context.Background(), types.TenantInfoContextKey, tenant)
	kb := &types.KnowledgeBase{StorageProviderConfig: &types.StorageProviderConfig{Provider: "local"}}
	svc := &knowledgeService{fileSvc: localSvc, storageResolver: resolver}

	for _, filePath := range []string{
		"s3://my-bucket/document.pdf",
		"s3:/my-bucket/document.pdf",
	} {
		got := svc.resolveFileServiceForPath(ctx, kb, filePath)
		require.Same(t, s3Svc, got, "file path %q should select the inferred provider", filePath)
	}
	require.Equal(t, []string{"local", "s3", "local", "s3"}, resolver.providers)
}
