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

func TestPatchModelRejectsUnsupportedFields(t *testing.T) {
	for _, tt := range []struct {
		name  string
		body  string
		field string
	}{
		{
			name:  "parameters with display name",
			body:  `{"display_name":"Updated","parameters":{"base_url":"https://new.example.com/v1","interface_type":"openai"}}`,
			field: "parameters",
		},
		{
			name:  "parameters with description",
			body:  `{"description":"Updated","parameters":{"interface_type":"openai"}}`,
			field: "parameters",
		},
		{
			name:  "parameters only",
			body:  `{"parameters":{"base_url":"https://new.example.com/v1"}}`,
			field: "parameters",
		},
		{
			name:  "null parameters with display name",
			body:  `{"display_name":"Updated","parameters":null}`,
			field: "parameters",
		},
		{
			name:  "unknown field with display name",
			body:  `{"display_name":"Updated","base_url":"https://new.example.com/v1"}`,
			field: "base_url",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			stub := &stubPatchModelService{model: &types.Model{
				ID: "m1", DisplayName: "Original", Description: "Original description",
				Parameters: types.ModelParameters{
					BaseURL: "https://old.example.com/v1", APIKey: "stored-secret",
				},
			}}
			rec := patchModel(t, stub, tt.body)
			require.Equal(t, http.StatusBadRequest, rec.Code, "unsupported PATCH fields must not be silently ignored: %s", rec.Body.String())
			assert.Contains(t, rec.Body.String(), `unknown field \"`+tt.field+`\"`)
			assert.Nil(t, stub.updated, "invalid requests must not persist partial edits")
			assert.Equal(t, "Original", stub.model.DisplayName)
			assert.Equal(t, "https://old.example.com/v1", stub.model.Parameters.BaseURL)
			assert.NotContains(t, rec.Body.String(), "stored-secret")
		})
	}
}
