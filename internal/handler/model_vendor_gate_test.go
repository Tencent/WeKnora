package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	modelruntime "github.com/Tencent/WeKnora/internal/models/runtime"
	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/types"
)

// offVendorGate has the workspace turn off the plugin of one vendor, known
// by its registered (lowercase) ID only, as the plugin registry knows it.
type offVendorGate struct{}

func (offVendorGate) EnabledFilter(context.Context, uint64) func(manifest.Point, string) bool {
	return func(point manifest.Point, id string) bool {
		return point != manifest.PointModelVendors || id != "acme.ai/acme"
	}
}

func (g offVendorGate) CallFilter(ctx context.Context, tenantID uint64) func(manifest.Point, string) bool {
	return g.EnabledFilter(ctx, tenantID)
}

func registerOffVendor(t *testing.T) {
	t.Helper()
	if err := modelruntime.RegisterPlugin("acme.ai/acme",
		[]byte(`{"name":"ACME AI","base_url":"https://api.acme.example/v1","model_types":["rerank"]}`),
		t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { modelruntime.Unregister("acme.ai/acme") })
}

// sendModel drives POST /models, or PUT /models/{id} against stored, with
// the given provider and returns what the handler reported.
func sendModel(t *testing.T, stored *types.Model, provider string) (errs []string, updated *types.Model) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	svc := &stubUpdateModelService{stored: stored}
	h := NewModelHandler(svc)
	h.SetPluginGate(offVendorGate{})
	params := types.ModelParameters{Provider: provider}
	var body []byte
	var err error
	method, path := http.MethodPost, "/api/v1/models"
	if stored != nil {
		method, path = http.MethodPut, "/api/v1/models/"+stored.ID
		params.ExtraConfig = stored.Parameters.ExtraConfig
		body, err = json.Marshal(UpdateModelRequest{
			Name: stored.Name, Type: stored.Type, Source: stored.Source, Parameters: params,
		})
	} else {
		body, err = json.Marshal(CreateModelRequest{
			Name: "acme-rerank", Type: types.ModelTypeRerank, Source: types.ModelSourceRemote, Parameters: params,
		})
	}
	require.NoError(t, err)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, path, bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(c.Request.Context(), types.TenantRoleContextKey, types.TenantRoleAdmin)
	c.Request = c.Request.WithContext(context.WithValue(ctx, types.TenantIDContextKey, uint64(1)))
	c.Set(types.TenantIDContextKey.String(), uint64(1))
	if stored != nil {
		c.Params = gin.Params{{Key: "id", Value: stored.ID}}
		h.UpdateModel(c)
	} else {
		h.CreateModel(c)
	}
	return c.Errors.Errors(), svc.updated
}

// Creating a model on, or moving one to, a vendor whose plugin the workspace
// turned off is refused however the provider is spelled; a model already
// there keeps its other edits.
func TestModelsCannotTakeATurnedOffPluginVendor(t *testing.T) {
	registerOffVendor(t)
	registerSecretExtraVendor(t)
	for _, provider := range []string{"acme.ai/acme", "ACME.AI/Acme", " acme.ai/acme "} {
		errs, _ := sendModel(t, nil, provider)
		assert.Contains(t, strings.Join(errs, "\n"), disabledIntegrationError, "create with %q", provider)
		errs, updated := sendModel(t, storedSecretExtraModel(), provider)
		assert.Contains(t, strings.Join(errs, "\n"), disabledIntegrationError, "move to %q", provider)
		assert.Nil(t, updated)
	}

	onAcme := storedSecretExtraModel()
	onAcme.Parameters.Provider = "acme.ai/acme"
	errs, updated := sendModel(t, onAcme, "ACME.AI/acme")
	assert.Empty(t, errs, "an edit that keeps the vendor")
	assert.NotNil(t, updated)
}
