package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
)

type invalidURLKnowledgeService struct {
	interfaces.KnowledgeService
}

func (s *invalidURLKnowledgeService) CreateKnowledgeFromURL(
	_ context.Context, _ string, _ string, _ string, _ string, _ *bool,
	_ string, _ []string, _ string, _ *types.KnowledgeProcessOverrides,
) (*types.Knowledge, error) {
	return nil, service.ErrInvalidURL
}

// POST /knowledge-bases/:id/knowledge/url forwards the service error unchanged.
// ErrInvalidURL is a client error, so a plain sentinel would be reported as
// HTTP 500 by the handler's catch-all branch (the same defect #5877 fixed for
// unsupported upload types).
func TestCreateKnowledgeFromURLRejectsInvalidURLWith400(t *testing.T) {
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(utils.ResetSSRFWhitelistForTest)

	h := &KnowledgeHandler{
		kgService: &invalidURLKnowledgeService{},
		kbService: &stubKBService{get: func(context.Context, string) (*types.KnowledgeBase, error) {
			return &types.KnowledgeBase{ID: "kb", TenantID: 7}, nil
		}},
	}
	r := documentHandlerRouter()
	r.POST("/:id/knowledge/url", h.CreateKnowledgeFromURL)

	req := httptest.NewRequest(http.MethodPost, "/kb/knowledge/url",
		strings.NewReader(`{"url":"http://127.0.0.1/doc"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "invalid URL")
}
