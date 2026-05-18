package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// ListKnowledgeBases store-enrichment — every KB in the list response
// carries the same resolved vector_store_* metadata as the single-KB
// endpoint. The list path funnels the resolution through
// BatchResolveStoreView so an N-KB list costs one service call rather
// than N. Cross-tenant shared KBs still render via SharedStoreDisplay
// so the owner-tenant's store inventory cannot be correlated across
// rows in the same response.

// stubListKBService returns a fixed slice from ListKnowledgeBases. Only
// the methods exercised by ListKnowledgeBases are implemented; embedding
// the interface keeps the rest nil-panic'ing intentionally.
type stubListKBService struct {
	interfaces.KnowledgeBaseService
	kbs []*types.KnowledgeBase
}

func (s *stubListKBService) ListKnowledgeBases(context.Context) ([]*types.KnowledgeBase, error) {
	return s.kbs, nil
}

// stubVectorStoreService satisfies the two service methods the list
// path depends on: BatchResolveStoreView for bound KBs and
// EnvDefaultStoreView for env-fallback KBs. ResolveStoreView is
// intentionally left nil because ListKnowledgeBases must never reach
// into the single-KB resolver — doing so per row would be the N+1
// pattern this path is designed to avoid.
type stubVectorStoreService struct {
	interfaces.VectorStoreService
	batch      map[string]types.StoreDisplay
	batchCalls int
	batchErr   error
	envView    types.StoreDisplay
}

func (s *stubVectorStoreService) BatchResolveStoreView(
	_ context.Context, _ uint64, storeIDs []string,
) (map[string]types.StoreDisplay, error) {
	s.batchCalls++
	if s.batchErr != nil {
		return nil, s.batchErr
	}
	out := make(map[string]types.StoreDisplay, len(storeIDs))
	for _, id := range storeIDs {
		if v, ok := s.batch[id]; ok {
			out[id] = v
		} else {
			out[id] = types.UnavailableStoreDisplay()
		}
	}
	return out, nil
}

func (s *stubVectorStoreService) EnvDefaultStoreView(_ context.Context) types.StoreDisplay {
	if s.envView.Source == "" {
		return types.DefaultStoreDisplay()
	}
	return s.envView
}

func newListKBRouter(
	t *testing.T,
	svc interfaces.KnowledgeBaseService,
	vss interfaces.VectorStoreService,
) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	r.Use(func(c *gin.Context) {
		c.Set(types.TenantIDContextKey.String(), uint64(1))
		c.Set(types.UserIDContextKey.String(), "u-test")
		c.Next()
	})
	h := &KnowledgeBaseHandler{service: svc, vectorStoreService: vss}
	r.GET("/knowledge-bases", h.ListKnowledgeBases)
	return r
}

func TestListKB_EnrichesEnvBoundAndSharedDistinctly(t *testing.T) {
	storeUserA := "aaaa-bbbb-cccc-dddd"
	storeForeign := "ffff-eeee-dddd-cccc"

	kbs := []*types.KnowledgeBase{
		{ID: "kb-env", Name: "env", TenantID: 1},
		{ID: "kb-bound", Name: "bound", TenantID: 1, VectorStoreID: &storeUserA},
		{ID: "kb-shared", Name: "shared", TenantID: 99, VectorStoreID: &storeForeign},
	}
	vss := &stubVectorStoreService{
		batch: map[string]types.StoreDisplay{
			storeUserA: {
				Name:       "prod-qdrant",
				Source:     types.StoreSourceUser,
				EngineType: "qdrant",
				Status:     "available",
			},
			// storeForeign is intentionally absent — shared KBs do not
			// flow through BatchResolveStoreView so the stub must never
			// see it. The assertion below confirms.
		},
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/knowledge-bases", nil)
	newListKBRouter(t, &stubListKBService{kbs: kbs}, vss).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}

	var envelope struct {
		Success bool                     `json:"success"`
		Data    []map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v body=%s", err, w.Body.String())
	}
	if !envelope.Success || len(envelope.Data) != 3 {
		t.Fatalf("expected 3 rows, got %d body=%s", len(envelope.Data), w.Body.String())
	}

	byID := map[string]map[string]interface{}{}
	for _, row := range envelope.Data {
		byID[row["id"].(string)] = row
	}

	// 1) env-store KB — System default labelling, no engine type.
	envRow := byID["kb-env"]
	if envRow["vector_store_source"] != string(types.StoreSourceEnv) {
		t.Errorf("env KB: expected source=env, got %v", envRow["vector_store_source"])
	}
	if name, _ := envRow["vector_store_name"].(string); name == "" {
		t.Errorf("env KB: expected non-empty system-default name")
	}

	// 2) own-tenant bound KB — name + engine surfaced.
	boundRow := byID["kb-bound"]
	if boundRow["vector_store_source"] != string(types.StoreSourceUser) {
		t.Errorf("bound KB: expected source=user, got %v", boundRow["vector_store_source"])
	}
	if boundRow["vector_store_name"] != "prod-qdrant" {
		t.Errorf("bound KB: expected name=prod-qdrant, got %v", boundRow["vector_store_name"])
	}
	if boundRow["vector_store_engine_type"] != "qdrant" {
		t.Errorf("bound KB: expected engine=qdrant, got %v", boundRow["vector_store_engine_type"])
	}

	// 3) cross-tenant shared KB — UUID stripped, source=shared, no name.
	sharedRow := byID["kb-shared"]
	if _, exists := sharedRow["vector_store_id"]; exists {
		t.Errorf("shared KB must NOT expose vector_store_id, got %v", sharedRow["vector_store_id"])
	}
	if sharedRow["vector_store_source"] != string(types.StoreSourceShared) {
		t.Errorf("shared KB: expected source=shared, got %v", sharedRow["vector_store_source"])
	}
	if name, ok := sharedRow["vector_store_name"]; ok && name != "" {
		t.Errorf("shared KB must not surface a name, got %v", name)
	}
	// Defensive: the foreign store UUID must not appear anywhere in the
	// shared row's serialized payload.
	serialized, _ := json.Marshal(sharedRow)
	if strings.Contains(string(serialized), storeForeign) {
		t.Fatalf("shared row leaked foreign store UUID: %s", serialized)
	}
}

func TestListKB_BatchesStoreLookupsToAvoidNPlus1(t *testing.T) {
	// Five KBs bound to three distinct stores. The list endpoint must
	// resolve them in a single BatchResolveStoreView call regardless of
	// row count — calling the per-KB ResolveStoreView path inside the
	// loop would issue one service call per KB (the N+1 pattern this
	// test pins against).
	s1, s2, s3 := "store-1", "store-2", "store-3"
	kbs := []*types.KnowledgeBase{
		{ID: "a", TenantID: 1, VectorStoreID: &s1},
		{ID: "b", TenantID: 1, VectorStoreID: &s2},
		{ID: "c", TenantID: 1, VectorStoreID: &s1}, // dup
		{ID: "d", TenantID: 1, VectorStoreID: &s3},
		{ID: "e", TenantID: 1}, // env, no store call
	}
	vss := &stubVectorStoreService{
		batch: map[string]types.StoreDisplay{
			s1: {Name: "s1", Source: types.StoreSourceUser, EngineType: "qdrant", Status: "available"},
			s2: {Name: "s2", Source: types.StoreSourceUser, EngineType: "postgres", Status: "available"},
			s3: {Name: "s3", Source: types.StoreSourceUser, EngineType: "weaviate", Status: "available"},
		},
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/knowledge-bases", nil)
	newListKBRouter(t, &stubListKBService{kbs: kbs}, vss).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if vss.batchCalls != 1 {
		t.Fatalf("expected exactly 1 batch store-view call (N+1 protection), got %d", vss.batchCalls)
	}
}

func TestListKB_GracefullyDegradesWhenBatchResolveFails(t *testing.T) {
	// If the store-view resolver fails, the list response must still
	// succeed — bound KBs render as unavailable. The list endpoint is
	// not allowed to 500 just because the vector-store service is
	// momentarily unhealthy.
	storeID := "aaaa-bbbb"
	kbs := []*types.KnowledgeBase{
		{ID: "kb", TenantID: 1, VectorStoreID: &storeID},
	}
	vss := &stubVectorStoreService{batchErr: errSentinel("infra glitch")}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/knowledge-bases", nil)
	newListKBRouter(t, &stubListKBService{kbs: kbs}, vss).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 even when batch resolve fails, got %d body=%s", w.Code, w.Body.String())
	}
	var envelope struct {
		Success bool                     `json:"success"`
		Data    []map[string]interface{} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &envelope)
	if len(envelope.Data) != 1 {
		t.Fatalf("expected 1 row, got %d", len(envelope.Data))
	}
	if envelope.Data[0]["vector_store_source"] != string(types.StoreSourceUnavailable) {
		t.Errorf("expected fallback source=unavailable, got %v", envelope.Data[0]["vector_store_source"])
	}
}
