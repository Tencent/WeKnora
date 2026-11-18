package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// stubWikiPageService captures the list request the handler builds. Only
// ListPages is implemented; the embedded interface keeps every other method
// nil-panicky on purpose, so a test that unexpectedly reaches an un-stubbed
// method fails loudly instead of silently returning zero values.
type stubWikiPageService struct {
	interfaces.WikiPageService
	listPages func(ctx context.Context, req *types.WikiPageListRequest) (*types.WikiPageListResponse, error)
}

func (s *stubWikiPageService) ListPages(
	ctx context.Context, req *types.WikiPageListRequest,
) (*types.WikiPageListResponse, error) {
	return s.listPages(ctx, req)
}

// stubWikiKBService serves only the KB lookup validateWikiKB performs, and
// returns a wiki-enabled KB so the handler proceeds past validation.
type stubWikiKBService struct {
	interfaces.KnowledgeBaseService
}

func (s *stubWikiKBService) GetKnowledgeBaseByID(_ context.Context, id string) (*types.KnowledgeBase, error) {
	return &types.KnowledgeBase{
		ID:               id,
		IndexingStrategy: types.IndexingStrategy{WikiEnabled: true},
	}, nil
}

// newWikiListTestRouter mounts ListPages with just the tenant key the handler
// reads via c.GetUint64, without pulling in the production RBAC chain.
func newWikiListTestRouter(wikiSvc interfaces.WikiPageService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(types.TenantIDContextKey.String(), uint64(1))
		c.Next()
	})
	h := &WikiPageHandler{wikiService: wikiSvc, kbService: &stubWikiKBService{}}
	r.GET("/knowledgebase/:kb_id/wiki/pages", h.ListPages)
	return r
}

// wikiListSizeUpperBound mirrors the sibling revision-list cap in this file
// (ListRevisions clamps its limit to the same value). It is pinned here as a
// literal instead of referencing the handler constant on purpose: this test
// must still compile — and fail on the assertion below — against the unfixed
// handler, which is what makes the regression guard meaningful.
const wikiListSizeUpperBound = 200

// TestWikiListPagesClampsPageSize pins the fix for the unbounded page_size on
// the wiki list endpoint. The repository floors page_size but has no ceiling,
// so before the fix ?page_size=100000000 reached the service untouched and the
// SQL LIMIT pulled every page of the KB — Content column included — into
// memory in a single request. The handler now clamps like its sibling list
// endpoints do.
func TestWikiListPagesClampsPageSize(t *testing.T) {
	cases := []struct {
		name  string
		query string
		want  int
	}{
		{name: "absent falls back to default", query: "", want: 20},
		{name: "zero falls back to default", query: "?page_size=0", want: 20},
		{name: "negative falls back to default", query: "?page_size=-5", want: 20},
		{name: "in-range value is preserved", query: "?page_size=50", want: 50},
		{name: "cap is preserved", query: "?page_size=200", want: wikiListSizeUpperBound},
		{name: "oversized value is clamped", query: "?page_size=100000000", want: wikiListSizeUpperBound},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got *types.WikiPageListRequest
			wikiSvc := &stubWikiPageService{
				listPages: func(
					_ context.Context, req *types.WikiPageListRequest,
				) (*types.WikiPageListResponse, error) {
					got = req
					return &types.WikiPageListResponse{PageSize: req.PageSize}, nil
				},
			}

			w := httptest.NewRecorder()
			req := httptest.NewRequest(
				http.MethodGet, "/knowledgebase/kb1/wiki/pages"+tc.query, nil,
			)
			newWikiListTestRouter(wikiSvc).ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
			}
			if got == nil {
				t.Fatal("ListPages was not called")
			}
			if got.PageSize < 1 || got.PageSize > wikiListSizeUpperBound {
				t.Fatalf(
					"page_size must reach the service within [1, %d], got %d",
					wikiListSizeUpperBound, got.PageSize,
				)
			}
			if got.PageSize != tc.want {
				t.Fatalf("page_size = %d, want %d", got.PageSize, tc.want)
			}
		})
	}
}
