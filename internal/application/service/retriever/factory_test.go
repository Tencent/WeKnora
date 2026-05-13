package retriever

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ----- test fakes -----

// fakeOwnership is an in-memory TenantStoreOwnership implementation.
// All mutable state is guarded by mu so that the race test exercises a
// safe fake and surfaces only genuine races inside the factory code.
type fakeOwnership struct {
	mu sync.Mutex
	// owned maps storeID → tenantID that owns it. Absence means "not owned".
	owned map[string]uint64
	// err, when non-nil, is returned from every StoreOwnedBy call
	// (used to simulate infrastructure failures).
	err error
	// calls records every (storeID, tenantID) pair asked. Tests use this
	// to assert that the factory short-circuits to the fallback path
	// without calling ownership when vectorStoreID is nil/empty.
	calls []ownershipCall
}

type ownershipCall struct {
	storeID  string
	tenantID uint64
}

func (f *fakeOwnership) StoreOwnedBy(ctx context.Context, storeID string, tenantID uint64) (bool, error) {
	f.mu.Lock()
	f.calls = append(f.calls, ownershipCall{storeID: storeID, tenantID: tenantID})
	err := f.err
	ownedTenant, ok := f.owned[storeID]
	f.mu.Unlock()
	if err != nil {
		return false, err
	}
	return ok && ownedTenant == tenantID, nil
}

// callCount returns the number of StoreOwnedBy calls recorded so far.
// Safe for concurrent use alongside StoreOwnedBy.
func (f *fakeOwnership) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// fakeEngine implements interfaces.RetrieveEngineService with only the
// methods that factory paths exercise. All other methods panic so that
// accidental use during testing is loud.
type fakeEngine struct {
	engineType types.RetrieverEngineType
	support    []types.RetrieverType
}

func (f *fakeEngine) EngineType() types.RetrieverEngineType { return f.engineType }

func (f *fakeEngine) Support() []types.RetrieverType { return f.support }

func (f *fakeEngine) Retrieve(ctx context.Context, _ types.RetrieveParams) ([]*types.RetrieveResult, error) {
	panic("fakeEngine.Retrieve: not used in factory tests")
}

func (f *fakeEngine) Index(ctx context.Context, _ embedding.Embedder, _ *types.IndexInfo, _ []types.RetrieverType) error {
	panic("fakeEngine.Index: not used in factory tests")
}

func (f *fakeEngine) BatchIndex(ctx context.Context, _ embedding.Embedder, _ []*types.IndexInfo, _ []types.RetrieverType) error {
	panic("fakeEngine.BatchIndex: not used in factory tests")
}

func (f *fakeEngine) EstimateStorageSize(ctx context.Context, _ embedding.Embedder, _ []*types.IndexInfo, _ []types.RetrieverType) int64 {
	panic("fakeEngine.EstimateStorageSize: not used in factory tests")
}

func (f *fakeEngine) CopyIndices(ctx context.Context, _ string, _ map[string]string, _ map[string]string, _ string, _ int, _ string) error {
	panic("fakeEngine.CopyIndices: not used in factory tests")
}

func (f *fakeEngine) DeleteByChunkIDList(ctx context.Context, _ []string, _ int, _ string) error {
	panic("fakeEngine.DeleteByChunkIDList: not used in factory tests")
}

func (f *fakeEngine) DeleteBySourceIDList(ctx context.Context, _ []string, _ int, _ string) error {
	panic("fakeEngine.DeleteBySourceIDList: not used in factory tests")
}

func (f *fakeEngine) DeleteByKnowledgeIDList(ctx context.Context, _ []string, _ int, _ string) error {
	panic("fakeEngine.DeleteByKnowledgeIDList: not used in factory tests")
}

func (f *fakeEngine) BatchUpdateChunkEnabledStatus(ctx context.Context, _ map[string]bool) error {
	panic("fakeEngine.BatchUpdateChunkEnabledStatus: not used in factory tests")
}

func (f *fakeEngine) BatchUpdateChunkTagID(ctx context.Context, _ map[string]string) error {
	panic("fakeEngine.BatchUpdateChunkTagID: not used in factory tests")
}

// registryWithStores builds a registry with the given storeID → service
// pairs populated in the byStoreID map. Engine-type entries are also
// registered so that the unbound path can resolve engines via the
// tenant's effective engines.
func registryWithStores(t *testing.T, stores map[string]*fakeEngine, engineTypes map[types.RetrieverEngineType]*fakeEngine) *RetrieveEngineRegistry {
	t.Helper()
	r := &RetrieveEngineRegistry{
		byEngineType: map[types.RetrieverEngineType]interfaces.RetrieveEngineService{},
		byStoreID:    map[string]interfaces.RetrieveEngineService{},
	}
	for id, svc := range stores {
		r.byStoreID[id] = svc
	}
	for et, svc := range engineTypes {
		r.byEngineType[et] = svc
	}
	return r
}

// newTenantCtx returns a context carrying a Tenant with the given
// EffectiveEngines. Factory's unbound path consumes this via
// types.TenantInfoFromContext.
func newTenantCtx(engines []types.RetrieverEngineParams) context.Context {
	tenant := &types.Tenant{
		RetrieverEngines: types.RetrieverEngines{Engines: engines},
	}
	return context.WithValue(context.Background(), types.TenantInfoContextKey, tenant)
}

// ----- CreateRetrieveEngineForKB -----

func TestCreateRetrieveEngineForKB_Unbound(t *testing.T) {
	postgresEngine := &fakeEngine{
		engineType: types.PostgresRetrieverEngineType,
		support:    []types.RetrieverType{types.KeywordsRetrieverType, types.VectorRetrieverType},
	}
	registry := registryWithStores(t, nil, map[types.RetrieverEngineType]*fakeEngine{
		types.PostgresRetrieverEngineType: postgresEngine,
	})
	ownership := &fakeOwnership{}

	tenantCtx := newTenantCtx([]types.RetrieverEngineParams{
		{RetrieverEngineType: types.PostgresRetrieverEngineType, RetrieverType: types.VectorRetrieverType},
	})

	cases := []struct {
		name  string
		store *string
	}{
		{"nil pointer", nil},
		{"empty string pointer", strPtr("")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			engine, err := CreateRetrieveEngineForKB(tenantCtx, registry, ownership, 1, tc.store)
			require.NoError(t, err)
			require.NotNil(t, engine)
			assert.Len(t, engine.engineInfos, 1, "unbound path uses tenant effective engines")
			assert.Same(t, postgresEngine, engine.engineInfos[0].retrieveEngine)
			assert.Zero(t, ownership.callCount(),
				"ownership must not be called on the unbound path")
		})
	}
}

func TestCreateRetrieveEngineForKB_UnboundMissingTenant(t *testing.T) {
	registry := registryWithStores(t, nil, nil)
	ownership := &fakeOwnership{}

	_, err := CreateRetrieveEngineForKB(context.Background(), registry, ownership, 1, nil)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrTenantInfoMissing),
		"err %q must wrap ErrTenantInfoMissing", err)
}

func TestCreateRetrieveEngineForKB_StoreBound(t *testing.T) {
	esEngine := &fakeEngine{
		engineType: types.ElasticsearchRetrieverEngineType,
		support:    []types.RetrieverType{types.KeywordsRetrieverType, types.VectorRetrieverType},
	}
	registry := registryWithStores(t,
		map[string]*fakeEngine{"store-A": esEngine},
		nil,
	)
	ownership := &fakeOwnership{owned: map[string]uint64{"store-A": 1}}

	storeID := "store-A"
	engine, err := CreateRetrieveEngineForKB(context.Background(), registry, ownership, 1, &storeID)

	require.NoError(t, err)
	require.NotNil(t, engine)
	require.Len(t, engine.engineInfos, 1)
	assert.Same(t, esEngine, engine.engineInfos[0].retrieveEngine)
	assert.Equal(t,
		[]types.RetrieverType{types.KeywordsRetrieverType, types.VectorRetrieverType},
		engine.engineInfos[0].retrieverType,
		"store-bound KB uses every retriever type the store supports")
}

func TestCreateRetrieveEngineForKB_CrossTenant(t *testing.T) {
	esEngine := &fakeEngine{
		engineType: types.ElasticsearchRetrieverEngineType,
		support:    []types.RetrieverType{types.VectorRetrieverType},
	}
	registry := registryWithStores(t,
		map[string]*fakeEngine{"store-A": esEngine},
		nil,
	)
	// store-A is owned by tenant 2, not tenant 1
	ownership := &fakeOwnership{owned: map[string]uint64{"store-A": 2}}

	storeID := "store-A"
	_, err := CreateRetrieveEngineForKB(context.Background(), registry, ownership, 1, &storeID)

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrVectorStoreForbidden),
		"cross-tenant access must yield ErrVectorStoreForbidden; got %q", err)
	// Sanity: no store UUID in the sentinel's message — the structured log
	// emitted inside the factory is where UUIDs live. The user-visible
	// error is intentionally opaque.
	assert.NotContains(t, err.Error(), "store-A",
		"sentinel error must not expose store UUIDs to callers")
}

func TestCreateRetrieveEngineForKB_StoreNotRegistered(t *testing.T) {
	// Ownership says the tenant owns the store, but registry has not
	// loaded it. This happens when a VectorStore row exists in the DB
	// but its engine initialization failed at app start.
	registry := registryWithStores(t, nil, nil)
	ownership := &fakeOwnership{owned: map[string]uint64{"store-A": 1}}

	storeID := "store-A"
	_, err := CreateRetrieveEngineForKB(context.Background(), registry, ownership, 1, &storeID)

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrVectorStoreNotFound),
		"unregistered store must yield ErrVectorStoreNotFound; got %q", err)
}

func TestCreateRetrieveEngineForKB_OwnershipLookupError(t *testing.T) {
	registry := registryWithStores(t,
		map[string]*fakeEngine{"store-A": {engineType: types.PostgresRetrieverEngineType}},
		nil,
	)
	ownership := &fakeOwnership{err: errors.New("db connection refused")}

	storeID := "store-A"
	_, err := CreateRetrieveEngineForKB(context.Background(), registry, ownership, 1, &storeID)

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrVectorStoreNotFound),
		"ownership infrastructure error must collapse to ErrVectorStoreNotFound; got %q", err)
}

// ----- CreateRetrieveEngineFromPayload -----

func TestCreateRetrieveEngineFromPayload_LegacyUnbound(t *testing.T) {
	// Four ways a payload can express "no binding": missing field,
	// explicit null, empty string, and programmatically nil. All four
	// should take the fallback path using the payload's effectiveEngines.
	postgresEngine := &fakeEngine{
		engineType: types.PostgresRetrieverEngineType,
		support:    []types.RetrieverType{types.VectorRetrieverType},
	}
	registry := registryWithStores(t, nil, map[types.RetrieverEngineType]*fakeEngine{
		types.PostgresRetrieverEngineType: postgresEngine,
	})
	ownership := &fakeOwnership{}
	engines := []types.RetrieverEngineParams{{
		RetrieverEngineType: types.PostgresRetrieverEngineType,
		RetrieverType:       types.VectorRetrieverType,
	}}

	// simulate payload decoding from JSON for each legacy shape
	decode := func(t *testing.T, body string) *string {
		t.Helper()
		var p types.KBDeletePayload
		require.NoError(t, json.Unmarshal([]byte(body), &p))
		return p.VectorStoreID
	}

	cases := []struct {
		name string
		ptr  *string
	}{
		{"missing field", decode(t, `{"tenant_id":1}`)},
		{"explicit null", decode(t, `{"tenant_id":1,"vector_store_id":null}`)},
		{"empty string", decode(t, `{"tenant_id":1,"vector_store_id":""}`)},
		{"programmatic nil", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			engine, err := CreateRetrieveEngineFromPayload(
				context.Background(), registry, ownership, 1, engines, tc.ptr)
			require.NoError(t, err)
			require.NotNil(t, engine)
			require.Len(t, engine.engineInfos, 1)
			assert.Same(t, postgresEngine, engine.engineInfos[0].retrieveEngine)
			assert.Zero(t, ownership.callCount(),
				"unbound payload must not trigger ownership lookup")
		})
	}
}

func TestCreateRetrieveEngineFromPayload_Bound(t *testing.T) {
	qdrantEngine := &fakeEngine{
		engineType: types.QdrantRetrieverEngineType,
		support:    []types.RetrieverType{types.VectorRetrieverType},
	}
	registry := registryWithStores(t,
		map[string]*fakeEngine{"qd-1": qdrantEngine},
		nil,
	)
	ownership := &fakeOwnership{owned: map[string]uint64{"qd-1": 42}}

	storeID := "qd-1"
	engine, err := CreateRetrieveEngineFromPayload(
		context.Background(), registry, ownership, 42, nil, &storeID)

	require.NoError(t, err)
	require.NotNil(t, engine)
	require.Len(t, engine.engineInfos, 1)
	assert.Same(t, qdrantEngine, engine.engineInfos[0].retrieveEngine)
}

func TestCreateRetrieveEngineFromPayload_TamperedCrossTenant(t *testing.T) {
	esEngine := &fakeEngine{engineType: types.ElasticsearchRetrieverEngineType,
		support: []types.RetrieverType{types.VectorRetrieverType}}
	registry := registryWithStores(t,
		map[string]*fakeEngine{"store-A": esEngine}, nil)
	// Store is owned by tenant 99, but the (possibly tampered) payload
	// claims tenant 1. Factory must reject.
	ownership := &fakeOwnership{owned: map[string]uint64{"store-A": 99}}

	storeID := "store-A"
	_, err := CreateRetrieveEngineFromPayload(
		context.Background(), registry, ownership, 1,
		[]types.RetrieverEngineParams{}, &storeID)

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrVectorStoreForbidden))
}

// ----- Race -----

// TestFactoryParallelInvocation is primarily meaningful when run under
// `go test -race`. Factory functions themselves are stateless; the test
// guards against accidental shared state being introduced later.
func TestFactoryParallelInvocation(t *testing.T) {
	esEngine := &fakeEngine{
		engineType: types.ElasticsearchRetrieverEngineType,
		support:    []types.RetrieverType{types.VectorRetrieverType},
	}
	registry := registryWithStores(t,
		map[string]*fakeEngine{"store-A": esEngine},
		nil,
	)
	ownership := &fakeOwnership{owned: map[string]uint64{"store-A": 1}}

	done := make(chan error, 16)
	for i := 0; i < 16; i++ {
		go func() {
			storeID := "store-A"
			_, err := CreateRetrieveEngineForKB(
				context.Background(), registry, ownership, 1, &storeID)
			done <- err
		}()
	}
	for i := 0; i < 16; i++ {
		if err := <-done; err != nil {
			t.Errorf("concurrent call %d failed: %v", i, err)
		}
	}
}

// ----- helpers -----

func strPtr(s string) *string { return &s }
