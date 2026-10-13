package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// stubGalleryChunkService records the knowledge ids one images-listing call
// received, so tests can assert how the GET cap and the POST body cap differ.
type stubGalleryChunkService struct {
	interfaces.ChunkService
	gotIDs []string
}

func (s *stubGalleryChunkService) ListImagesByKnowledgeBaseID(
	_ context.Context,
	_ string,
	page *types.Pagination,
	filter *types.ImageListFilter,
) (*types.PageResult, error) {
	s.gotIDs = filter.KnowledgeIDs
	return types.NewPageResult(0, page, []types.ImageAssetRow{}), nil
}

func newImagesRouter(chunks interfaces.ChunkService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	r.Use(func(c *gin.Context) {
		c.Set(types.TenantIDContextKey.String(), uint64(1))
		c.Set(types.UserIDContextKey.String(), "u-test")
		c.Next()
	})
	h := &KnowledgeHandler{kbService: &stubFolderKBService{}, chunkService: chunks}
	r.GET("/knowledge-bases/:id/images", h.ListImages)
	r.POST("/knowledge-bases/:id/images", h.ListImagesPost)
	return r
}

// commaIDs builds "id-0,id-1,..." of the requested size.
func commaIDs(n int) []string {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("0198c0de-0000-7000-8000-%012d", i)
	}
	return ids
}

func TestListImagesPost_KnowledgeIDBody(t *testing.T) {
	tests := []struct {
		name    string
		ids     int
		wantCap int
		wantHTT int
	}{
		{
			name:    "60 ids pass the POST body although the GET cap is 50",
			ids:     60,
			wantCap: 60,
			wantHTT: http.StatusOK,
		},
		{
			name:    "the body cap still guards abuse",
			ids:     maxKnowledgeIDFilterBody + 1,
			wantCap: 0,
			wantHTT: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &stubGalleryChunkService{}
			router := newImagesRouter(svc)
			ids := commaIDs(tt.ids)
			payload, _ := json.Marshal(map[string]any{"knowledge_ids": ids})
			req := httptest.NewRequest(http.MethodPost, "/knowledge-bases/kb-1/images",
				strings.NewReader(string(payload)))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			if w.Code != tt.wantHTT {
				t.Fatalf("status = %d, want %d; body=%s", w.Code, tt.wantHTT, w.Body.String())
			}
			if len(svc.gotIDs) != tt.wantCap {
				t.Fatalf("got %d knowledge ids, want %d", len(svc.gotIDs), tt.wantCap)
			}
		})
	}
}

func TestListImagesPost_EmptyBody(t *testing.T) {
	svc := &stubGalleryChunkService{}
	router := newImagesRouter(svc)
	req := httptest.NewRequest(http.MethodPost, "/knowledge-bases/kb-1/images", nil)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if len(svc.gotIDs) != 0 {
		t.Fatalf("got %d knowledge ids, want none", len(svc.gotIDs))
	}
}

func TestListImagesGet_KnowledgeIDCap(t *testing.T) {
	svc := &stubGalleryChunkService{}
	router := newImagesRouter(svc)
	// 60 ids on the URL: the GET cap of 50 truncates them.
	url := "/knowledge-bases/kb-1/images?knowledge_ids=" + strings.Join(commaIDs(60), ",")
	req := httptest.NewRequest(http.MethodGet, url, nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if len(svc.gotIDs) != maxKnowledgeIDFilter {
		t.Fatalf("got %d knowledge ids, want the GET cap %d", len(svc.gotIDs), maxKnowledgeIDFilter)
	}
}
