package service

import (
	"context"
	"testing"

	filesvc "github.com/Tencent/WeKnora/internal/application/service/file"
	"github.com/Tencent/WeKnora/internal/types"
)

// When the tenant has no StorageEngineConfig (common for deployments that use a
// global/built-in storage engine without per-tenant config),
// resolveFileServiceForKnowledge must NOT short-circuit to the default
// fileService: the default service's provider may not match the file's actual
// backend and would open an object-storage path (s3://…) as a local path. It
// must resolve the backend from the FilePath's provider scheme and fill in
// config via NewFileServiceFromStorageConfig → built-in storage engine singleton.
func TestResolveFileServiceForKnowledge_TenantConfigNil_HonorsFilePathScheme(t *testing.T) {
	// The built-in storage engine singleton provides the local provider config,
	// simulating the "built-in storage engine".
	types.SetBuiltinStorageEngineForTest(&types.StorageEngineConfig{
		DefaultProvider: "local",
		Local:           &types.LocalEngineConfig{PathPrefix: "builtin"},
	})
	t.Cleanup(types.ResetBuiltinStorageEngine)

	sentinel := filesvc.NewDummyFileService() // recognizable default fallback service
	s := &DataTableSummaryService{fileService: sentinel}

	resources := &extractionResources{
		knowledge: &types.Knowledge{
			ID:       "k1",
			FilePath: "local://10000/kb/data.xlsx",
		},
		tenant: &types.Tenant{StorageEngineConfig: nil}, // key: tenant has no storage config
	}

	got := s.resolveFileServiceForKnowledge(context.Background(), resources)
	if got == sentinel {
		t.Fatalf("must not short-circuit to the default service when the tenant storage config is nil; should resolve a provider-specific file service from the FilePath scheme")
	}
}

// When the FilePath has no provider scheme and no default provider is available,
// falling back to the default service is the correct behavior.
func TestResolveFileServiceForKnowledge_NoSchemeNoProvider_FallsBackToDefault(t *testing.T) {
	types.ResetBuiltinStorageEngine() // no built-in config, no default provider
	t.Cleanup(types.ResetBuiltinStorageEngine)

	sentinel := filesvc.NewDummyFileService()
	s := &DataTableSummaryService{fileService: sentinel}

	resources := &extractionResources{
		knowledge: &types.Knowledge{
			ID:       "k2",
			FilePath: "/legacy/abs/path/data.xlsx", // no provider scheme
		},
		tenant: &types.Tenant{StorageEngineConfig: nil},
	}

	got := s.resolveFileServiceForKnowledge(context.Background(), resources)
	if got != sentinel {
		t.Fatalf("should fall back to the default fileService when the storage backend cannot be determined")
	}
}
