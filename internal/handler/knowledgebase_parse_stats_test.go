package handler

import (
	"context"
	stderrors "errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// stubKBWithKnowledgeService implements KnowledgeBaseService and a thin
// knowledge-service shim so GetKnowledgeParseStats can be exercised
// through its real handler path. Both are embedded so a future test that
// accidentally touches another method panics loudly.
type stubKBWithKnowledgeService struct {
	interfaces.KnowledgeBaseService
	getByID          func(ctx context.Context, id string) (*types.KnowledgeBase, error)
	parseStats       func(ctx context.Context, kbID string) (map[string]int64, error)
}

func (s *stubKBWithKnowledgeService) GetKnowledgeBaseByID(
	ctx context.Context, id string,
) (*types.KnowledgeBase, error) {
	return s.getByID(ctx, id)
}

// stubKnowledgeSvc adapts the parseStats closure so it satisfies
// interfaces.KnowledgeService for the one method we need.
type stubKnowledgeSvc struct {
	interfaces.KnowledgeService
	parseStats func(ctx context.Context, kbID string) (map[string]int64, error)
}

func (s *stubKnowledgeSvc) GetKnowledgeParseStats(
	ctx context.Context, kbID string,
) (map[string]int64, error) {
	return s.parseStats(ctx, kbID)
}

// newParseStatsTestRouter creates a Gin engine that injects test tenant
// and user, then mounts ONLY the parse-stats GET route wired to a handler
// that carries both stubs.
func newParseStatsTestRouter(
	kbSvc interfaces.KnowledgeBaseService,
	kgSvc interfaces.KnowledgeService,
) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	r.Use(func(c *gin.Context) {
		c.Set(types.TenantIDContextKey.String(), uint64(1))
		c.Set(types.UserIDContextKey.String(), "u-test")
		c.Next()
	})
	h := &KnowledgeBaseHandler{service: kbSvc, knowledgeService: kgSvc}
	r.GET("/knowledge-bases/:id/parse-stats", h.GetKnowledgeParseStats)
	return r
}

// sampleKB returns a KB owned by tenant 1 so validateAndGetKnowledgeBase
// never reaches the cross-tenant shared-KB path.
func sampleKB() *types.KnowledgeBase {
	return &types.KnowledgeBase{
		ID:       "kb-1",
		TenantID: 1,
		Type:     types.KnowledgeBaseTypeDocument,
	}
}

func TestParseStatsReturns200WithValidData(t *testing.T) {
	kbSvc := &stubKBWithKnowledgeService{
		getByID: func(_ context.Context, _ string) (*types.KnowledgeBase, error) {
			return sampleKB(), nil
		},
	}
	kgSvc := &stubKnowledgeSvc{
		parseStats: func(_ context.Context, _ string) (map[string]int64, error) {
			return map[string]int64{
				"completed":  100,
				"processing": 3,
				"pending":    2,
				"failed":     1,
			}, nil
		},
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/knowledge-bases/kb-1/parse-stats", nil)
	newParseStatsTestRouter(kbSvc, kgSvc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"success":true`) {
		t.Fatalf("expected success envelope, got %s", w.Body.String())
	}
	// Verify every expected status key appears in the response.
	for _, key := range []string{`"completed":100`, `"processing":3`, `"pending":2`, `"failed":1`} {
		if !strings.Contains(w.Body.String(), key) {
			t.Fatalf("expected body to contain %s, got %s", key, w.Body.String())
		}
	}
}

func TestParseStatsMapsNotFoundTo404(t *testing.T) {
	kbSvc := &stubKBWithKnowledgeService{
		getByID: func(_ context.Context, _ string) (*types.KnowledgeBase, error) {
			return nil, repository.ErrKnowledgeBaseNotFound
		},
	}
	kgSvc := &stubKnowledgeSvc{} // never called

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/knowledge-bases/missing-kb/parse-stats", nil)
	newParseStatsTestRouter(kbSvc, kgSvc).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing KB, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestParseStatsKeeps500WhenServiceErrors(t *testing.T) {
	kbSvc := &stubKBWithKnowledgeService{
		getByID: func(_ context.Context, _ string) (*types.KnowledgeBase, error) {
			return sampleKB(), nil
		},
	}
	kgSvc := &stubKnowledgeSvc{
		parseStats: func(_ context.Context, _ string) (map[string]int64, error) {
			return nil, stderrors.New("db timeout")
		},
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/knowledge-bases/kb-1/parse-stats", nil)
	newParseStatsTestRouter(kbSvc, kgSvc).ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for service error, got %d body=%s", w.Code, w.Body.String())
	}
}
