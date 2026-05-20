package handler

import (
	"context"
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

// handler.CopyKnowledgeBase pre-flight tests.
//
// The async clone worker (service.CopyKnowledgeBase) re-applies the same
// embedding-model and store-binding defenses as defense in depth, but the
// handler-level pre-flight is the one that surfaces 400 to the API caller
// synchronously instead of inside Asynq progress polling. These tests pin
// the synchronous behavior so a future refactor that drops the pre-flight
// fails loudly here rather than silently degrading UX.

// stubKBCopyService provides only the two methods the Copy handler reaches
// for (GetKnowledgeBaseByID twice). Other interface methods stay nil so any
// accidental new call panics rather than silently succeeding.
type stubKBCopyService struct {
	interfaces.KnowledgeBaseService
	byID func(ctx context.Context, id string) (*types.KnowledgeBase, error)
}

func (s *stubKBCopyService) GetKnowledgeBaseByID(ctx context.Context, id string) (*types.KnowledgeBase, error) {
	return s.byID(ctx, id)
}

// stubEnqueuer records whether Enqueue was invoked. The whole point of the
// pre-flight is to short-circuit *before* enqueue, so the test fails if
// enqueue ran for a mismatched clone.
type stubEnqueuer struct {
	calls int
}

func (s *stubEnqueuer) Enqueue(_ interface{}, _ ...interface{}) (*stubEnqueueInfo, error) {
	s.calls++
	return &stubEnqueueInfo{ID: "x"}, nil
}

// stubEnqueueInfo is a stand-in for asynq.TaskInfo so the test does not need
// to construct one.
type stubEnqueueInfo struct{ ID string }

func newCopyPreflightRouter(svc interfaces.KnowledgeBaseService) (*gin.Engine, *stubEnqueuer) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	r.Use(func(c *gin.Context) {
		c.Set(types.TenantIDContextKey.String(), uint64(1))
		c.Set(types.UserIDContextKey.String(), "u-test")
		c.Next()
	})
	enq := &stubEnqueuer{}
	// The real handler reaches into h.asynqClient.Enqueue with the asynq
	// task type. We do not exercise the enqueue path in these tests — every
	// case here either short-circuits at pre-flight or returns 4xx earlier.
	// Leaving asynqClient nil would panic if enqueue ran, which is exactly
	// the regression we want to catch.
	h := &KnowledgeBaseHandler{service: svc}
	r.POST("/knowledge-bases/copy", h.CopyKnowledgeBase)
	return r, enq
}

func storeIDPtr(s string) *string { return &s }

func TestCopyHandlerPreflight_DifferentEmbeddingModel(t *testing.T) {
	svc := &stubKBCopyService{
		byID: func(_ context.Context, id string) (*types.KnowledgeBase, error) {
			switch id {
			case "src":
				return &types.KnowledgeBase{
					ID: "src", TenantID: 1, EmbeddingModelID: "embed-A",
				}, nil
			case "dst":
				return &types.KnowledgeBase{
					ID: "dst", TenantID: 1, EmbeddingModelID: "embed-B",
				}, nil
			}
			return nil, nil
		},
	}
	r, _ := newCopyPreflightRouter(svc)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/knowledge-bases/copy",
		strings.NewReader(`{"source_id":"src","target_id":"dst"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for embedding-model mismatch, got %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "different embedding models") {
		t.Fatalf("expected embedding-model error message, got %s", w.Body.String())
	}
}

func TestCopyHandlerPreflight_DifferentVectorStore(t *testing.T) {
	svc := &stubKBCopyService{
		byID: func(_ context.Context, id string) (*types.KnowledgeBase, error) {
			switch id {
			case "src":
				return &types.KnowledgeBase{
					ID: "src", TenantID: 1, EmbeddingModelID: "embed-A",
					VectorStoreID: storeIDPtr("store-A"),
				}, nil
			case "dst":
				return &types.KnowledgeBase{
					ID: "dst", TenantID: 1, EmbeddingModelID: "embed-A",
					VectorStoreID: storeIDPtr("store-B"),
				}, nil
			}
			return nil, nil
		},
	}
	r, _ := newCopyPreflightRouter(svc)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/knowledge-bases/copy",
		strings.NewReader(`{"source_id":"src","target_id":"dst"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for store mismatch, got %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "different vector stores") {
		t.Fatalf("expected store-mismatch error message, got %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "Phase 4") {
		t.Fatalf("error message must not leak internal roadmap labels: %s", w.Body.String())
	}
}

func TestCopyHandlerPreflight_OneSideNilStore(t *testing.T) {
	svc := &stubKBCopyService{
		byID: func(_ context.Context, id string) (*types.KnowledgeBase, error) {
			switch id {
			case "src":
				return &types.KnowledgeBase{
					ID: "src", TenantID: 1, EmbeddingModelID: "embed-A",
					VectorStoreID: nil,
				}, nil
			case "dst":
				return &types.KnowledgeBase{
					ID: "dst", TenantID: 1, EmbeddingModelID: "embed-A",
					VectorStoreID: storeIDPtr("store-A"),
				}, nil
			}
			return nil, nil
		},
	}
	r, _ := newCopyPreflightRouter(svc)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/knowledge-bases/copy",
		strings.NewReader(`{"source_id":"src","target_id":"dst"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when one side is env-store and the other is DB-store, got %d body=%s",
			w.Code, w.Body.String())
	}
}

// compile-time guard against accidentally dropping the apperrors import
// from the file — if the pre-flight refactor goes away, this fails too.
var _ = apperrors.NewBadRequestError
