package retriever

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockEngineService is a minimal mock for testing registry operations.
// Only EngineType() is meaningful; all other methods are no-ops.
type mockEngineService struct {
	engineType types.RetrieverEngineType
}

func (m *mockEngineService) EngineType() types.RetrieverEngineType { return m.engineType }
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

func newMock(engineType types.RetrieverEngineType) interfaces.RetrieveEngineService {
	return &mockEngineService{engineType: engineType}
}

// --- Register (byEngineType) tests ---

func TestRegistry_Register(t *testing.T) {
	reg := NewRetrieveEngineRegistry().(*RetrieveEngineRegistry)

	t.Run("success", func(t *testing.T) {
		err := reg.Register(newMock(types.PostgresRetrieverEngineType))
		assert.NoError(t, err)
	})

	t.Run("duplicate engine type returns error", func(t *testing.T) {
		err := reg.Register(newMock(types.PostgresRetrieverEngineType))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "already registered")
	})
}

func TestRegistry_GetRetrieveEngineService(t *testing.T) {
	reg := NewRetrieveEngineRegistry().(*RetrieveEngineRegistry)
	_ = reg.Register(newMock(types.PostgresRetrieverEngineType))

	t.Run("found", func(t *testing.T) {
		svc, err := reg.GetRetrieveEngineService(types.PostgresRetrieverEngineType)
		assert.NoError(t, err)
		assert.NotNil(t, svc)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := reg.GetRetrieveEngineService(types.QdrantRetrieverEngineType)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestRegistry_GetAllRetrieveEngineServices(t *testing.T) {
	reg := NewRetrieveEngineRegistry().(*RetrieveEngineRegistry)
	_ = reg.Register(newMock(types.PostgresRetrieverEngineType))
	_ = reg.Register(newMock(types.ElasticsearchRetrieverEngineType))

	t.Run("returns all byEngineType entries", func(t *testing.T) {
		all := reg.GetAllRetrieveEngineServices()
		assert.Len(t, all, 2)
	})

	t.Run("returns copy - modifying result does not affect registry", func(t *testing.T) {
		all := reg.GetAllRetrieveEngineServices()
		all = append(all, newMock(types.QdrantRetrieverEngineType))
		assert.Len(t, reg.GetAllRetrieveEngineServices(), 2)
	})
}

// --- RegisterWithStoreID (byStoreID) tests ---

func TestRegistry_RegisterWithStoreID(t *testing.T) {
	reg := NewRetrieveEngineRegistry().(*RetrieveEngineRegistry)

	t.Run("success", func(t *testing.T) {
		reg.RegisterWithStoreID("store-1", newMock(types.PostgresRetrieverEngineType))
		svc, err := reg.GetByStoreID("store-1")
		assert.NoError(t, err)
		assert.NotNil(t, svc)
	})

	t.Run("upsert overwrites existing", func(t *testing.T) {
		newSvc := newMock(types.ElasticsearchRetrieverEngineType)
		reg.RegisterWithStoreID("store-1", newSvc)
		svc, err := reg.GetByStoreID("store-1")
		assert.NoError(t, err)
		assert.Equal(t, types.ElasticsearchRetrieverEngineType, svc.EngineType())
	})

	t.Run("same engine type different store IDs", func(t *testing.T) {
		reg.RegisterWithStoreID("es-hot", newMock(types.ElasticsearchRetrieverEngineType))
		reg.RegisterWithStoreID("es-warm", newMock(types.ElasticsearchRetrieverEngineType))

		svc1, err1 := reg.GetByStoreID("es-hot")
		svc2, err2 := reg.GetByStoreID("es-warm")
		assert.NoError(t, err1)
		assert.NoError(t, err2)
		assert.NotSame(t, svc1, svc2)
	})
}

func TestRegistry_GetByStoreID(t *testing.T) {
	reg := NewRetrieveEngineRegistry().(*RetrieveEngineRegistry)
	reg.RegisterWithStoreID("store-1", newMock(types.PostgresRetrieverEngineType))

	t.Run("found", func(t *testing.T) {
		svc, err := reg.GetByStoreID("store-1")
		assert.NoError(t, err)
		assert.NotNil(t, svc)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := reg.GetByStoreID("nonexistent")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestRegistry_UnregisterByStoreID(t *testing.T) {
	reg := NewRetrieveEngineRegistry().(*RetrieveEngineRegistry)
	reg.RegisterWithStoreID("store-1", newMock(types.PostgresRetrieverEngineType))

	t.Run("removes registered store", func(t *testing.T) {
		reg.UnregisterByStoreID("store-1")
		_, err := reg.GetByStoreID("store-1")
		assert.Error(t, err)
	})

	t.Run("idempotent on nonexistent store", func(t *testing.T) {
		reg.UnregisterByStoreID("nonexistent") // should not panic
	})
}

// --- Dual map isolation tests ---

func TestRegistry_DualMapIsolation(t *testing.T) {
	reg := NewRetrieveEngineRegistry().(*RetrieveEngineRegistry)

	_ = reg.Register(newMock(types.PostgresRetrieverEngineType))
	reg.RegisterWithStoreID("store-pg", newMock(types.PostgresRetrieverEngineType))
	reg.RegisterWithStoreID("store-es", newMock(types.ElasticsearchRetrieverEngineType))

	t.Run("GetAllRetrieveEngineServices returns only byEngineType", func(t *testing.T) {
		all := reg.GetAllRetrieveEngineServices()
		assert.Len(t, all, 1)
	})

	t.Run("byStoreID does not affect byEngineType lookup", func(t *testing.T) {
		_, err := reg.GetRetrieveEngineService(types.ElasticsearchRetrieverEngineType)
		assert.Error(t, err) // ES is only in byStoreID, not byEngineType
	})

	t.Run("unregister byStoreID does not affect byEngineType", func(t *testing.T) {
		reg.UnregisterByStoreID("store-pg")
		svc, err := reg.GetRetrieveEngineService(types.PostgresRetrieverEngineType)
		assert.NoError(t, err)
		assert.NotNil(t, svc)
	})
}

// --- Concurrency test ---

func TestRegistry_ConcurrentAccess(t *testing.T) {
	reg := NewRetrieveEngineRegistry().(*RetrieveEngineRegistry)
	const goroutines = 10

	var wg sync.WaitGroup
	wg.Add(goroutines * 3)

	for i := 0; i < goroutines; i++ {
		storeID := fmt.Sprintf("store-%d", i)
		go func() {
			defer wg.Done()
			reg.RegisterWithStoreID(storeID, newMock(types.PostgresRetrieverEngineType))
		}()
		go func() {
			defer wg.Done()
			_, _ = reg.GetByStoreID(storeID)
		}()
		go func() {
			defer wg.Done()
			reg.UnregisterByStoreID(storeID)
		}()
	}

	wg.Wait()
}

// --- Interface compliance ---

func TestRegistry_ImplementsStoreRegistry(t *testing.T) {
	reg := NewRetrieveEngineRegistry()
	concreteReg, ok := reg.(*RetrieveEngineRegistry)
	require.True(t, ok)

	var _ interfaces.StoreRegistry = concreteReg
}
