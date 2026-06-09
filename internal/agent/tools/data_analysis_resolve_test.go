package tools

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// When no storage config can be obtained from the KB / tenant (e.g. a deployment
// that switched to a global/built-in storage engine without per-tenant config),
// resolveFileServiceForKnowledge must NOT short-circuit to the default
// fileService: the default service's provider may not match the file's actual
// backend and would open an object-storage path (s3://…) as a local path. It
// must resolve the backend from the FilePath's provider scheme and fill in config
// via NewFileServiceFromStorageConfig → built-in storage engine singleton.
func TestDataAnalysisResolveFileService_NoTenantConfig_HonorsFilePathScheme(t *testing.T) {
	types.SetBuiltinStorageEngineForTest(&types.StorageEngineConfig{
		DefaultProvider: "local",
		Local:           &types.LocalEngineConfig{PathPrefix: "builtin"},
	})
	t.Cleanup(types.ResetBuiltinStorageEngine)

	sentinel := &fakeFileService{} // recognizable default fallback service
	tool := &DataAnalysisTool{fileService: sentinel, sessionID: "test-resolve"}

	k := &types.Knowledge{ID: "k1", FilePath: "local://10000/kb/data.xlsx"}
	got := tool.resolveFileServiceForKnowledge(context.Background(), k)
	if got == sentinel {
		t.Fatalf("must not short-circuit to the default service when there is no tenant storage config; should resolve a provider-specific file service from the FilePath scheme")
	}
}

// When the FilePath has no provider scheme and no default provider is available,
// falling back to the default service is correct (preserves existing behavior).
func TestDataAnalysisResolveFileService_NoSchemeNoProvider_FallsBack(t *testing.T) {
	types.ResetBuiltinStorageEngine()
	t.Cleanup(types.ResetBuiltinStorageEngine)

	sentinel := &fakeFileService{}
	tool := &DataAnalysisTool{fileService: sentinel, sessionID: "test-resolve-fallback"}

	k := &types.Knowledge{ID: "k2", FilePath: "tenants/42/data.csv"}
	got := tool.resolveFileServiceForKnowledge(context.Background(), k)
	if got != sentinel {
		t.Fatalf("should fall back to the default fileService when the storage backend cannot be determined")
	}
}
