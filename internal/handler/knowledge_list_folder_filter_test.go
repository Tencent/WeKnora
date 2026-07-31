package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const knowledgeListFolderFilterUUID = "10000000-0000-4000-8000-000000000001"

type knowledgeListFolderFilterServiceStub struct {
	interfaces.KnowledgeService

	filters []types.KnowledgeListFilter
}

func (s *knowledgeListFolderFilterServiceStub) ListPagedKnowledgeByKnowledgeBaseID(
	_ context.Context,
	_ string,
	page *types.Pagination,
	filter types.KnowledgeListFilter,
) (*types.PageResult, error) {
	s.filters = append(s.filters, filter)
	return types.NewPageResult(0, page, []*types.Knowledge{}), nil
}

type knowledgeListFolderFilterKBServiceStub struct {
	interfaces.KnowledgeBaseService
}

func (s *knowledgeListFolderFilterKBServiceStub) GetKnowledgeBaseByID(
	_ context.Context,
	id string,
) (*types.KnowledgeBase, error) {
	return &types.KnowledgeBase{ID: id, TenantID: 1}, nil
}

func TestListKnowledgePreservesFolderIDQueryThreeStates(t *testing.T) {
	tests := []struct {
		name        string
		query       string
		wantPresent bool
		wantFolder  string
	}{
		{
			name: "omitted means no folder filter",
		},
		{
			name:        "explicit empty means root folder",
			query:       "?folder_id=",
			wantPresent: true,
		},
		{
			name:        "uuid means exact folder",
			query:       "?folder_id=" + knowledgeListFolderFilterUUID,
			wantPresent: true,
			wantFolder:  knowledgeListFolderFilterUUID,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			serviceStub := &knowledgeListFolderFilterServiceStub{}
			router := newKnowledgeListFolderFilterTestRouter(serviceStub)
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodGet,
				"/knowledge-bases/kb-1/knowledge"+test.query,
				nil,
			)

			router.ServeHTTP(recorder, request)

			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			require.Len(t, serviceStub.filters, 1)
			filter := serviceStub.filters[0]
			require.Equal(t, test.wantPresent, filter.FolderID != nil)
			if test.wantPresent {
				require.Equal(t, test.wantFolder, *filter.FolderID)
			}
		})
	}
}

func newKnowledgeListFolderFilterTestRouter(
	serviceStub interfaces.KnowledgeService,
) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.ErrorHandler())
	router.Use(func(c *gin.Context) {
		c.Set(types.TenantIDContextKey.String(), uint64(1))
		c.Next()
	})
	handler := &KnowledgeHandler{
		kgService: serviceStub,
		kbService: &knowledgeListFolderFilterKBServiceStub{},
	}
	router.GET("/knowledge-bases/:id/knowledge", handler.ListKnowledge)
	return router
}
