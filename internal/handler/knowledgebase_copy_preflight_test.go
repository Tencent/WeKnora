package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// stubKBCopyService provides only the methods the duplicate handler reaches.
// Other interface methods stay embedded so accidental new calls panic in tests.
type stubKBCopyService struct {
	interfaces.KnowledgeBaseService
	byID      func(ctx context.Context, id string) (*types.KnowledgeBase, error)
	duplicate func(ctx context.Context, sourceID, targetID string) (*types.KnowledgeBase, error)
}

func (s *stubKBCopyService) GetKnowledgeBaseByID(ctx context.Context, id string) (*types.KnowledgeBase, error) {
	return s.byID(ctx, id)
}

func (s *stubKBCopyService) DuplicateKnowledgeBase(
	ctx context.Context,
	sourceID string,
	targetID string,
) (*types.KnowledgeBase, error) {
	return s.duplicate(ctx, sourceID, targetID)
}

func newDuplicateRouter(svc interfaces.KnowledgeBaseService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	r.Use(func(c *gin.Context) {
		c.Set(types.TenantIDContextKey.String(), uint64(1))
		c.Set(types.UserIDContextKey.String(), "u-test")
		c.Next()
	})
	h := &KnowledgeBaseHandler{service: svc}
	r.POST("/knowledge-bases/:id/duplicate", h.DuplicateKnowledgeBase)
	return r
}

func TestDuplicateHandler_ReturnsCreatedKnowledgeBase(t *testing.T) {
	var gotSourceID, gotTargetID string
	svc := &stubKBCopyService{
		byID: func(_ context.Context, id string) (*types.KnowledgeBase, error) {
			if id != "src" {
				t.Fatalf("handler should only load the source KB, got id=%s", id)
			}
			return &types.KnowledgeBase{ID: "src", TenantID: 1, Name: "Source"}, nil
		},
		duplicate: func(_ context.Context, sourceID, targetID string) (*types.KnowledgeBase, error) {
			gotSourceID, gotTargetID = sourceID, targetID
			return &types.KnowledgeBase{
				ID:        "copy-id",
				TenantID:  1,
				Name:      "Source",
				CreatorID: "u-test",
			}, nil
		},
	}
	r := newDuplicateRouter(svc)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/knowledge-bases/src/duplicate",
		strings.NewReader(`{"target_id":"copy-id"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 for duplicate, got %d body=%s", w.Code, w.Body.String())
	}
	if gotSourceID != "src" || gotTargetID != "copy-id" {
		t.Fatalf("duplicate service called with source=%q target=%q", gotSourceID, gotTargetID)
	}
	body := w.Body.String()
	for _, want := range []string{`"source_id":"src"`, `"target_id":"copy-id"`, `"knowledge_base"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("response missing %s: %s", want, body)
		}
	}
}

func TestDuplicateHandler_RejectsCrossTenantSource(t *testing.T) {
	calledDuplicate := false
	svc := &stubKBCopyService{
		byID: func(_ context.Context, id string) (*types.KnowledgeBase, error) {
			return &types.KnowledgeBase{ID: id, TenantID: 2, Name: "Shared"}, nil
		},
		duplicate: func(_ context.Context, _, _ string) (*types.KnowledgeBase, error) {
			calledDuplicate = true
			return nil, nil
		},
	}
	r := newDuplicateRouter(svc)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/knowledge-bases/src/duplicate",
		strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for cross-tenant source, got %d body=%s", w.Code, w.Body.String())
	}
	if calledDuplicate {
		t.Fatal("duplicate service must not be called when source KB is outside the caller tenant")
	}
}
