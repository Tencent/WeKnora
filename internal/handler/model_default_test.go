package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type defaultModelViewService struct {
	interfaces.ModelService
	stored *types.Model
}

func (s *defaultModelViewService) GetModelByID(context.Context, string) (*types.Model, error) {
	return s.stored, nil
}

func (s *defaultModelViewService) ListModels(context.Context) ([]*types.Model, error) {
	return []*types.Model{{ID: s.stored.ID, IsDefault: false}}, nil
}

func TestModelDefaultDetailUsesEffectiveWorkspaceSelection(t *testing.T) {
	stub := &defaultModelViewService{stored: &types.Model{ID: "builtin", IsBuiltin: true, IsDefault: true}}
	router := gin.New()
	router.GET("/models/:id", NewModelHandler(stub).GetModel)
	req := httptest.NewRequest(http.MethodGet, "/models/builtin", nil)
	req = req.WithContext(context.WithValue(req.Context(), types.TenantIDContextKey, uint64(7)))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"is_default":false`)
	require.True(t, stub.stored.IsDefault, "the legacy/shared flag is preserved")
}
