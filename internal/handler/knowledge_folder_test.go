package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type stubFolderKBService struct {
	interfaces.KnowledgeBaseService
}

func (s *stubFolderKBService) GetKnowledgeBaseByID(_ context.Context, id string) (*types.KnowledgeBase, error) {
	if id != "kb-1" {
		return nil, errors.New("kb not found")
	}
	return &types.KnowledgeBase{ID: "kb-1", TenantID: 1, Type: types.KnowledgeBaseTypeDocument}, nil
}

type stubFolderKGService struct {
	interfaces.KnowledgeService

	gotFilter types.KnowledgeListFilter
	tree      *types.KnowledgeFolderTree
	treeErr   error
}

func (s *stubFolderKGService) ListPagedKnowledgeByKnowledgeBaseID(
	_ context.Context,
	_ string,
	page *types.Pagination,
	filter types.KnowledgeListFilter,
) (*types.PageResult, error) {
	s.gotFilter = filter
	return types.NewPageResult(0, page, []*types.Knowledge{}), nil
}

func (s *stubFolderKGService) ListKnowledgeFolderTree(
	_ context.Context,
	_ string,
) (*types.KnowledgeFolderTree, error) {
	return s.tree, s.treeErr
}

func newFolderRouter(kg interfaces.KnowledgeService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	r.Use(func(c *gin.Context) {
		c.Set(types.TenantIDContextKey.String(), uint64(1))
		c.Set(types.UserIDContextKey.String(), "u-test")
		c.Next()
	})
	h := &KnowledgeHandler{kbService: &stubFolderKBService{}, kgService: kg}
	r.GET("/knowledge-bases/:id/knowledge", h.ListKnowledge)
	r.GET("/knowledge-bases/:id/knowledge/folders", h.ListKnowledgeFolders)
	return r
}

// The folder dimension is keyed off parameter presence, because an empty
// folder_path is a meaningful value (the knowledge base root) and must be
// distinguishable from "browse every folder".
func TestListKnowledge_FolderScopeFromQuery(t *testing.T) {
	tests := []struct {
		name      string
		query     string
		wantPath  string
		wantScope types.KnowledgeFolderScope
	}{
		{"absent parameter keeps the flat listing", "", "", types.FolderScopeAny},
		{"empty parameter selects the root", "?folder_path=", "", types.FolderScopeExact},
		{"folder selects that folder only", "?folder_path=docs/spec", "docs/spec", types.FolderScopeExact},
		{"recursive selects the subtree", "?folder_path=docs&folder_recursive=true", "docs", types.FolderScopeSubtree},
		{"recursive false stays exact", "?folder_path=docs&folder_recursive=false", "docs", types.FolderScopeExact},
		{"path is normalized", "?folder_path=/docs//spec/", "docs/spec", types.FolderScopeExact},
		{"traversal cannot escape the root", "?folder_path=../../spec", "spec", types.FolderScopeExact},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kg := &stubFolderKGService{}
			router := newFolderRouter(kg)

			req := httptest.NewRequest(http.MethodGet, "/knowledge-bases/kb-1/knowledge"+tt.query, nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
			}
			if kg.gotFilter.FolderPath != tt.wantPath {
				t.Fatalf("folder path = %q, want %q", kg.gotFilter.FolderPath, tt.wantPath)
			}
			if kg.gotFilter.FolderScope != tt.wantScope {
				t.Fatalf("folder scope = %q, want %q", kg.gotFilter.FolderScope, tt.wantScope)
			}
		})
	}
}

func TestListKnowledgeFolders_ReturnsTree(t *testing.T) {
	kg := &stubFolderKGService{
		tree: types.BuildKnowledgeFolderTree([]*types.KnowledgeFolderCount{
			{FolderPath: "", Count: 1},
			{FolderPath: "docs/spec", Count: 2},
		}),
	}
	router := newFolderRouter(kg)

	req := httptest.NewRequest(http.MethodGet, "/knowledge-bases/kb-1/knowledge/folders", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}

	var body struct {
		Success bool                      `json:"success"`
		Data    types.KnowledgeFolderTree `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v; body=%s", err, w.Body.String())
	}
	if !body.Success {
		t.Fatalf("success = false; body=%s", w.Body.String())
	}
	if body.Data.RootDocumentCount != 1 || body.Data.TotalDocumentCount != 3 {
		t.Fatalf("counts = root %d total %d, want 1 / 3",
			body.Data.RootDocumentCount, body.Data.TotalDocumentCount)
	}
	if len(body.Data.Folders) != 1 || body.Data.Folders[0].Path != "docs" {
		t.Fatalf("folders = %+v, want a single \"docs\" root folder", body.Data.Folders)
	}
	if len(body.Data.Folders[0].Children) != 1 || body.Data.Folders[0].Children[0].Path != "docs/spec" {
		t.Fatalf("children = %+v, want \"docs/spec\"", body.Data.Folders[0].Children)
	}
}
