package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/providers"
	modelruntime "github.com/Tencent/WeKnora/internal/models/runtime"
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

const (
	secretExtraProvider = "handler-secret-extra-vendor"
	// plainExtraProvider declares no secret extra field at all — the vendor a
	// row is switched *to* in the provider-change tests below.
	plainExtraProvider = "handler-plain-extra-vendor"
)

func registerSecretExtraVendor(t *testing.T) {
	t.Helper()
	modelruntime.Register(&providers.Definition{
		ID:         secretExtraProvider,
		Name:       "Secret Extra Vendor",
		ModelTypes: []types.ModelType{types.ModelTypeRerank},
		ExtraFields: []providers.ExtraField{
			{Key: "secret_key", Label: "Secret Key", Type: "password", Secret: true},
			{Key: "region", Label: "Region", Type: "string"},
		},
	})
	modelruntime.Register(&providers.Definition{
		ID:         plainExtraProvider,
		Name:       "Plain Extra Vendor",
		ModelTypes: []types.ModelType{types.ModelTypeRerank},
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

// putModelAs drives PUT /models/{id} against the stored rerank row with the
// given provider and extra_config, as a workspace admin (the only role whose
// response carries integration detail at all). It returns the row the handler
// persisted and the response body.
func putModelAs(t *testing.T, provider string, extraConfig map[string]string) (*types.Model, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	svc := &stubUpdateModelService{stored: storedSecretExtraModel()}
	h := NewModelHandler(svc)

	body, err := json.Marshal(UpdateModelRequest{
		Name:   "rerank-v1",
		Type:   types.ModelTypeRerank,
		Source: types.ModelSourceRemote,
		Parameters: types.ModelParameters{
			Provider:    provider,
			ExtraConfig: extraConfig,
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
	require.Empty(t, c.Errors.Errors(), "handler reported an error")
	require.Equal(t, http.StatusOK, w.Code)
	require.NotNil(t, svc.updated, "UpdateModel was never called")
	return svc.updated, w.Body.String()
}

// putModel keeps the row on its own vendor, the ordinary edit.
func putModel(t *testing.T, extraConfig map[string]string) *types.Model {
	t.Helper()
	updated, _ := putModelAs(t, secretExtraProvider, extraConfig)
	return updated
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
	_, body := putModelAs(t, secretExtraProvider, map[string]string{"secret_key": "cam-secret-rotated"})
	assert.NotContains(t, body, "cam-secret-rotated")
	assert.Contains(t, body, `"secret_key":{"configured":true}`)
}

// Switching the row to a vendor that declares no secret field used to write
// the previous vendor's CAM/IAM key back into the row — the merge looked at
// the stored provider while the redactor looked at the new one, so the key
// came back in plaintext in this response and in every later GET.
func TestUpdateModel_ProviderChangeDoesNotEchoOldSecretExtra(t *testing.T) {
	registerSecretExtraVendor(t)
	updated, body := putModelAs(t, plainExtraProvider, map[string]string{"api": "openai_completions"})

	assert.NotContains(t, updated.Parameters.ExtraConfig, "secret_key",
		"the previous vendor's credential must not survive the move")
	assert.Equal(t, "openai_completions", updated.Parameters.ExtraConfig["api"],
		"the edit the user actually made still lands")
	assert.NotContains(t, body, "cam-secret-stored")
}

// Even a row that somehow still carries the key — hand-edited provider, or a
// provider that was never stored — must not have it echoed: redaction asks
// every registered vendor, not just this row's.
func TestUpdateModel_ResponseRedactsSecretExtraOfAnotherVendor(t *testing.T) {
	registerSecretExtraVendor(t)
	_, body := putModelAs(t, plainExtraProvider, map[string]string{"secret_key": "cam-secret-typed"})
	assert.NotContains(t, body, "cam-secret-typed")
	assert.Contains(t, body, `"secret_key":{"configured":true}`)
}

// A body that names no vendor at all is not a vendor change: it carries no
// new integration identity, and the stored secret still has to survive it.
func TestUpdateModel_KeepsSecretExtraWhenRequestNamesNoProvider(t *testing.T) {
	registerSecretExtraVendor(t)
	updated, _ := putModelAs(t, "", map[string]string{"region": "ap-beijing"})
	assert.Equal(t, "cam-secret-stored", updated.Parameters.ExtraConfig["secret_key"])
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

// Testing the row against a different vendor must not reach for the stored
// credential: it authenticates the integration being replaced.
func TestFillSecretsFromStoredModel_SkipsSecretExtraOfAnotherVendor(t *testing.T) {
	registerSecretExtraVendor(t)
	stored := storedSecretExtraModel()
	h := &InitializationHandler{
		modelService: &stubFillSecretsModelService{
			getModelByID: func(context.Context, string) (*types.Model, error) { return stored, nil },
		},
	}
	req := &ModelTestRequest{
		ModelID:     "m-rerank",
		Provider:    plainExtraProvider,
		ExtraConfig: map[string]string{"api": "openai_completions"},
	}

	h.fillSecretsFromStoredModel(context.Background(), req)

	assert.NotContains(t, req.ExtraConfig, "secret_key")
	assert.Equal(t, "AKID-public-id", req.APIKey, "the api_key still comes from the stored row")
}
