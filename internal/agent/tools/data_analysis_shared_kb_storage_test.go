package tools

import (
	"context"
	"fmt"
	"io"
	"testing"

	filesvc "github.com/Tencent/WeKnora/internal/application/service/file"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

// sharedKBStorageResolver models the tenant-scoped backend lookup: a backend ID
// only resolves for the tenant that owns it, exactly like
// StorageBackendService.ResolveBackend (repo.GetByID(ctx, tenant.ID, backendID)).
type sharedKBStorageResolver struct {
	interfaces.StorageBackendResolver
	ownerTenantID uint64
	backendID     string
	service       interfaces.FileService
	tenants       []*types.Tenant
}

func (r *sharedKBStorageResolver) ResolveFileService(
	_ context.Context, tenant *types.Tenant, backendID, _, _ string,
) (interfaces.FileService, string, error) {
	r.tenants = append(r.tenants, tenant)
	if tenant == nil {
		return nil, "", fmt.Errorf("workspace context missing")
	}
	if tenant.ID != r.ownerTenantID || backendID != r.backendID {
		return nil, "", fmt.Errorf("storage backend not found")
	}
	return r.service, "local", nil
}

type sharedKBTenantService struct {
	interfaces.TenantService
	tenants map[uint64]*types.Tenant
}

func (s *sharedKBTenantService) GetTenantByID(_ context.Context, id uint64) (*types.Tenant, error) {
	tenant, ok := s.tenants[id]
	if !ok {
		return nil, fmt.Errorf("tenant %d not found", id)
	}
	return tenant, nil
}

// A knowledge base shared with another tenant keeps its documents in the
// owner's storage backend. The file service must be resolved with the owner
// tenant: the backend lookup is tenant-scoped, so resolving with the viewer
// would miss the owner's backend and fall back to a service that cannot open
// the file.
func TestDataAnalysisResolvesSharedKnowledgeBaseOwnerStorage(t *testing.T) {
	ctx := context.Background()
	const backendID = "owner-storage"
	ownerTenantID := uint64(1)
	viewerTenantID := uint64(2)

	ownerFiles := filesvc.NewLocalFileService(t.TempDir(), "")
	stored, err := ownerFiles.SaveBytes(ctx, []byte("amount\n42\n"), ownerTenantID, "data.csv", false)
	require.NoError(t, err)
	filePath := "storage://" + backendID + "/" + stored

	resolver := &sharedKBStorageResolver{
		ownerTenantID: ownerTenantID,
		backendID:     backendID,
		service:       filesvc.NewBackendScopedFileService(backendID, ownerFiles),
	}
	ownerBackendID := backendID
	tool := NewDataAnalysisTool(
		&stubKnowledgeBaseService{kb: &types.KnowledgeBase{
			ID: "kb", TenantID: ownerTenantID, StorageBackendID: &ownerBackendID,
		}},
		nil,
		&sharedKBTenantService{tenants: map[uint64]*types.Tenant{
			ownerTenantID: {ID: ownerTenantID, DefaultStorageBackendID: &ownerBackendID},
		}},
		// The viewer's default service cannot open a path from the owner's
		// backend, so the fallback is observable.
		filesvc.NewLocalFileService(t.TempDir(), ""),
		nil,
		"shared-kb-storage-test",
		resolver,
	)
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, &types.Tenant{ID: viewerTenantID})
	knowledge := &types.Knowledge{
		ID: "document", KnowledgeBaseID: "kb", TenantID: ownerTenantID, FilePath: filePath,
	}

	reader, err := tool.resolveFileServiceForKnowledge(ctx, knowledge).GetFile(ctx, filePath)
	require.NoError(t, err)
	defer func() { _ = reader.Close() }()
	data, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Equal(t, "amount\n42\n", string(data))
	require.Len(t, resolver.tenants, 1)
	require.NotNil(t, resolver.tenants[0])
	require.Equal(t, ownerTenantID, resolver.tenants[0].ID)
}

// Requests inside the owning tenant must keep resolving with the request
// tenant; the owner lookup exists only for shared knowledge bases.
func TestDataAnalysisKeepsRequestTenantForOwnedKnowledge(t *testing.T) {
	ctx := context.Background()
	const backendID = "owned-storage"
	tenantID := uint64(3)

	files := filesvc.NewLocalFileService(t.TempDir(), "")
	stored, err := files.SaveBytes(ctx, []byte("amount\n42\n"), tenantID, "data.csv", false)
	require.NoError(t, err)
	filePath := "storage://" + backendID + "/" + stored

	resolver := &sharedKBStorageResolver{
		ownerTenantID: tenantID,
		backendID:     backendID,
		service:       filesvc.NewBackendScopedFileService(backendID, files),
	}
	backend := backendID
	tool := NewDataAnalysisTool(
		&stubKnowledgeBaseService{kb: &types.KnowledgeBase{
			ID: "kb", TenantID: tenantID, StorageBackendID: &backend,
		}},
		nil,
		// A lookup here would be a regression: the owning tenant is already
		// the request tenant.
		&sharedKBTenantService{},
		filesvc.NewLocalFileService(t.TempDir(), ""),
		nil,
		"owned-storage-test",
		resolver,
	)
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, &types.Tenant{ID: tenantID})
	knowledge := &types.Knowledge{ID: "document", KnowledgeBaseID: "kb", TenantID: tenantID, FilePath: filePath}

	reader, err := tool.resolveFileServiceForKnowledge(ctx, knowledge).GetFile(ctx, filePath)
	require.NoError(t, err)
	defer func() { _ = reader.Close() }()
	require.Len(t, resolver.tenants, 1)
	require.NotNil(t, resolver.tenants[0])
	require.Equal(t, tenantID, resolver.tenants[0].ID)
}
