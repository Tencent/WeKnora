package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubWikiPageWriteService struct {
	interfaces.WikiPageService
	page    *types.WikiPage
	created *types.WikiPage
	updated *types.WikiPage
}

func (s *stubWikiPageWriteService) GetPageBySlug(context.Context, string, string) (*types.WikiPage, error) {
	return s.page, nil
}

func (s *stubWikiPageWriteService) CreatePage(_ context.Context, page *types.WikiPage) (*types.WikiPage, error) {
	s.created = page
	return page, nil
}

func (s *stubWikiPageWriteService) UpdatePage(_ context.Context, page *types.WikiPage) (*types.WikiPage, error) {
	s.updated = page
	return page, nil
}

type stubWikiSourceKnowledgeService struct {
	interfaces.KnowledgeService
	docs map[string]*types.Knowledge
	err  error
}

func (s *stubWikiSourceKnowledgeService) GetKnowledgeByIDOnly(
	_ context.Context, id string,
) (*types.Knowledge, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.docs[id], nil
}

func newWikiPageWriteTestRouter(h *WikiPageHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(types.TenantIDContextKey.String(), uint64(1))
		c.Next()
	})
	r.POST("/knowledgebase/:kb_id/wiki/pages", h.CreatePage)
	r.PUT("/knowledgebase/:kb_id/wiki/pages/*slug", h.UpdatePage)
	return r
}

func wikiPageWriteRequest(r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	return rec
}

func newWikiPageUpdateTestHandler() (*stubWikiPageWriteService, *gin.Engine) {
	svc := &stubWikiPageWriteService{page: &types.WikiPage{
		ID: "p-1", KnowledgeBaseID: "kb-1", Slug: "concept/a", Version: 3,
		SourceRefs: types.StringArray{"doc-1|real title"},
	}}
	h := &WikiPageHandler{wikiService: svc, kbService: &stubWikiKBService{}}
	return svc, newWikiPageWriteTestRouter(h)
}

// Issue #5188: a PUT carrying source_refs used to return 200 without changing
// anything, so callers believed the provenance write had succeeded.
func TestWikiPageUpdateRejectsUnsupportedFields(t *testing.T) {
	svc, r := newWikiPageUpdateTestHandler()
	rec := wikiPageWriteRequest(r, http.MethodPut,
		"/knowledgebase/kb-1/wiki/pages/concept/a",
		`{"content":"# changed","version":3,"source_refs":["doc-1|forged"]}`)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "source_refs")
	assert.Contains(t, rec.Body.String(), "PUT accepts only")
	assert.Nil(t, svc.updated)
}

func TestWikiPageUpdateAcceptsWhitelistedFields(t *testing.T) {
	svc, r := newWikiPageUpdateTestHandler()
	rec := wikiPageWriteRequest(r, http.MethodPut,
		"/knowledgebase/kb-1/wiki/pages/concept/a",
		`{"content":"# changed","version":3}`)

	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, svc.updated)
	assert.Equal(t, "# changed", svc.updated.Content)
	assert.Equal(t, types.StringArray{"doc-1|real title"}, svc.updated.SourceRefs)
}

func TestWikiPageUpdateRejectsTrailingJSON(t *testing.T) {
	svc, r := newWikiPageUpdateTestHandler()
	rec := wikiPageWriteRequest(r, http.MethodPut,
		"/knowledgebase/kb-1/wiki/pages/concept/a",
		`{"content":"# first"}{"content":"# second"}`)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Nil(t, svc.updated)
}

func TestWikiPageCreateValidatesSourceRefs(t *testing.T) {
	docs := map[string]*types.Knowledge{
		"doc-1": {ID: "doc-1", KnowledgeBaseID: "kb-1", Title: "Server title"},
		"doc-2": {ID: "doc-2", KnowledgeBaseID: "other-kb", Title: "Foreign document"},
	}

	cases := []struct {
		name       string
		body       string
		lookupErr  error
		wantStatus int
		wantRefs   types.StringArray
		wantCreate bool
	}{
		{
			name:       "valid refs are rebuilt from server data and deduplicated",
			body:       `{"slug":"p","title":"P","content":"x","source_refs":["doc-1|forged title","doc-1"]}`,
			wantStatus: http.StatusCreated,
			wantRefs:   types.StringArray{"doc-1|Server title"},
			wantCreate: true,
		},
		{
			name:       "no refs",
			body:       `{"slug":"p","content":"x"}`,
			wantStatus: http.StatusCreated,
			wantCreate: true,
		},
		{
			name:       "unknown document",
			body:       `{"slug":"p","source_refs":["missing"]}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "document from another knowledge base",
			body:       `{"slug":"p","source_refs":["doc-2"]}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "malformed ref",
			body:       `{"slug":"p","source_refs":["|title"]}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "lookup failure",
			body:       `{"slug":"p","source_refs":["doc-1"]}`,
			lookupErr:  errors.New("lookup failed"),
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := &stubWikiPageWriteService{}
			knowledgeSvc := &stubWikiSourceKnowledgeService{docs: docs, err: tc.lookupErr}
			h := &WikiPageHandler{
				wikiService:      svc,
				kbService:        &stubWikiKBService{},
				knowledgeService: knowledgeSvc,
			}
			rec := wikiPageWriteRequest(newWikiPageWriteTestRouter(h), http.MethodPost,
				"/knowledgebase/kb-1/wiki/pages", tc.body)

			require.Equal(t, tc.wantStatus, rec.Code, rec.Body.String())
			if !tc.wantCreate {
				assert.Nil(t, svc.created)
				return
			}
			require.NotNil(t, svc.created)
			assert.Equal(t, tc.wantRefs, svc.created.SourceRefs)
		})
	}
}
