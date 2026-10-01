package dto

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/providers"
	modelruntime "github.com/Tencent/WeKnora/internal/models/runtime"
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
	modelruntime.Register(&providers.Definition{
		ID:          id,
		Name:        "Secret Extra Vendor",
		ModelTypes:  []types.ModelType{types.ModelTypeRerank},
		URLPatterns: []string{"secret-extra-vendor.example.com"},
		ExtraFields: []providers.ExtraField{
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
	sameVendor := VendorRef{Provider: provider}

	t.Run("incoming omits the key", func(t *testing.T) {
		got := PreserveStoredSecretExtras(
			stored, map[string]string{"region": "ap-beijing"}, sameVendor, sameVendor)
		assert.Equal(t, "cam-secret-stored", got["secret_key"])
		assert.Equal(t, "ap-beijing", got["region"], "non-secret edits still apply")
	})
	t.Run("incoming carries an empty or masked value", func(t *testing.T) {
		for _, masked := range []string{"", "   ", "******", "••••"} {
			got := PreserveStoredSecretExtras(
				stored, map[string]string{"secret_key": masked}, sameVendor, sameVendor)
			assert.Equal(t, "cam-secret-stored", got["secret_key"], "masked value %q means unchanged", masked)
		}
	})
	t.Run("incoming carries a new value", func(t *testing.T) {
		got := PreserveStoredSecretExtras(
			stored, map[string]string{"secret_key": "cam-secret-rotated"}, sameVendor, sameVendor)
		assert.Equal(t, "cam-secret-rotated", got["secret_key"])
	})
	t.Run("nil incoming keeps the whole stored map", func(t *testing.T) {
		assert.Equal(t, stored, PreserveStoredSecretExtras(stored, nil, sameVendor, sameVendor))
	})
	t.Run("unknown stored provider passes the map through", func(t *testing.T) {
		in := map[string]string{"secret_key": ""}
		assert.Equal(t, in,
			PreserveStoredSecretExtras(stored, in, VendorRef{Provider: "no-such-vendor"}, sameVendor))
	})
	t.Run("a request naming no vendor is not a vendor change", func(t *testing.T) {
		got := PreserveStoredSecretExtras(stored, map[string]string{}, sameVendor, VendorRef{})
		assert.Equal(t, "cam-secret-stored", got["secret_key"])
	})

	// Switching the row to another vendor leaves the credential behind: it
	// authenticates the account of the integration being replaced.
	t.Run("vendor change drops the stored secret", func(t *testing.T) {
		other := VendorRef{Provider: "openai"}
		got := PreserveStoredSecretExtras(stored, map[string]string{"api": "openai_completions"}, sameVendor, other)
		assert.NotContains(t, got, "secret_key")
		assert.Equal(t, "openai_completions", got["api"])
	})
	t.Run("vendor change drops a masked secret instead of refilling it", func(t *testing.T) {
		other := VendorRef{Provider: "openai"}
		got := PreserveStoredSecretExtras(stored, map[string]string{"secret_key": "****"}, sameVendor, other)
		assert.NotContains(t, got, "secret_key")
	})
	t.Run("vendor change keeps a value the user typed", func(t *testing.T) {
		other := VendorRef{Provider: "openai"}
		got := PreserveStoredSecretExtras(stored, map[string]string{"secret_key": "typed-now"}, sameVendor, other)
		assert.Equal(t, "typed-now", got["secret_key"])
	})
	t.Run("vendor change with no extra_config at all", func(t *testing.T) {
		got := PreserveStoredSecretExtras(stored, nil, sameVendor, VendorRef{Provider: "openai"})
		assert.NotContains(t, got, "secret_key")
		assert.Equal(t, "ap-guangzhou", got["region"], "non-secret settings are not credentials")
		assert.Equal(t, "cam-secret-stored", stored["secret_key"], "the stored map is not mutated")
	})
	t.Run("a legacy row is matched by endpoint, not treated as a switch", func(t *testing.T) {
		legacy := VendorRef{BaseURL: "https://secret-extra-vendor.example.com/v1"}
		got := PreserveStoredSecretExtras(stored, map[string]string{}, legacy, legacy)
		assert.Equal(t, "cam-secret-stored", got["secret_key"])
	})
}

// Redaction may not depend on the row's provider being the one that declared
// the key: a hand-edited or emptied provider would otherwise echo a stored
// credential in plaintext.
func TestModelResponse_RedactsSecretExtraDeclaredByAnotherVendor(t *testing.T) {
	registerSecretExtraVendor(t)
	for _, provider := range []string{"openai", ""} {
		m := &types.Model{
			ID:   "m-switched",
			Type: types.ModelTypeRerank,
			Parameters: types.ModelParameters{
				Provider:    provider,
				ExtraConfig: map[string]string{"secret_key": "cam-secret-do-not-leak", "region": "ap-beijing"},
			},
		}
		resp := NewModelResponse(adminContext(), m)
		body, err := json.Marshal(resp)
		require.NoError(t, err)
		assert.NotContains(t, string(body), "cam-secret-do-not-leak", "provider %q", provider)
		assert.Equal(t, "ap-beijing", resp.Parameters.ExtraConfig["region"])
		assert.True(t, resp.Credentials["secret_key"].Configured,
			"a withheld value is still reported as present")
	}
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

func TestNewModelResponseStripsSpecFromNonAdmins(t *testing.T) {
	// Spec pins the protocol and carries the compat overlay, whose
	// extra_body is merged verbatim into every request. It is deployment
	// configuration, like base_url and extra_config beside it, not part of
	// the capability surface a viewer is shown.
	spec := &types.ModelSpecOverride{
		API:    "anthropic-messages",
		Compat: map[string]any{"extra_body": map[string]any{"internal_route": "eu-gateway"}},
	}
	model := &types.Model{
		ID: "m1", Name: "claude-sonnet-4-5", Type: types.ModelTypeKnowledgeQA,
		Source: types.ModelSourceRemote,
		Parameters: types.ModelParameters{
			Provider: "anthropic", BaseURL: "https://gateway.internal/v1", Spec: spec,
		},
	}

	admin := context.WithValue(context.Background(), types.TenantRoleContextKey, types.TenantRoleAdmin)
	if got := NewModelResponse(admin, model).Parameters.Spec; got == nil {
		t.Fatalf("an admin configures this; it must still be returned")
	}

	viewer := context.WithValue(context.Background(), types.TenantRoleContextKey, types.TenantRoleViewer)
	params := NewModelResponse(viewer, model).Parameters
	if params.Spec != nil {
		t.Errorf("viewer should not see the protocol override, got %+v", params.Spec)
	}
	if params.BaseURL != "" || params.ExtraConfig != nil {
		t.Errorf("the neighbouring fields should still be stripped")
	}
	// The stored model is shared; stripping must not mutate it.
	if model.Parameters.Spec == nil {
		t.Errorf("NewModelResponse must not clear the stored spec")
	}
}
