package container

import (
	"os"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
)

func TestMain(m *testing.M) {
	// Engine wiring tests intentionally use loopback httptest servers.
	utils.SetSSRFWhitelistFromRaw("127.0.0.1,::1,localhost")
	code := m.Run()
	utils.SetSSRFWhitelistFromRaw("")
	os.Exit(code)
}

func TestValidateRuntimeVectorStoreAddressesRejectsUnsafeEndpoints(t *testing.T) {
	tests := []types.VectorStore{
		{EngineType: types.QdrantRetrieverEngineType, ConnectionConfig: types.ConnectionConfig{Host: "169.254.169.254", Port: 6334}},
		{EngineType: types.MilvusRetrieverEngineType, ConnectionConfig: types.ConnectionConfig{Addr: "169.254.169.254:19530"}},
		{EngineType: types.WeaviateRetrieverEngineType, ConnectionConfig: types.ConnectionConfig{Host: "https://example.com", GrpcAddress: "169.254.169.254:50051"}},
		{EngineType: types.DorisRetrieverEngineType, ConnectionConfig: types.ConnectionConfig{Addr: "169.254.169.254:9030"}},
		{EngineType: types.TencentVectorDBRetrieverEngineType, ConnectionConfig: types.ConnectionConfig{Addr: "169.254.169.254:80"}},
	}
	for _, store := range tests {
		if err := validateRuntimeVectorStoreAddresses(store); err == nil {
			t.Fatalf("expected %s unsafe address to be rejected", store.EngineType)
		}
	}
}

func TestValidateRuntimeVectorStoreAddressesAllowsNonNetworkEngines(t *testing.T) {
	for _, engineType := range []types.RetrieverEngineType{
		types.PostgresRetrieverEngineType,
		types.SQLiteRetrieverEngineType,
	} {
		if err := validateRuntimeVectorStoreAddresses(types.VectorStore{EngineType: engineType}); err != nil {
			t.Fatalf("unexpected %s validation error: %v", engineType, err)
		}
	}
}
