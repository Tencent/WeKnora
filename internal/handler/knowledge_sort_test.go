package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestListKnowledgeSortQuery(t *testing.T) {
	tests := []struct {
		name      string
		query     string
		wantCode  int
		wantBy    types.KnowledgeListSortField
		wantOrder types.KnowledgeListSortOrder
	}{
		{
			name:      "未传参数时默认最近更新",
			wantCode:  http.StatusOK,
			wantBy:    types.KnowledgeListSortByUpdatedAt,
			wantOrder: types.KnowledgeListSortDescending,
		},
		{
			name:      "接受创建时间升序",
			query:     "?sort_by=created_at&sort_order=asc",
			wantCode:  http.StatusOK,
			wantBy:    types.KnowledgeListSortByCreatedAt,
			wantOrder: types.KnowledgeListSortAscending,
		},
		{
			name:      "接受文件名称降序",
			query:     "?sort_by=file_name&sort_order=desc",
			wantCode:  http.StatusOK,
			wantBy:    types.KnowledgeListSortByFileName,
			wantOrder: types.KnowledgeListSortDescending,
		},
		{
			name:     "拒绝未知排序字段",
			query:    "?sort_by=deleted_at&sort_order=desc",
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "拒绝未知排序方向",
			query:    "?sort_by=updated_at&sort_order=random",
			wantCode: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kg := &stubFolderKGService{}
			router := newFolderRouter(kg)
			req := httptest.NewRequest(http.MethodGet, "/knowledge-bases/kb-1/knowledge"+tt.query, nil)
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			if w.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d; body=%s", w.Code, tt.wantCode, w.Body.String())
			}
			if tt.wantCode == http.StatusOK {
				if kg.gotFilter.SortBy != tt.wantBy {
					t.Fatalf("sort_by = %q, want %q", kg.gotFilter.SortBy, tt.wantBy)
				}
				if kg.gotFilter.SortOrder != tt.wantOrder {
					t.Fatalf("sort_order = %q, want %q", kg.gotFilter.SortOrder, tt.wantOrder)
				}
			}
		})
	}
}
