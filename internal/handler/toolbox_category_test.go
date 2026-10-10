package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
)

type fakeToolboxCategoryService struct {
	replacedIDs []string
}

func (*fakeToolboxCategoryService) List(context.Context, uint64) ([]*types.ToolboxCategory, error) {
	return nil, nil
}

func (*fakeToolboxCategoryService) Create(
	context.Context, uint64, string,
) (*types.ToolboxCategory, error) {
	return nil, nil
}

func (*fakeToolboxCategoryService) Update(
	context.Context, uint64, string, string,
) (*types.ToolboxCategory, error) {
	return nil, nil
}

func (*fakeToolboxCategoryService) Delete(context.Context, uint64, string) error { return nil }

func (*fakeToolboxCategoryService) ListByResourceIDs(
	context.Context, uint64, string, []string,
) (map[string][]types.ToolboxCategory, error) {
	return nil, nil
}

func (f *fakeToolboxCategoryService) ReplaceSkillCategories(
	_ context.Context, _ uint64, _ string, categoryIDs []string,
) ([]types.ToolboxCategory, error) {
	f.replacedIDs = append(make([]string, 0, len(categoryIDs)), categoryIDs...)
	return []types.ToolboxCategory{}, nil
}

func (f *fakeToolboxCategoryService) ReplaceMCPServiceCategories(
	_ context.Context, _ uint64, _ string, categoryIDs []string,
) ([]types.ToolboxCategory, error) {
	f.replacedIDs = append(make([]string, 0, len(categoryIDs)), categoryIDs...)
	return []types.ToolboxCategory{}, nil
}

func newToolboxCategoryRouter(service *fakeToolboxCategoryService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.ErrorHandler())
	router.Use(func(c *gin.Context) {
		c.Set(types.TenantIDContextKey.String(), uint64(7))
		c.Next()
	})
	h := NewToolboxCategoryHandler(service)
	router.PUT("/skills/:id/categories", h.ReplaceSkillCategories)
	return router
}

func TestReplaceToolboxCategoriesRequiresTheCategoryIDsField(t *testing.T) {
	service := &fakeToolboxCategoryService{}
	router := newToolboxCategoryRouter(service)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPut, "/skills/skill-a/categories", strings.NewReader(`{}`),
	)
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Nil(t, service.replacedIDs)
}

func TestReplaceToolboxCategoriesAcceptsAnExplicitEmptyList(t *testing.T) {
	service := &fakeToolboxCategoryService{}
	router := newToolboxCategoryRouter(service)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPut, "/skills/skill-a/categories", strings.NewReader(`{"category_ids":[]}`),
	)
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Empty(t, service.replacedIDs)
	require.NotNil(t, service.replacedIDs)
}
