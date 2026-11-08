package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type defaultSettingsModelsService struct {
	interfaces.ModelService
	models []*types.Model
}

func (s *defaultSettingsModelsService) ListModels(context.Context) ([]*types.Model, error) {
	return s.models, nil
}

type defaultSettingsTenantService struct {
	interfaces.TenantService
	saved *types.Tenant
}

func (s *defaultSettingsTenantService) UpdateTenant(_ context.Context, tenant *types.Tenant) (*types.Tenant, error) {
	s.saved = tenant
	return tenant, nil
}

// defaultSettingsStores knows one registered store, "store-1"; the env
// store resolves as env and everything else as unavailable.
type defaultSettingsStores struct {
	interfaces.VectorStoreService
}

func (defaultSettingsStores) ResolveStoreView(_ context.Context, _ uint64, id string) (types.StoreDisplay, error) {
	switch id {
	case "store-1":
		return types.StoreDisplay{Source: types.StoreSourceUser}, nil
	case "__env_postgres__":
		return types.StoreDisplay{Source: types.StoreSourceEnv}, nil
	}
	return types.UnavailableStoreDisplay(), nil
}

func TestUpdateDefaultSettingsRepairsStaleSlotsIndividually(t *testing.T) {
	for _, tc := range []struct {
		name           string
		previous, next types.DefaultSettings
		wantError      string
	}{
		{
			name:     "clear chat while utility is still stale",
			previous: types.DefaultSettings{ChatModelID: "deleted", UtilityModelID: "deleted"},
			next:     types.DefaultSettings{UtilityModelID: "deleted"},
		},
		{
			name:     "replace chat while utility is still stale",
			previous: types.DefaultSettings{ChatModelID: "deleted", UtilityModelID: "deleted"},
			next:     types.DefaultSettings{ChatModelID: "chat", UtilityModelID: "deleted"},
		},
		{
			name:     "clear remaining stale utility",
			previous: types.DefaultSettings{ChatModelID: "chat", UtilityModelID: "deleted"},
			next:     types.DefaultSettings{ChatModelID: "chat"},
		},
		{
			name: "new missing selection is rejected",
			next: types.DefaultSettings{ChatModelID: "deleted"}, wantError: "model not found",
		},
		{
			name:      "cannot copy stale ID to a different slot",
			previous:  types.DefaultSettings{ChatModelID: "deleted"},
			next:      types.DefaultSettings{ChatModelID: "deleted", UtilityModelID: "deleted"},
			wantError: "model not found",
		},
		{name: "vision capable chat is accepted", next: types.DefaultSettings{VLMModelID: "vision-chat"}},
		{
			name: "text only chat cannot do vision", next: types.DefaultSettings{VLMModelID: "chat"},
			wantError: "must be a VLLM model",
		},
		{
			name: "inactive models cannot be selected", next: types.DefaultSettings{ChatModelID: "downloading"},
			wantError: "model is not active",
		},
		{name: "registered vector store is accepted", next: types.DefaultSettings{VectorStoreID: "store-1"}},
		{
			name: "env store is not a default choice", next: types.DefaultSettings{VectorStoreID: "__env_postgres__"},
			wantError: "vector store not found",
		},
		{
			name: "unknown vector store is rejected", next: types.DefaultSettings{VectorStoreID: "missing"},
			wantError: "vector store not found",
		},
		{
			name:     "deleted default store may stay while another slot changes",
			previous: types.DefaultSettings{VectorStoreID: "deleted"},
			next:     types.DefaultSettings{VectorStoreID: "deleted", ChatModelID: "chat"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tenants := &defaultSettingsTenantService{}
			h := &TenantHandler{service: tenants, vectorStoreService: defaultSettingsStores{}}
			h.modelService = &defaultSettingsModelsService{models: []*types.Model{
				{ID: "chat", Type: types.ModelTypeKnowledgeQA, Status: types.ModelStatusActive},
				{
					ID: "vision-chat", Type: types.ModelTypeKnowledgeQA, Status: types.ModelStatusActive,
					Parameters: types.ModelParameters{SupportsVision: true},
				},
				{ID: "downloading", Type: types.ModelTypeKnowledgeQA, Status: types.ModelStatusDownloading},
			}}
			body, err := json.Marshal(tc.next)
			require.NoError(t, err)
			ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
			tenant := &types.Tenant{ID: 7, DefaultSettings: &tc.previous}
			ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenant)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPut, "/tenants/kv/default-settings", bytes.NewReader(body))
			c.Request = c.Request.WithContext(ctx)
			c.Request.Header.Set("Content-Type", "application/json")
			h.updateTenantDefaultSettingsInternal(c)
			assert.Equal(t, tc.previous, *tenant.DefaultSettings, "do not mutate request tenant")
			if tc.wantError != "" {
				require.NotEmpty(t, c.Errors)
				assert.Contains(t, c.Errors.Last().Error(), tc.wantError)
				assert.Nil(t, tenants.saved)
				return
			}
			require.Empty(t, c.Errors)
			assert.Equal(t, http.StatusOK, w.Code)
			require.NotNil(t, tenants.saved)
			assert.Equal(t, tc.next, *tenants.saved.DefaultSettings)
		})
	}
}
