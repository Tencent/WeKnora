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
)

type previewHandlerService struct {
	interfaces.DataSourceService
	called bool
}

func (*previewHandlerService) GetDataSource(context.Context, string) (*types.DataSource, error) {
	return &types.DataSource{ID: "one", KnowledgeBaseID: "kb"}, nil
}

func (s *previewHandlerService) PreviewAvailableResources(
	_ context.Context, _ string, parent string, cfg *types.DataSourceConfig,
) ([]types.Resource, error) {
	s.called = true
	if parent != "parent" || cfg.Credentials["token"] != "synthetic-secret" {
		return nil, errors.New("wrong preview")
	}
	return nil, errors.New("synthetic-secret")
}

func (s *previewHandlerService) PreviewResourceAncestors(
	_ context.Context, _ string, ids []string, cfg *types.DataSourceConfig,
) ([]string, error) {
	s.called = true
	if len(ids) != 1 || ids[0] != "child" || cfg.Credentials["token"] != "synthetic-secret" {
		return nil, errors.New("wrong preview")
	}
	return []string{"parent"}, nil
}

func TestDataSourcePreviewAuthorizationAndRedaction(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resourceBody := `{"parent_id":"parent","config":{"credentials":{"token":"synthetic-secret"}}}`
	ancestorBody := `{"resource_ids":["child"],"config":{"credentials":{"token":"synthetic-secret"}}}`
	for _, tc := range []struct {
		path, body string
		tenant     uint64
		want       int
		called     bool
	}{
		{"resources/preview", resourceBody, 0, 401, false},
		{"resources/preview", resourceBody, 2, 403, false},
		{"resources/preview", resourceBody, 1, 400, true},
		{"resource-ancestors/preview", ancestorBody, 1, 200, true},
	} {
		svc := &previewHandlerService{}
		kb := &stubKBServiceForDS{getByID: func(context.Context, string) (*types.KnowledgeBase, error) {
			return &types.KnowledgeBase{TenantID: 1}, nil
		}}
		h := NewDataSourceHandler(svc, kb)
		r := gin.New()
		r.Use(func(c *gin.Context) { c.Set(types.TenantIDContextKey.String(), tc.tenant); c.Next() })
		r.POST("/datasource/:id/resources/preview", h.PreviewAvailableResources)
		r.POST("/datasource/:id/resource-ancestors/preview", h.PreviewResourceAncestors)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/datasource/one/"+tc.path, strings.NewReader(tc.body)))
		if w.Code != tc.want || svc.called != tc.called || strings.Contains(w.Body.String(), "synthetic-secret") {
			t.Fatalf("%s tenant %d: status %d called %v body %s",
				tc.path, tc.tenant, w.Code, svc.called, w.Body.String())
		}
	}
}
