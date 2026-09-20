package dto

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/catalog"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModelResponse_OmitsSecrets(t *testing.T) {
	m := &types.Model{
		ID:          "m-1",
		Name:        "gpt-x",
		DisplayName: "Support QA",
		Parameters: types.ModelParameters{
			APIKey:    "sk-real-api-key-do-not-leak",
			AppSecret: "app-real-secret-do-not-leak",
			AppID:     "appid-public-ok-to-show",
			BaseURL:   "https://api.example.com",
			Provider:  "openai",
		},
	}
	body, err := json.Marshal(NewModelResponse(adminContext(), m))
	assert.NoError(t, err)
	s := string(body)
	assert.NotContains(t, s, "sk-real-api-key-do-not-leak")
	assert.NotContains(t, s, "app-real-secret-do-not-leak")
	// Parameters sub-object must contain no secret keys.
	var raw map[string]json.RawMessage
	assert.NoError(t, json.Unmarshal(body, &raw))
	params := string(raw["parameters"])
	assert.NotContains(t, params, `"api_key"`)
	assert.NotContains(t, params, `"app_secret"`)
	// Credential metadata map exposes booleans only.
	assert.Contains(t, s, `"credentials"`)
	assert.Contains(t, s, `"api_key":{"configured":true}`)
	assert.Contains(t, s, `"app_secret":{"configured":true}`)
	// Non-secret fields pass through.
	assert.Contains(t, s, "appid-public-ok-to-show")
	assert.Contains(t, s, "api.example.com")
	assert.Contains(t, s, `"display_name":"Support QA"`)
}

func TestModelResponse_BuiltinStripsTenantConfig(t *testing.T) {
	m := &types.Model{
		ID:        "builtin-1",
		IsBuiltin: true,
		Parameters: types.ModelParameters{
			BaseURL:        "https://tenant-private.example.com",
			APIKey:         "should-not-leak",
			AppID:          "tenant-app-id",
			SupportsVision: true,
			ExtraConfig:    map[string]string{"region": "cn-hangzhou"},
		},
	}
	resp := NewModelResponse(adminContext(), m)
	assert.Empty(t, resp.Parameters.BaseURL,
		"builtin must not leak per-tenant base URL")
	assert.Empty(t, resp.Parameters.AppID,
		"builtin must not leak per-tenant app_id")
	assert.Nil(t, resp.Parameters.ExtraConfig,
		"builtin must not leak per-tenant extra_config")
	assert.True(t, resp.Parameters.SupportsVision,
		"capability metadata must survive (not per-tenant)")

	body, _ := json.Marshal(resp)
	assert.False(t, strings.Contains(string(body), "should-not-leak"))
	assert.False(t, strings.Contains(string(body), "tenant-private.example.com"))
}

func TestModelResponse_SystemAdminCanManageBuiltinConfig(t *testing.T) {
	ctx := context.WithValue(viewerContext(), types.SystemAdminContextKey, true)
	m := &types.Model{
		ID:        "builtin-1",
		IsBuiltin: true,
		Parameters: types.ModelParameters{
			BaseURL:       "https://global-provider.example.com",
			APIKey:        "should-never-be-returned",
			AppSecret:     "also-never-returned",
			AppID:         "global-app-id",
			ExtraConfig:   map[string]string{"region": "ap-guangzhou"},
			CustomHeaders: map[string]string{"X-Route": "global"},
		},
	}

	resp := NewModelResponse(ctx, m)
	assert.Equal(t, "https://global-provider.example.com", resp.Parameters.BaseURL)
	assert.Equal(t, "global-app-id", resp.Parameters.AppID)
	assert.Equal(t, map[string]string{"region": "ap-guangzhou"}, resp.Parameters.ExtraConfig)
	assert.Equal(t, map[string]string{"X-Route": "global"}, resp.Parameters.CustomHeaders)
	assert.True(t, resp.Credentials["api_key"].Configured)
	assert.True(t, resp.Credentials["app_secret"].Configured)

	body, err := json.Marshal(resp)
	require.NoError(t, err)
	assert.NotContains(t, string(body), "should-never-be-returned")
	assert.NotContains(t, string(body), "also-never-returned")
}

func TestModelResponse_ViewerStripsIntegrationDetail(t *testing.T) {
	m := &types.Model{
		ID: "m-2",
		Parameters: types.ModelParameters{
			BaseURL:       "https://tenant-private.example.com",
			CustomHeaders: map[string]string{"Authorization": "Bearer secret"},
			ExtraConfig:   map[string]string{"region": "cn-hangzhou"},
		},
	}
	resp := NewModelResponse(viewerContext(), m)
	assert.Empty(t, resp.Parameters.BaseURL)
	assert.Nil(t, resp.Parameters.CustomHeaders)
	assert.Nil(t, resp.Parameters.ExtraConfig)
}

// registerSecretExtraVendor adds a vendor whose extra_config carries a real
// credential, the way LKEAP / Volcengine rerank declare secret_key.
func registerSecretExtraVendor(t *testing.T) string {
	t.Helper()
	id := "dto-secret-extra-vendor"
	catalog.Register(&catalog.Vendor{
		ID:          id,
		Name:        "Secret Extra Vendor",
		ModelTypes:  []types.ModelType{types.ModelTypeRerank},
		URLPatterns: []string{"secret-extra-vendor.example.com"},
		ExtraFields: []catalog.ExtraField{
			{Key: "secret_key", Label: "Secret Key", Type: "password", Secret: true},
			{Key: "region", Label: "Region", Type: "string"},
		},
	})
	return id
}

// A vendor-declared secret extra field is a credential that happens to live
// in extra_config: GET must report its presence, never its value — even to a
// caller allowed to see integration detail, exactly like api_key.
func TestModelResponse_RedactsVendorSecretExtraConfig(t *testing.T) {
	provider := registerSecretExtraVendor(t)
	m := &types.Model{
		ID:   "m-rerank",
		Type: types.ModelTypeRerank,
		Parameters: types.ModelParameters{
			Provider: provider,
			BaseURL:  "https://secret-extra-vendor.example.com",
			APIKey:   "AKID-public-id",
			ExtraConfig: map[string]string{
				"secret_key": "cam-secret-do-not-leak",
				"region":     "ap-guangzhou",
			},
		},
	}
	resp := NewModelResponse(adminContext(), m)

	assert.NotContains(t, resp.Parameters.ExtraConfig, "secret_key")
	assert.Equal(t, "ap-guangzhou", resp.Parameters.ExtraConfig["region"],
		"non-secret extra fields still round-trip")
	assert.True(t, resp.Credentials["secret_key"].Configured,
		"presence is reported in the same shape as api_key")

	body, err := json.Marshal(resp)
	require.NoError(t, err)
	assert.NotContains(t, string(body), "cam-secret-do-not-leak")

	assert.Equal(t, "cam-secret-do-not-leak", m.Parameters.ExtraConfig["secret_key"],
		"the stored model must not be mutated by rendering a response")
}

func TestModelResponse_SecretExtraConfigReportsAbsence(t *testing.T) {
	provider := registerSecretExtraVendor(t)
	m := &types.Model{
		ID:         "m-rerank",
		Type:       types.ModelTypeRerank,
		Parameters: types.ModelParameters{Provider: provider, ExtraConfig: map[string]string{"region": "ap-beijing"}},
	}
	resp := NewModelResponse(adminContext(), m)
	assert.False(t, resp.Credentials["secret_key"].Configured)
	assert.Equal(t, "ap-beijing", resp.Parameters.ExtraConfig["region"])
}

// Legacy rows saved before provider was stored are matched by endpoint.
func TestModelResponse_RedactsSecretExtraConfigForLegacyRowWithoutProvider(t *testing.T) {
	registerSecretExtraVendor(t)
	m := &types.Model{
		ID:   "m-legacy",
		Type: types.ModelTypeRerank,
		Parameters: types.ModelParameters{
			BaseURL:     "https://secret-extra-vendor.example.com/v1",
			ExtraConfig: map[string]string{"secret_key": "cam-secret-do-not-leak"},
		},
	}
	resp := NewModelResponse(adminContext(), m)
	assert.NotContains(t, resp.Parameters.ExtraConfig, "secret_key")
	assert.True(t, resp.Credentials["secret_key"].Configured)
}

// PUT replaces extra_config wholesale and GET redacts the secret, so the
// save that follows an unrelated edit must not erase the stored key.
func TestPreserveStoredSecretExtras(t *testing.T) {
	provider := registerSecretExtraVendor(t)
	stored := map[string]string{"secret_key": "cam-secret-stored", "region": "ap-guangzhou"}

	t.Run("incoming omits the key", func(t *testing.T) {
		got := PreserveStoredSecretExtras(stored, map[string]string{"region": "ap-beijing"}, provider, "")
		assert.Equal(t, "cam-secret-stored", got["secret_key"])
		assert.Equal(t, "ap-beijing", got["region"], "non-secret edits still apply")
	})
	t.Run("incoming carries an empty or masked value", func(t *testing.T) {
		for _, masked := range []string{"", "   ", "******", "••••"} {
			got := PreserveStoredSecretExtras(stored, map[string]string{"secret_key": masked}, provider, "")
			assert.Equal(t, "cam-secret-stored", got["secret_key"], "masked value %q means unchanged", masked)
		}
	})
	t.Run("incoming carries a new value", func(t *testing.T) {
		got := PreserveStoredSecretExtras(stored, map[string]string{"secret_key": "cam-secret-rotated"}, provider, "")
		assert.Equal(t, "cam-secret-rotated", got["secret_key"])
	})
	t.Run("nil incoming keeps the whole stored map", func(t *testing.T) {
		assert.Equal(t, stored, PreserveStoredSecretExtras(stored, nil, provider, ""))
	})
	t.Run("unknown provider passes the map through", func(t *testing.T) {
		in := map[string]string{"secret_key": ""}
		assert.Equal(t, in, PreserveStoredSecretExtras(stored, in, "no-such-vendor", ""))
	})
}

func TestHasAllSecretExtras(t *testing.T) {
	provider := registerSecretExtraVendor(t)
	assert.True(t, HasAllSecretExtras(provider, "", map[string]string{"secret_key": "cam-secret"}))
	assert.False(t, HasAllSecretExtras(provider, "", map[string]string{"region": "ap-guangzhou"}))
	assert.False(t, HasAllSecretExtras(provider, "", map[string]string{"secret_key": "****"}))
	assert.True(t, HasAllSecretExtras("no-such-vendor", "", nil), "a vendor with no secret extras is complete")
}

func TestModelResponse_NilSafe(t *testing.T) {
	assert.Nil(t, NewModelResponse(adminContext(), nil))
	assert.Equal(t, []*ModelResponse{}, NewModelResponses(adminContext(), nil))
}
