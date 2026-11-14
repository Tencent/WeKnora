package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubPatchModelService struct {
	interfaces.ModelService
	model   *types.Model
	updated *types.Model
}

func (s *stubPatchModelService) GetModelByID(context.Context, string) (*types.Model, error) {
	copied := *s.model
	return &copied, nil
}

func (s *stubPatchModelService) UpdateModel(_ context.Context, model *types.Model) error {
	s.updated = model
	return nil
}

func patchModel(t *testing.T, stub *stubPatchModelService, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.ErrorHandler())
	router.PATCH("/models/:id", NewModelHandler(stub).PatchModel)
	req := httptest.NewRequest(http.MethodPatch, "/models/m1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestPatchModelChangesOnlyTheDisplayFields(t *testing.T) {
	stub := &stubPatchModelService{model: &types.Model{
		ID: "m1", Name: "gpt-4o", DisplayName: "old", Description: "keep",
		Type: types.ModelTypeKnowledgeQA, Source: types.ModelSourceRemote,
		Parameters: types.ModelParameters{BaseURL: "https://api.example.com/v1", APIKey: "sk-live"},
	}}
	rec := patchModel(t, stub, `{"display_name":"  Production GPT  "}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NotNil(t, stub.updated)
	assert.Equal(t, "Production GPT", stub.updated.DisplayName)
	assert.Equal(t, "keep", stub.updated.Description)
	assert.Equal(t, "https://api.example.com/v1", stub.updated.Parameters.BaseURL)
	assert.Equal(t, "sk-live", stub.updated.Parameters.APIKey, "the stored key is saved back unchanged")
	assert.NotContains(t, rec.Body.String(), "sk-live")

	rec = patchModel(t, stub, `{"description":""}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "old", stub.updated.DisplayName)
	assert.Empty(t, stub.updated.Description)
}

func TestPatchModelNeedsAField(t *testing.T) {
	stub := &stubPatchModelService{model: &types.Model{ID: "m1"}}
	rec := patchModel(t, stub, `{}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Nil(t, stub.updated)
}
