package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

// pageGalleryService 记录经过权限和 contract 约束后的参数，不模拟数据库排序。
type pageGalleryService struct {
	interfaces.ChunkService
	filter *types.ImageListFilter
}

func (s *pageGalleryService) ListImagesByKnowledgeBaseID(
	_ context.Context,
	_ string,
	page *types.Pagination,
	filter *types.ImageListFilter,
) (*types.PageResult, error) {
	s.filter = filter
	return types.NewPageResult(0, page, []types.ImageAsset{}), nil
}

func TestListImages_两种请求都保留页码排序且默认契约不变(t *testing.T) {
	for _, tc := range []struct{ name, method, url, body, sort, order string }{
		{
			"GET页码升序", http.MethodGet,
			"/knowledge-bases/kb-1/images?sort_by=builtin:page&sort_order=asc", "", "builtin:page", "asc",
		},
		{
			"POST来源筛选与页码升序", http.MethodPost, "/knowledge-bases/kb-1/images",
			`{"knowledge_ids":["doc-a"],"sort_by":"builtin:page","sort_order":"asc"}`, "builtin:page", "asc",
		},
		{"GET未指定排序", http.MethodGet, "/knowledge-bases/kb-1/images", "", "", "desc"},
		{"POST未指定排序", http.MethodPost, "/knowledge-bases/kb-1/images", `{}`, "", "desc"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &pageGalleryService{}
			req := httptest.NewRequest(tc.method, tc.url, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			res := httptest.NewRecorder()
			newImagesRouter(svc).ServeHTTP(res, req)
			require.Equal(t, http.StatusOK, res.Code, "请求失败：%s", res.Body.String())
			require.NotNil(t, svc.filter, "应调用图片服务")
			require.Equal(t, tc.sort, svc.filter.SortBy, "页码字段必须通过图库contract检查")
			require.Equal(t, tc.order, svc.filter.SortOrder, "没有参数时仍采用旧的降序默认值")
		})
	}
}
