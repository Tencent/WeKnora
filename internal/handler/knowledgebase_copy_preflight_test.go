package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/application/repository"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// stubKBCopyService provides only the methods the duplicate handler reaches.
// Other interface methods stay embedded so accidental new calls panic in tests.
type stubKBCopyService struct {
	interfaces.KnowledgeBaseService
	byID      func(ctx context.Context, id string) (*types.KnowledgeBase, error)
	duplicate func(ctx context.Context, sourceID string) (*types.KnowledgeBase, error)
}

func (s *stubKBCopyService) GetKnowledgeBaseByID(ctx context.Context, id string) (*types.KnowledgeBase, error) {
	return s.byID(ctx, id)
}

func (s *stubKBCopyService) DuplicateKnowledgeBase(
	ctx context.Context,
	sourceID string,
) (*types.KnowledgeBase, error) {
	return s.duplicate(ctx, sourceID)
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
	var gotSourceID string
	svc := &stubKBCopyService{
		byID: func(_ context.Context, id string) (*types.KnowledgeBase, error) {
			if id != "src" {
				t.Fatalf("handler should only load the source KB, got id=%s", id)
			}
			return &types.KnowledgeBase{ID: "src", TenantID: 1, Name: "Source"}, nil
		},
		duplicate: func(_ context.Context, sourceID string) (*types.KnowledgeBase, error) {
			gotSourceID = sourceID
			return &types.KnowledgeBase{
				ID:        "copy-id",
				TenantID:  1,
				Name:      "Source Copy",
				CreatorID: "u-test",
			}, nil
		},
	}
	r := newDuplicateRouter(svc)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/knowledge-bases/src/duplicate", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 for duplicate, got %d body=%s", w.Code, w.Body.String())
	}
	if gotSourceID != "src" {
		t.Fatalf("duplicate service called with source=%q", gotSourceID)
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
		duplicate: func(_ context.Context, _ string) (*types.KnowledgeBase, error) {
			calledDuplicate = true
			return nil, nil
		},
	}
	r := newDuplicateRouter(svc)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/knowledge-bases/src/duplicate", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for cross-tenant source, got %d body=%s", w.Code, w.Body.String())
	}
	if calledDuplicate {
		t.Fatal("duplicate service must not be called when source KB is outside the caller tenant")
	}
}

func TestDuplicateHandler_SourceNotFound(t *testing.T) {
	calledDuplicate := false
	svc := &stubKBCopyService{
		byID: func(_ context.Context, _ string) (*types.KnowledgeBase, error) {
			return nil, repository.ErrKnowledgeBaseNotFound
		},
		duplicate: func(_ context.Context, _ string) (*types.KnowledgeBase, error) {
			calledDuplicate = true
			return nil, nil
		},
	}
	r := newDuplicateRouter(svc)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/knowledge-bases/missing/duplicate", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing source, got %d body=%s", w.Code, w.Body.String())
	}
	if calledDuplicate {
		t.Fatal("duplicate service must not be called when source KB is missing")
	}
}

func TestDuplicateHandler_PropagatesServiceAppError(t *testing.T) {
	svc := &stubKBCopyService{
		byID: func(_ context.Context, _ string) (*types.KnowledgeBase, error) {
			return &types.KnowledgeBase{ID: "src", TenantID: 1, Name: "Source"}, nil
		},
		duplicate: func(_ context.Context, _ string) (*types.KnowledgeBase, error) {
			return nil, apperrors.NewBadRequestError("invalid vector store binding")
		},
	}
	r := newDuplicateRouter(svc)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/knowledge-bases/src/duplicate", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for service app error, got %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "invalid vector store binding") {
		t.Fatalf("expected service error message in body: %s", w.Body.String())
	}
}

func TestDuplicateHandler_ServiceUnexpectedError(t *testing.T) {
	svc := &stubKBCopyService{
		byID: func(_ context.Context, _ string) (*types.KnowledgeBase, error) {
			return &types.KnowledgeBase{ID: "src", TenantID: 1, Name: "Source"}, nil
		},
		duplicate: func(_ context.Context, _ string) (*types.KnowledgeBase, error) {
			return nil, errors.New("database unavailable")
		},
	}
	r := newDuplicateRouter(svc)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/knowledge-bases/src/duplicate", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for unexpected service error, got %d body=%s", w.Code, w.Body.String())
	}
}
