package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// CreateKnowledgeBase typed-error preservation — the handler must surface
// the typed AppError (ErrVectorStoreBindingInvalid / ErrVectorStoreUnavailable)
// returned by validateVectorStoreBinding instead of stripping it into a
// generic 500 via NewInternalServerError. Without the IsAppError unwrap in
// the handler, the typed error codes would be silently nullified at the
// HTTP boundary and clients would lose the ability to branch on the cause.
//
// Shared-KB UUID suppression — responses for cross-tenant shared KBs must
// not leak the owner tenant's vector_store_id UUID. SharedStoreDisplay
// suppresses store name + engine_type for cross-tenant callers, but the
// underlying KnowledgeBase.MarshalJSON still emits the UUID; the
// buildKBResponse strip closes the gap so the UUID cannot be correlated
// across multiple shared KBs.

// stubKBCreateService drives CreateKnowledgeBase end-to-end with a
// service that returns a chosen error. Embedding the interface keeps
// any other method nil-panic'ing on purpose.
type stubKBCreateService struct {
	interfaces.KnowledgeBaseService
	createErr error
}

func (s *stubKBCreateService) CreateKnowledgeBase(_ context.Context, kb *types.KnowledgeBase) (*types.KnowledgeBase, error) {
	if s.createErr != nil {
		return nil, s.createErr
	}
	kb.ID = "kb-new"
	kb.TenantID = 1
	return kb, nil
}

func newCreateKBRouter(svc interfaces.KnowledgeBaseService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	r.Use(func(c *gin.Context) {
		c.Set(types.TenantIDContextKey.String(), uint64(1))
		c.Set(types.UserIDContextKey.String(), "u-test")
		c.Next()
	})
	h := &KnowledgeBaseHandler{service: svc}
	r.POST("/knowledge-bases", h.CreateKnowledgeBase)
	return r
}

func TestCreateKB_PreservesTypedErrorCode_2200(t *testing.T) {
	svc := &stubKBCreateService{
		createErr: apperrors.NewVectorStoreBindingInvalidError("vector store not found"),
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/knowledge-bases",
		strings.NewReader(`{"name":"kb"}`))
	req.Header.Set("Content-Type", "application/json")
	newCreateKBRouter(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `"code":2200`) {
		t.Fatalf("expected envelope to contain code 2200, got %s", body)
	}
	if strings.Contains(body, `"code":1007`) || strings.Contains(body, `"code":1000`) {
		t.Fatalf("typed error must not be wrapped into a generic code, got %s", body)
	}
}

func TestCreateKB_PreservesTypedErrorCode_2201(t *testing.T) {
	svc := &stubKBCreateService{
		createErr: apperrors.NewVectorStoreUnavailableError(""),
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/knowledge-bases",
		strings.NewReader(`{"name":"kb"}`))
	req.Header.Set("Content-Type", "application/json")
	newCreateKBRouter(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"code":2201`) {
		t.Fatalf("expected envelope to contain code 2201, got %s", w.Body.String())
	}
}

func TestCreateKB_GenericErrorStillFallsThroughTo500(t *testing.T) {
	// A non-AppError must NOT be auto-rewritten to 200/400 — operational
	// monitoring still needs to see infrastructure failures as 5xx.
	svc := &stubKBCreateService{createErr: errSentinel("connection refused")}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/knowledge-bases",
		strings.NewReader(`{"name":"kb"}`))
	req.Header.Set("Content-Type", "application/json")
	newCreateKBRouter(svc).ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for raw infra error, got %d body=%s", w.Code, w.Body.String())
	}
}

type errSentinel string

func (e errSentinel) Error() string { return string(e) }

// ---------------------------------------------------------------------------
// buildKBResponse must strip vector_store_id for shared KB responses
// ---------------------------------------------------------------------------

func TestBuildKBResponse_StripsVectorStoreIDForSharedKB(t *testing.T) {
	storeID := "aaaa-bbbb-cccc-dddd"
	kb := &types.KnowledgeBase{
		ID:               "kb-1",
		Name:             "shared-kb",
		TenantID:         42, // different from caller
		EmbeddingModelID: "e",
		SummaryModelID:   "s",
		VectorStoreID:    &storeID,
	}
	got := buildKBResponse(kb, types.SharedStoreDisplay(), nil)
	m, ok := got.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map result, got %T", got)
	}
	if _, exists := m["vector_store_id"]; exists {
		t.Fatalf("shared KB response must not expose vector_store_id, got %v", m["vector_store_id"])
	}
	if _, exists := m["vector_store_name"]; exists {
		t.Fatalf("shared KB response must not expose vector_store_name, got %v", m["vector_store_name"])
	}
	if m["vector_store_source"] != types.StoreSourceShared {
		t.Fatalf("expected vector_store_source=shared, got %v", m["vector_store_source"])
	}
	// Defensive: ensure the source UUID does not appear *anywhere* in
	// the serialized output (paranoid check against future map keys).
	serialized, _ := json.Marshal(m)
	if strings.Contains(string(serialized), storeID) {
		t.Fatalf("shared KB response leaked vector store UUID via some path: %s", serialized)
	}
}

func TestBuildKBResponse_KeepsVectorStoreIDForOwnerKB(t *testing.T) {
	// Same setup but with the user-source display — owner caller should
	// still see the UUID alongside the resolved metadata.
	storeID := "aaaa-bbbb-cccc-dddd"
	kb := &types.KnowledgeBase{
		ID:               "kb-1",
		Name:             "owner-kb",
		TenantID:         1,
		EmbeddingModelID: "e",
		SummaryModelID:   "s",
		VectorStoreID:    &storeID,
	}
	view := types.StoreDisplay{
		Name:       "prod-es",
		Source:     types.StoreSourceUser,
		EngineType: "elasticsearch",
		Status:     "available",
	}
	got := buildKBResponse(kb, view, nil)
	m, ok := got.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map result, got %T", got)
	}
	if m["vector_store_id"] != storeID {
		t.Fatalf("owner KB must keep vector_store_id, got %v", m["vector_store_id"])
	}
	if m["vector_store_name"] != "prod-es" {
		t.Fatalf("owner KB must surface store name, got %v", m["vector_store_name"])
	}
}
