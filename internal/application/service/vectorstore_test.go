package service

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Mock repository
// ---------------------------------------------------------------------------

type mockVectorStoreRepo struct {
	stores              []*types.VectorStore
	createErr           error
	updateErr           error
	deleteErr           error
	existsByEndpointErr error
	existsByEndpoint    bool
}

func (m *mockVectorStoreRepo) Create(_ context.Context, store *types.VectorStore) error {
	if m.createErr != nil {
		return m.createErr
	}
	m.stores = append(m.stores, store)
	return nil
}

func (m *mockVectorStoreRepo) GetByID(_ context.Context, tenantID uint64, id string) (*types.VectorStore, error) {
	for _, s := range m.stores {
		if s.ID == id && s.TenantID == tenantID {
			return s, nil
		}
	}
	return nil, nil
}

func (m *mockVectorStoreRepo) List(_ context.Context, tenantID uint64) ([]*types.VectorStore, error) {
	var result []*types.VectorStore
	for _, s := range m.stores {
		if s.TenantID == tenantID {
			result = append(result, s)
		}
	}
	return result, nil
}

func (m *mockVectorStoreRepo) Update(_ context.Context, store *types.VectorStore) error {
	return m.updateErr
}

func (m *mockVectorStoreRepo) UpdateConnectionConfig(_ context.Context, _ *types.VectorStore) error {
	return m.updateErr
}

func (m *mockVectorStoreRepo) Delete(_ context.Context, _ uint64, _ string) error {
	return m.deleteErr
}

func (m *mockVectorStoreRepo) ExistsByEndpointAndIndex(
	_ context.Context, _ uint64, _ types.RetrieverEngineType, _ string, _ string,
) (bool, error) {
	if m.existsByEndpointErr != nil {
		return false, m.existsByEndpointErr
	}
	return m.existsByEndpoint, nil
}

// ---------------------------------------------------------------------------
// Mock StoreRegistry
// ---------------------------------------------------------------------------

type mockStoreRegistry struct {
	registered   map[string]bool
	unregistered []string
}

func newMockStoreRegistry() *mockStoreRegistry {
	return &mockStoreRegistry{registered: make(map[string]bool)}
}

func (m *mockStoreRegistry) RegisterWithStoreID(storeID string, _ interfaces.RetrieveEngineService) {
	m.registered[storeID] = true
}

func (m *mockStoreRegistry) GetByStoreID(storeID string) (interfaces.RetrieveEngineService, error) {
	return nil, nil
}

func (m *mockStoreRegistry) UnregisterByStoreID(storeID string) {
	m.unregistered = append(m.unregistered, storeID)
	delete(m.registered, storeID)
}

// ---------------------------------------------------------------------------
// Mock EngineFactory
// ---------------------------------------------------------------------------

func mockEngineFactory(err error) interfaces.EngineFactory {
	return func(_ context.Context, _ types.VectorStore) (interfaces.RetrieveEngineService, error) {
		if err != nil {
			return nil, err
		}
		return &mockEngineService{}, nil
	}
}

// mockEngineService satisfies interfaces.RetrieveEngineService minimally.
type mockEngineService struct{}

func (m *mockEngineService) EngineType() types.RetrieverEngineType                    { return "mock" }
func (m *mockEngineService) Retrieve(_ context.Context, _ types.RetrieveParams) ([]*types.RetrieveResult, error) {
	return nil, nil
}
func (m *mockEngineService) Support() []types.RetrieverType { return nil }
func (m *mockEngineService) Index(_ context.Context, _ embedding.Embedder, _ *types.IndexInfo, _ []types.RetrieverType) error {
	return nil
}
func (m *mockEngineService) BatchIndex(_ context.Context, _ embedding.Embedder, _ []*types.IndexInfo, _ []types.RetrieverType) error {
	return nil
}
func (m *mockEngineService) EstimateStorageSize(_ context.Context, _ embedding.Embedder, _ []*types.IndexInfo, _ []types.RetrieverType) int64 {
	return 0
}
func (m *mockEngineService) CopyIndices(_ context.Context, _ string, _ map[string]string, _ map[string]string, _ string, _ int, _ string) error {
	return nil
}
func (m *mockEngineService) DeleteByChunkIDList(_ context.Context, _ []string, _ int, _ string) error {
	return nil
}
func (m *mockEngineService) DeleteBySourceIDList(_ context.Context, _ []string, _ int, _ string) error {
	return nil
}
func (m *mockEngineService) DeleteByKnowledgeIDList(_ context.Context, _ []string, _ int, _ string) error {
	return nil
}
func (m *mockEngineService) BatchUpdateChunkEnabledStatus(_ context.Context, _ map[string]bool) error {
	return nil
}
func (m *mockEngineService) BatchUpdateChunkTagID(_ context.Context, _ map[string]string) error {
	return nil
}

// ---------------------------------------------------------------------------
// CreateStore tests
// ---------------------------------------------------------------------------

func TestCreateStore_Success(t *testing.T) {
	repo := &mockVectorStoreRepo{}
	svc := NewVectorStoreService(repo, nil, nil)

	store := &types.VectorStore{
		TenantID:   1,
		Name:       "test-es",
		EngineType: types.ElasticsearchRetrieverEngineType,
		ConnectionConfig: types.ConnectionConfig{
			Addr: "http://es:9200",
		},
	}

	err := svc.CreateStore(context.Background(), store)
	assert.NoError(t, err)
	assert.Len(t, repo.stores, 1)
}

func TestCreateStore_ValidationError(t *testing.T) {
	repo := &mockVectorStoreRepo{}
	svc := NewVectorStoreService(repo, nil, nil)

	tests := []struct {
		name  string
		store *types.VectorStore
	}{
		{
			name:  "empty name",
			store: &types.VectorStore{TenantID: 1, EngineType: types.PostgresRetrieverEngineType},
		},
		{
			name:  "invalid engine type",
			store: &types.VectorStore{TenantID: 1, Name: "test", EngineType: "unknown"},
		},
		{
			name:  "zero tenant ID",
			store: &types.VectorStore{Name: "test", EngineType: types.PostgresRetrieverEngineType},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := svc.CreateStore(context.Background(), tt.store)
			require.Error(t, err)
			var appErr *errors.AppError
			assert.ErrorAs(t, err, &appErr)
		})
	}
}

func TestCreateStore_ConnectionConfigValidation(t *testing.T) {
	repo := &mockVectorStoreRepo{}
	svc := NewVectorStoreService(repo, nil, nil)

	tests := []struct {
		name      string
		store     *types.VectorStore
		wantError bool
	}{
		{
			name: "elasticsearch without addr",
			store: &types.VectorStore{
				TenantID: 1, Name: "test",
				EngineType:       types.ElasticsearchRetrieverEngineType,
				ConnectionConfig: types.ConnectionConfig{},
			},
			wantError: true,
		},
		{
			name: "postgres without addr or default connection",
			store: &types.VectorStore{
				TenantID: 1, Name: "test",
				EngineType:       types.PostgresRetrieverEngineType,
				ConnectionConfig: types.ConnectionConfig{},
			},
			wantError: true,
		},
		{
			name: "postgres with use_default_connection",
			store: &types.VectorStore{
				TenantID: 1, Name: "test",
				EngineType:       types.PostgresRetrieverEngineType,
				ConnectionConfig: types.ConnectionConfig{UseDefaultConnection: true},
			},
			wantError: false,
		},
		{
			name: "qdrant without host",
			store: &types.VectorStore{
				TenantID: 1, Name: "test",
				EngineType:       types.QdrantRetrieverEngineType,
				ConnectionConfig: types.ConnectionConfig{},
			},
			wantError: true,
		},
		{
			name: "milvus without addr",
			store: &types.VectorStore{
				TenantID: 1, Name: "test",
				EngineType:       types.MilvusRetrieverEngineType,
				ConnectionConfig: types.ConnectionConfig{},
			},
			wantError: true,
		},
		{
			name: "weaviate without host",
			store: &types.VectorStore{
				TenantID: 1, Name: "test",
				EngineType:       types.WeaviateRetrieverEngineType,
				ConnectionConfig: types.ConnectionConfig{},
			},
			wantError: true,
		},
		{
			name: "sqlite with no config (ok)",
			store: &types.VectorStore{
				TenantID: 1, Name: "test",
				EngineType:       types.SQLiteRetrieverEngineType,
				ConnectionConfig: types.ConnectionConfig{},
			},
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := svc.CreateStore(context.Background(), tt.store)
			if tt.wantError {
				require.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestCreateStore_DuplicateCheck_DBStore(t *testing.T) {
	repo := &mockVectorStoreRepo{existsByEndpoint: true}
	svc := NewVectorStoreService(repo, nil, nil)

	store := &types.VectorStore{
		TenantID:   1,
		Name:       "dup-store",
		EngineType: types.ElasticsearchRetrieverEngineType,
		ConnectionConfig: types.ConnectionConfig{
			Addr: "http://es:9200",
		},
	}

	err := svc.CreateStore(context.Background(), store)
	require.Error(t, err)

	var appErr *errors.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, errors.ErrConflict, appErr.Code)
}

func TestCreateStore_DuplicateCheck_DBError(t *testing.T) {
	repo := &mockVectorStoreRepo{
		existsByEndpointErr: assert.AnError,
	}
	svc := NewVectorStoreService(repo, nil, nil)

	store := &types.VectorStore{
		TenantID:   1,
		Name:       "test",
		EngineType: types.ElasticsearchRetrieverEngineType,
		ConnectionConfig: types.ConnectionConfig{
			Addr: "http://es:9200",
		},
	}

	err := svc.CreateStore(context.Background(), store)
	require.Error(t, err)
}

func TestCreateStore_DuplicateCheck_EnvStore(t *testing.T) {
	// Set up env to simulate an existing elasticsearch env store
	t.Setenv("RETRIEVE_DRIVER", "elasticsearch_v8")
	t.Setenv("ELASTICSEARCH_ADDR", "http://es:9200")
	t.Setenv("ELASTICSEARCH_USERNAME", "elastic")
	t.Setenv("ELASTICSEARCH_PASSWORD", "secret")
	t.Setenv("ELASTICSEARCH_INDEX", "xwrag_default")

	repo := &mockVectorStoreRepo{existsByEndpoint: false} // no DB duplicate
	svc := NewVectorStoreService(repo, nil, nil)

	store := &types.VectorStore{
		TenantID:   1,
		Name:       "dup-env-store",
		EngineType: types.ElasticsearchRetrieverEngineType,
		ConnectionConfig: types.ConnectionConfig{
			Addr: "http://es:9200",
		},
		IndexConfig: types.IndexConfig{
			IndexName: "xwrag_default",
		},
	}

	err := svc.CreateStore(context.Background(), store)
	require.Error(t, err)

	var appErr *errors.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, errors.ErrConflict, appErr.Code)
	assert.Contains(t, appErr.Error(), "environment variables")
}

func TestCreateStore_DuplicateCheck_EnvStore_DifferentIndex_Allowed(t *testing.T) {
	// Same endpoint as env store but different index — should be allowed
	t.Setenv("RETRIEVE_DRIVER", "elasticsearch_v8")
	t.Setenv("ELASTICSEARCH_ADDR", "http://es:9200")
	t.Setenv("ELASTICSEARCH_INDEX", "xwrag_default")

	repo := &mockVectorStoreRepo{existsByEndpoint: false}
	svc := NewVectorStoreService(repo, nil, nil)

	store := &types.VectorStore{
		TenantID:   1,
		Name:       "different-index",
		EngineType: types.ElasticsearchRetrieverEngineType,
		ConnectionConfig: types.ConnectionConfig{
			Addr: "http://es:9200",
		},
		IndexConfig: types.IndexConfig{
			IndexName: "different_index",
		},
	}

	err := svc.CreateStore(context.Background(), store)
	assert.NoError(t, err)
}

func TestCreateStore_DifferentEndpointSameIndex_Allowed(t *testing.T) {
	repo := &mockVectorStoreRepo{existsByEndpoint: false}
	svc := NewVectorStoreService(repo, nil, nil)

	// Use postgres with UseDefaultConnection to avoid needing a real ES endpoint.
	// The test verifies duplicate-check logic, not connectivity.
	store := &types.VectorStore{
		TenantID:   1,
		Name:       "new-store",
		EngineType: types.PostgresRetrieverEngineType,
		ConnectionConfig: types.ConnectionConfig{
			UseDefaultConnection: true,
		},
	}

	err := svc.CreateStore(context.Background(), store)
	assert.NoError(t, err)
}

// ---------------------------------------------------------------------------
// CreateStore + Registry integration tests
// ---------------------------------------------------------------------------

func TestCreateStore_RegistersInRegistry(t *testing.T) {
	repo := &mockVectorStoreRepo{}
	registry := newMockStoreRegistry()
	factory := mockEngineFactory(nil)
	svc := NewVectorStoreService(repo, registry, factory)

	store := &types.VectorStore{
		TenantID:   1,
		Name:       "test-es",
		EngineType: types.ElasticsearchRetrieverEngineType,
		ConnectionConfig: types.ConnectionConfig{
			Addr: "http://es:9200",
		},
	}

	err := svc.CreateStore(context.Background(), store)
	require.NoError(t, err)

	// Store should be persisted AND registered in registry
	assert.Len(t, repo.stores, 1)
	assert.True(t, registry.registered[store.ID])
}

func TestCreateStore_RegistryFailureDoesNotRollBackDB(t *testing.T) {
	repo := &mockVectorStoreRepo{}
	registry := newMockStoreRegistry()
	factory := mockEngineFactory(assert.AnError) // factory fails
	svc := NewVectorStoreService(repo, registry, factory)

	store := &types.VectorStore{
		TenantID:   1,
		Name:       "test-es",
		EngineType: types.ElasticsearchRetrieverEngineType,
		ConnectionConfig: types.ConnectionConfig{
			Addr: "http://es:9200",
		},
	}

	// CreateStore should succeed even if registry fails (best-effort + self-healing)
	err := svc.CreateStore(context.Background(), store)
	assert.NoError(t, err)

	// DB should have the store
	assert.Len(t, repo.stores, 1)
	// Registry should NOT have it (factory failed)
	assert.False(t, registry.registered[store.ID])
}

func TestCreateStore_NilRegistryAndFactory(t *testing.T) {
	repo := &mockVectorStoreRepo{}
	svc := NewVectorStoreService(repo, nil, nil) // no registry

	store := &types.VectorStore{
		TenantID:   1,
		Name:       "test-es",
		EngineType: types.ElasticsearchRetrieverEngineType,
		ConnectionConfig: types.ConnectionConfig{
			Addr: "http://es:9200",
		},
	}

	// Should work fine without registry (degrades gracefully)
	err := svc.CreateStore(context.Background(), store)
	assert.NoError(t, err)
	assert.Len(t, repo.stores, 1)
}

// ---------------------------------------------------------------------------
// DeleteStore + Registry integration tests
// ---------------------------------------------------------------------------

func TestDeleteStore_UnregistersFromRegistry(t *testing.T) {
	repo := &mockVectorStoreRepo{}
	registry := newMockStoreRegistry()
	registry.registered["store-1"] = true
	svc := NewVectorStoreService(repo, registry, nil)

	err := svc.DeleteStore(context.Background(), 1, "store-1")
	require.NoError(t, err)

	// Should be unregistered
	assert.Contains(t, registry.unregistered, "store-1")
	assert.False(t, registry.registered["store-1"])
}

func TestDeleteStore_NilRegistryGraceful(t *testing.T) {
	repo := &mockVectorStoreRepo{}
	svc := NewVectorStoreService(repo, nil, nil)

	// Should not panic with nil registry
	err := svc.DeleteStore(context.Background(), 1, "store-1")
	assert.NoError(t, err)
}

func TestDeleteStore_RepoErrorSkipsUnregister(t *testing.T) {
	repo := &mockVectorStoreRepo{deleteErr: assert.AnError}
	registry := newMockStoreRegistry()
	registry.registered["store-1"] = true
	svc := NewVectorStoreService(repo, registry, nil)

	err := svc.DeleteStore(context.Background(), 1, "store-1")
	assert.Error(t, err)

	// Registry should NOT be touched if DB delete fails
	assert.True(t, registry.registered["store-1"])
	assert.Empty(t, registry.unregistered)
}

// ---------------------------------------------------------------------------
// UpdateStore tests
// ---------------------------------------------------------------------------

func TestUpdateStore_Success(t *testing.T) {
	repo := &mockVectorStoreRepo{}
	svc := NewVectorStoreService(repo, nil, nil)

	store := &types.VectorStore{
		ID:       "test-id",
		TenantID: 1,
		Name:     "updated-name",
	}

	err := svc.UpdateStore(context.Background(), store)
	assert.NoError(t, err)
}

func TestUpdateStore_ValidationError(t *testing.T) {
	repo := &mockVectorStoreRepo{}
	svc := NewVectorStoreService(repo, nil, nil)

	tests := []struct {
		name  string
		store *types.VectorStore
	}{
		{
			name:  "empty name",
			store: &types.VectorStore{ID: "id", TenantID: 1, Name: ""},
		},
		{
			name:  "zero tenant ID",
			store: &types.VectorStore{ID: "id", TenantID: 0, Name: "test"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := svc.UpdateStore(context.Background(), tt.store)
			require.Error(t, err)
		})
	}
}

// ---------------------------------------------------------------------------
// DeleteStore tests
// ---------------------------------------------------------------------------

func TestDeleteStore_Success(t *testing.T) {
	repo := &mockVectorStoreRepo{}
	svc := NewVectorStoreService(repo, nil, nil)

	err := svc.DeleteStore(context.Background(), 1, "test-id")
	assert.NoError(t, err)
}

func TestDeleteStore_RepoError(t *testing.T) {
	repo := &mockVectorStoreRepo{deleteErr: assert.AnError}
	svc := NewVectorStoreService(repo, nil, nil)

	err := svc.DeleteStore(context.Background(), 1, "test-id")
	assert.Error(t, err)
}

// ---------------------------------------------------------------------------
// SaveDetectedVersion tests
// ---------------------------------------------------------------------------

func TestSaveDetectedVersion_Success(t *testing.T) {
	repo := &mockVectorStoreRepo{}
	svc := NewVectorStoreService(repo, nil, nil)

	store := &types.VectorStore{
		ID:               "store-1",
		TenantID:         1,
		ConnectionConfig: types.ConnectionConfig{Addr: "http://es:9200"},
	}

	err := svc.SaveDetectedVersion(context.Background(), store, "7.10.1")
	assert.NoError(t, err)
}

func TestSaveDetectedVersion_RepoError(t *testing.T) {
	repo := &mockVectorStoreRepo{updateErr: assert.AnError}
	svc := NewVectorStoreService(repo, nil, nil)

	store := &types.VectorStore{ID: "store-1", TenantID: 1}
	err := svc.SaveDetectedVersion(context.Background(), store, "8.11.0")
	assert.Error(t, err)
}

func TestSaveDetectedVersion_DoesNotMutateOriginal(t *testing.T) {
	repo := &mockVectorStoreRepo{}
	svc := NewVectorStoreService(repo, nil, nil)

	store := &types.VectorStore{
		ID:               "store-1",
		TenantID:         1,
		ConnectionConfig: types.ConnectionConfig{Version: "old"},
	}

	err := svc.SaveDetectedVersion(context.Background(), store, "new")
	require.NoError(t, err)

	// Original store must not be mutated
	assert.Equal(t, "old", store.ConnectionConfig.Version)
}

// ---------------------------------------------------------------------------
// TestConnection tests
// ---------------------------------------------------------------------------

func TestTestConnection_UnsupportedEngineType(t *testing.T) {
	repo := &mockVectorStoreRepo{}
	svc := NewVectorStoreService(repo, nil, nil)

	_, err := svc.TestConnection(context.Background(), "unknown_engine", types.ConnectionConfig{})
	require.Error(t, err)

	var appErr *errors.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, errors.ErrBadRequest, appErr.Code)
}

func TestTestConnection_SQLiteAlwaysSucceeds(t *testing.T) {
	repo := &mockVectorStoreRepo{}
	svc := NewVectorStoreService(repo, nil, nil)

	version, err := svc.TestConnection(context.Background(), types.SQLiteRetrieverEngineType, types.ConnectionConfig{})
	assert.NoError(t, err)
	assert.Empty(t, version)
}

func TestTestConnection_PostgresDefaultConnection(t *testing.T) {
	repo := &mockVectorStoreRepo{}
	svc := NewVectorStoreService(repo, nil, nil)

	version, err := svc.TestConnection(context.Background(), types.PostgresRetrieverEngineType,
		types.ConnectionConfig{UseDefaultConnection: true})
	assert.NoError(t, err)
	assert.Empty(t, version) // default connection cannot detect version without DB handle
}

func TestTestConnection_DorisInvalidAddr(t *testing.T) {
	// 给一个不可达的地址 + 5s timeout，期望返回 BadRequestError 而非 panic。
	repo := &mockVectorStoreRepo{}
	svc := NewVectorStoreService(repo, nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	_, err := svc.TestConnection(ctx, types.DorisRetrieverEngineType, types.ConnectionConfig{
		Addr:     "127.0.0.1:1", // 一定不可连通
		Database: "weknora",
		Username: "root",
	})
	require.Error(t, err)
}

func TestTestConnection_DorisMissingAddr(t *testing.T) {
	repo := &mockVectorStoreRepo{}
	svc := NewVectorStoreService(repo, nil, nil)

	_, err := svc.TestConnection(context.Background(), types.DorisRetrieverEngineType,
		types.ConnectionConfig{})
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// validateConnectionConfig tests
// ---------------------------------------------------------------------------

func TestValidateConnectionConfig(t *testing.T) {
	tests := []struct {
		name       string
		engineType types.RetrieverEngineType
		config     types.ConnectionConfig
		wantError  bool
	}{
		{
			name:       "elasticsearch valid",
			engineType: types.ElasticsearchRetrieverEngineType,
			config:     types.ConnectionConfig{Addr: "http://es:9200"},
			wantError:  false,
		},
		{
			name:       "elasticsearch missing addr",
			engineType: types.ElasticsearchRetrieverEngineType,
			config:     types.ConnectionConfig{},
			wantError:  true,
		},
		{
			name:       "postgres with default connection",
			engineType: types.PostgresRetrieverEngineType,
			config:     types.ConnectionConfig{UseDefaultConnection: true},
			wantError:  false,
		},
		{
			name:       "postgres with addr",
			engineType: types.PostgresRetrieverEngineType,
			config:     types.ConnectionConfig{Addr: "postgres://host:5432/db"},
			wantError:  false,
		},
		{
			name:       "postgres without addr or default",
			engineType: types.PostgresRetrieverEngineType,
			config:     types.ConnectionConfig{},
			wantError:  true,
		},
		{
			name:       "qdrant valid",
			engineType: types.QdrantRetrieverEngineType,
			config:     types.ConnectionConfig{Host: "qdrant-host"},
			wantError:  false,
		},
		{
			name:       "qdrant missing host",
			engineType: types.QdrantRetrieverEngineType,
			config:     types.ConnectionConfig{},
			wantError:  true,
		},
		{
			name:       "milvus valid",
			engineType: types.MilvusRetrieverEngineType,
			config:     types.ConnectionConfig{Addr: "milvus:19530"},
			wantError:  false,
		},
		{
			name:       "milvus missing addr",
			engineType: types.MilvusRetrieverEngineType,
			config:     types.ConnectionConfig{},
			wantError:  true,
		},
		{
			name:       "weaviate valid",
			engineType: types.WeaviateRetrieverEngineType,
			config:     types.ConnectionConfig{Host: "weaviate:8080"},
			wantError:  false,
		},
		{
			name:       "weaviate missing host",
			engineType: types.WeaviateRetrieverEngineType,
			config:     types.ConnectionConfig{},
			wantError:  true,
		},
		{
			name:       "sqlite always valid",
			engineType: types.SQLiteRetrieverEngineType,
			config:     types.ConnectionConfig{},
			wantError:  false,
		},
		{
			name:       "doris valid",
			engineType: types.DorisRetrieverEngineType,
			config:     types.ConnectionConfig{Addr: "doris-fe:9030", Database: "weknora"},
			wantError:  false,
		},
		{
			name:       "doris missing addr",
			engineType: types.DorisRetrieverEngineType,
			config:     types.ConnectionConfig{Database: "weknora"},
			wantError:  true,
		},
		{
			name:       "doris missing database",
			engineType: types.DorisRetrieverEngineType,
			config:     types.ConnectionConfig{Addr: "doris-fe:9030"},
			wantError:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateConnectionConfig(tt.engineType, tt.config)
			if tt.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
