package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/catalog"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubUpdateModelService serves one stored model and records the row the
// handler tried to persist.
type stubUpdateModelService struct {
	interfaces.ModelService
	stored  *types.Model
	updated *types.Model
}

func (s *stubUpdateModelService) GetModelByID(context.Context, string) (*types.Model, error) {
	return s.stored, nil
}

func (s *stubUpdateModelService) UpdateModel(_ context.Context, m *types.Model) error {
	copied := *m
	s.updated = &copied
	return nil
}

const secretExtraProvider = "handler-secret-extra-vendor"

func registerSecretExtraVendor(t *testing.T) {
	t.Helper()
	catalog.Register(&catalog.Vendor{
		ID:         secretExtraProvider,
		Name:       "Secret Extra Vendor",
		ModelTypes: []types.ModelType{types.ModelTypeRerank},
		ExtraFields: []catalog.ExtraField{
			{Key: "secret_key", Label: "Secret Key", Type: "password", Secret: true},
			{Key: "region", Label: "Region", Type: "string"},
		},
	})
}

func storedSecretExtraModel() *types.Model {
	return &types.Model{
		ID:     "m-rerank",
		Name:   "rerank-v1",
		Type:   types.ModelTypeRerank,
		Source: types.ModelSourceRemote,
		Parameters: types.ModelParameters{
			Provider: secretExtraProvider,
			APIKey:   "AKID-public-id",
			ExtraConfig: map[string]string{
				"secret_key": "cam-secret-stored",
				"region":     "ap-guangzhou",
			},
		},
	}
}

// putModel drives PUT /models/{id} with the given extra_config and returns
// the row the handler persisted.
func putModel(t *testing.T, extraConfig map[string]string) *types.Model {
	t.Helper()
	gin.SetMode(gin.TestMode)
	svc := &stubUpdateModelService{stored: storedSecretExtraModel()}
	h := NewModelHandler(svc)

	body, err := json.Marshal(UpdateModelRequest{
		Name:   "rerank-v1",
		Type:   types.ModelTypeRerank,
		Source: types.ModelSourceRemote,
		Parameters: types.ModelParameters{
			Provider:    secretExtraProvider,
			ExtraConfig: extraConfig,
		},
	})
	require.NoError(t, err)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/models/m-rerank", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "id", Value: "m-rerank"}}

	h.UpdateModel(c)
	require.Empty(t, c.Errors.Errors(), "handler reported an error")
	require.Equal(t, http.StatusOK, w.Code)
	require.NotNil(t, svc.updated, "UpdateModel was never called")
	return svc.updated
}

// The frontend always sends extra_config for remote rows, but GET redacts
// the vendor's secret fields — so the map it sends back carries no
// secret_key. Saving must not wipe the stored one.
func TestUpdateModel_KeepsStoredSecretExtraWhenRequestOmitsIt(t *testing.T) {
	registerSecretExtraVendor(t)
	updated := putModel(t, map[string]string{"region": "ap-beijing"})
	assert.Equal(t, "cam-secret-stored", updated.Parameters.ExtraConfig["secret_key"])
	assert.Equal(t, "ap-beijing", updated.Parameters.ExtraConfig["region"],
		"the edit the user actually made still lands")
}

func TestUpdateModel_KeepsStoredSecretExtraWhenRequestSendsBlank(t *testing.T) {
	registerSecretExtraVendor(t)
	updated := putModel(t, map[string]string{"secret_key": "", "region": "ap-guangzhou"})
	assert.Equal(t, "cam-secret-stored", updated.Parameters.ExtraConfig["secret_key"])
}

func TestUpdateModel_ReplacesSecretExtraWhenRequestSendsNewValue(t *testing.T) {
	registerSecretExtraVendor(t)
	updated := putModel(t, map[string]string{"secret_key": "cam-secret-rotated", "region": "ap-guangzhou"})
	assert.Equal(t, "cam-secret-rotated", updated.Parameters.ExtraConfig["secret_key"])
}

// The PUT response goes through the same redaction as GET.
func TestUpdateModel_ResponseDoesNotEchoSecretExtra(t *testing.T) {
	registerSecretExtraVendor(t)
	gin.SetMode(gin.TestMode)
	svc := &stubUpdateModelService{stored: storedSecretExtraModel()}
	h := NewModelHandler(svc)

	body, err := json.Marshal(UpdateModelRequest{
		Name: "rerank-v1", Type: types.ModelTypeRerank, Source: types.ModelSourceRemote,
		Parameters: types.ModelParameters{
			Provider:    secretExtraProvider,
			ExtraConfig: map[string]string{"secret_key": "cam-secret-rotated"},
		},
	})
	require.NoError(t, err)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/models/m-rerank", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request = c.Request.WithContext(
		context.WithValue(c.Request.Context(), types.TenantRoleContextKey, types.TenantRoleAdmin),
	)
	c.Params = gin.Params{{Key: "id", Value: "m-rerank"}}

	h.UpdateModel(c)
	require.Equal(t, http.StatusOK, w.Code)
	assert.NotContains(t, w.Body.String(), "cam-secret-rotated")
	assert.Contains(t, w.Body.String(), `"secret_key":{"configured":true}`)
}

// The "test connection" path must see the secret the UI never received,
// otherwise verifying an existing rerank row fails with a missing key.
func TestFillSecretsFromStoredModel_FillsRedactedSecretExtra(t *testing.T) {
	registerSecretExtraVendor(t)
	stored := storedSecretExtraModel()
	h := &InitializationHandler{
		modelService: &stubFillSecretsModelService{
			getModelByID: func(context.Context, string) (*types.Model, error) { return stored, nil },
		},
	}
	req := &ModelTestRequest{
		ModelID:     "m-rerank",
		Provider:    secretExtraProvider,
		APIKey:      "AKID-typed-by-user",
		AppSecret:   "unused",
		ExtraConfig: map[string]string{"region": "ap-beijing"},
	}

	h.fillSecretsFromStoredModel(context.Background(), req)

	assert.Equal(t, "cam-secret-stored", req.ExtraConfig["secret_key"])
	assert.Equal(t, "ap-beijing", req.ExtraConfig["region"])
	assert.Equal(t, "AKID-typed-by-user", req.APIKey, "a value the user typed always wins")
}
