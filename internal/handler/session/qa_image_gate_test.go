package session

import (
	"bytes"
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type imageGateSearchStub struct {
	interfaces.SessionService
	calls int
}

func (s *imageGateSearchStub) SearchKnowledge(
	_ context.Context, _ []string, _ []string, _ []types.TagScope, _ string, _ *types.KnowledgeSearchOptions,
) (*types.RetrievalResult, error) {
	s.calls++
	return &types.RetrievalResult{}, nil
}

func serveImageGateSearch(t *testing.T, h *Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	r.POST("/knowledge-search", h.SearchKnowledge)
	req := httptest.NewRequest(http.MethodPost, "/knowledge-search", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func imageGateHandler() (*Handler, *imageGateSearchStub) {
	stub := &imageGateSearchStub{}
	return &Handler{sessionService: stub, fileService: &stubResourceFileService{}}, stub
}

// A whitespace-only query with no image ran a full search for nothing: the
// old gate compared the raw string against "" and called whitespace text.
func TestSearchKnowledgeRejectsWhitespaceOnlyQuery(t *testing.T) {
	h, stub := imageGateHandler()
	w := serveImageGateSearch(t, h, `{"query":"   ","knowledge_base_ids":["kb-1"]}`)

	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "Query content cannot be empty")
	assert.Zero(t, stub.calls, "nothing should reach the search service")
}

// An image query with vector match disabled can never return a result: recall
// runs in the image vector space, which the request just switched off.
func TestSearchKnowledgeRejectsImageWithoutVectorMatch(t *testing.T) {
	pngB64 := base64.StdEncoding.EncodeToString(samplePNG(t))
	h, stub := imageGateHandler()
	w := serveImageGateSearch(t, h,
		`{"query":"diagram","query_image":"`+pngB64+`","disable_vector_match":true,"knowledge_base_ids":["kb-1"]}`)

	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "disable_vector_match")
	assert.Zero(t, stub.calls, "nothing should reach the search service")
}

// The gate must not over-reject: a whitespace query beside a real image is an
// image-only search, and a text query beside vector match is a keyword search.
func TestSearchKnowledgeGateAcceptsViableRequests(t *testing.T) {
	pngB64 := base64.StdEncoding.EncodeToString(samplePNG(t))
	h, stub := imageGateHandler()

	w := serveImageGateSearch(t, h,
		`{"query":"   ","query_image":"`+pngB64+`","knowledge_base_ids":["kb-1"]}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, 1, stub.calls)

	w = serveImageGateSearch(t, h, `{"query":"diagram","disable_vector_match":true,"knowledge_base_ids":["kb-1"]}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, 2, stub.calls)
}
