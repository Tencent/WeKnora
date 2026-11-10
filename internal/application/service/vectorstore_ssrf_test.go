package service

import (
	"context"
	"os"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withSSRFWhitelist sets SSRF_WHITELIST for the duration of a test, resetting
// the cached singleton both before and after so neither this test nor a
// neighbour sees a stale whitelist (see ResetSSRFWhitelistForTest docs).
func withSSRFWhitelist(t *testing.T, whitelist string) {
	t.Helper()
	utils.ResetSSRFWhitelistForTest()
	require.NoError(t, os.Setenv("SSRF_WHITELIST", whitelist))
	t.Cleanup(func() {
		_ = os.Unsetenv("SSRF_WHITELIST")
		utils.ResetSSRFWhitelistForTest()
	})
}

func TestValidateConnectionAddrSSRF(t *testing.T) {
	// Whitelist a benign host so "pass" cases have a deterministic, DNS-free
	// way through. Reject cases use direct private IPs, which are blocked
	// before any DNS lookup, keeping the test non-flaky.
	withSSRFWhitelist(t, "vector.allowed.test")

	tests := []struct {
		name       string
		engineType types.RetrieverEngineType
		config     types.ConnectionConfig
		wantError  bool
	}{
		{
			name:       "elasticsearch private IP blocked",
			engineType: types.ElasticsearchRetrieverEngineType,
			config:     types.ConnectionConfig{Addr: "http://10.0.0.5:9200"},
			wantError:  true,
		},
		{
			name:       "elasticsearch whitelisted host allowed",
			engineType: types.ElasticsearchRetrieverEngineType,
			config:     types.ConnectionConfig{Addr: "http://vector.allowed.test:9200"},
			wantError:  false,
		},
		{
			name:       "opensearch loopback blocked",
			engineType: types.OpenSearchRetrieverEngineType,
			config:     types.ConnectionConfig{Addr: "http://127.0.0.1:9200"},
			wantError:  true,
		},
		{
			name:       "milvus empty addr skipped (presence is validateConnectionConfig's job)",
			engineType: types.MilvusRetrieverEngineType,
			config:     types.ConnectionConfig{},
			wantError:  false,
		},
		{
			name:       "doris private IP blocked",
			engineType: types.DorisRetrieverEngineType,
			config:     types.ConnectionConfig{Addr: "192.168.1.10:9030"},
			wantError:  true,
		},
		{
			name:       "qdrant host+port private IP blocked",
			engineType: types.QdrantRetrieverEngineType,
			config:     types.ConnectionConfig{Host: "10.1.2.3", Port: 6334},
			wantError:  true,
		},
		{
			name:       "qdrant whitelisted host allowed",
			engineType: types.QdrantRetrieverEngineType,
			config:     types.ConnectionConfig{Host: "vector.allowed.test", Port: 6334},
			wantError:  false,
		},
		{
			name:       "weaviate host ok but grpc_address private IP blocked",
			engineType: types.WeaviateRetrieverEngineType,
			config: types.ConnectionConfig{
				Host:        "vector.allowed.test",
				GrpcAddress: "10.0.0.9:50051",
			},
			wantError: true,
		},
		{
			name:       "weaviate both fields whitelisted allowed",
			engineType: types.WeaviateRetrieverEngineType,
			config: types.ConnectionConfig{
				Host:        "vector.allowed.test",
				GrpcAddress: "vector.allowed.test:50051",
			},
			wantError: false,
		},
		{
			name:       "sqlite skipped (no remote address)",
			engineType: types.SQLiteRetrieverEngineType,
			config:     types.ConnectionConfig{},
			wantError:  false,
		},
		{
			name:       "unknown engine fails closed",
			engineType: types.RetrieverEngineType("some-future-engine"),
			config:     types.ConnectionConfig{Addr: "http://vector.allowed.test"},
			wantError:  true,
		},
		{
			name:       "infinity (legacy, unmapped) fails closed",
			engineType: types.InfinityRetrieverEngineType,
			config:     types.ConnectionConfig{Addr: "http://vector.allowed.test"},
			wantError:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateConnectionAddrSSRF(tt.engineType, tt.config)
			if tt.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// TestValidateConnectionAddrSSRF_WhitelistSkipsPortBlock pins the intentional
// behaviour that a whitelisted host bypasses the port blocklist (whitelist
// trust is host-granular). If this ever changes, the bundled-service defaults
// in docker-compose would silently break, so the decision is asserted here.
func TestValidateConnectionAddrSSRF_WhitelistSkipsPortBlock(t *testing.T) {
	withSSRFWhitelist(t, "qdrant")
	// 6379 (redis) is on the blocklist, but a whitelisted host skips all
	// checks including the port block.
	err := validateConnectionAddrSSRF(types.QdrantRetrieverEngineType,
		types.ConnectionConfig{Host: "qdrant", Port: 6379})
	require.NoError(t, err)
}

// TestValidateConnectionAddrSSRF_Completeness guards against a future engine
// being added to validEngineTypes without an SSRF address mapping. Such an
// engine would fall into the fail-closed default branch and error even with a
// whitelisted address — this test catches that at build/test time.
func TestValidateConnectionAddrSSRF_Completeness(t *testing.T) {
	withSSRFWhitelist(t, "any.allowed.test")

	// Derive the engine list from the live registry (GetVectorStoreTypes is
	// backed by validEngineTypes) rather than a hard-coded slice, so a newly
	// added registerable engine that lacks an SSRF address mapping fails this
	// test instead of passing vacuously.
	storeTypes := types.GetVectorStoreTypes()
	require.NotEmpty(t, storeTypes)

	for _, st := range storeTypes {
		et := types.RetrieverEngineType(st.Type)
		if !types.IsValidEngineType(et) {
			continue // env-only / legacy engines are not user-registerable
		}
		// All address fields point at a whitelisted host, so the only way to
		// get an error is the fail-closed default branch (= missing mapping).
		err := validateConnectionAddrSSRF(et, types.ConnectionConfig{
			Addr:        "any.allowed.test",
			Host:        "any.allowed.test",
			GrpcAddress: "any.allowed.test",
		})
		require.NoErrorf(t, err,
			"engine %q is registerable but has no SSRF address mapping (fell into fail-closed default)", et)
	}
}

func TestTestRawConnection_Rejections(t *testing.T) {
	withSSRFWhitelist(t, "vector.allowed.test")
	repo := &mockVectorStoreRepo{}
	svc := NewVectorStoreService(repo, nil, nil, nil, nil)

	tests := []struct {
		name       string
		engineType types.RetrieverEngineType
		config     types.ConnectionConfig
	}{
		{
			// postgres is not a user-registerable vector store; raw-testing it
			// would otherwise dial the app's own DB host (credential oracle).
			name:       "postgres rejected by engine allowlist",
			engineType: types.PostgresRetrieverEngineType,
			config:     types.ConnectionConfig{Addr: "postgres://u:p@vector.allowed.test:5432/db"},
		},
		{
			// empty addr must be rejected by required-field validation before
			// the driver falls back to its localhost:19530 default.
			name:       "milvus empty addr rejected (no localhost fallback)",
			engineType: types.MilvusRetrieverEngineType,
			config:     types.ConnectionConfig{},
		},
		{
			name:       "elasticsearch private IP rejected by SSRF",
			engineType: types.ElasticsearchRetrieverEngineType,
			config:     types.ConnectionConfig{Addr: "http://10.0.0.5:9200"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.TestRawConnection(context.Background(), tt.engineType, tt.config)
			require.Error(t, err)
		})
	}
}

func TestCreateStore_SSRFRejected(t *testing.T) {
	withSSRFWhitelist(t, "vector.allowed.test")
	repo := &mockVectorStoreRepo{}
	svc := NewVectorStoreService(repo, nil, nil, nil, nil)

	store := &types.VectorStore{
		TenantID:   1,
		Name:       "es-internal",
		EngineType: types.ElasticsearchRetrieverEngineType,
		ConnectionConfig: types.ConnectionConfig{
			Addr: "http://169.254.169.254:9200", // cloud metadata endpoint
		},
	}

	err := svc.CreateStore(context.Background(), store)
	require.Error(t, err)
	// Rejected before persistence and before any connection probe.
	assert.Empty(t, repo.stores)
}
