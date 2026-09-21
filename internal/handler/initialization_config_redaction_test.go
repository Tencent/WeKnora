package handler

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildConfigResponse_ViewerOmitsModelBaseURL(t *testing.T) {
	h := &InitializationHandler{}
	ctx := context.WithValue(context.Background(), types.TenantRoleContextKey, types.TenantRoleViewer)
	models := []*types.Model{{
		Type: types.ModelTypeKnowledgeQA,
		Name: "custom-llm",
		Parameters: types.ModelParameters{
			BaseURL: "https://tenant-private.example.com",
			APIKey:  "sk-secret-do-not-leak",
		},
	}}
	kb := &types.KnowledgeBase{}

	config := h.buildConfigResponse(ctx, models, kb, false)
	llm, ok := config["llm"].(map[string]interface{})
	require.True(t, ok)
	assert.Empty(t, llm["baseUrl"])

	body, err := json.Marshal(config)
	require.NoError(t, err)
	assert.NotContains(t, string(body), "tenant-private.example.com")
	assert.NotContains(t, string(body), "sk-secret-do-not-leak")
}

func TestBuildConfigResponse_AdminKeepsModelBaseURL(t *testing.T) {
	h := &InitializationHandler{}
	ctx := context.WithValue(context.Background(), types.TenantRoleContextKey, types.TenantRoleAdmin)
	ctx = types.WithCaller(ctx, types.Caller{TenantID: 42, UserID: "u", Role: types.TenantRoleAdmin})
	models := []*types.Model{{
		Type: types.ModelTypeKnowledgeQA,
		Name: "custom-llm",
		Parameters: types.ModelParameters{
			BaseURL: "https://tenant-private.example.com",
			APIKey:  "sk-secret-do-not-leak",
		},
	}}
	kb := &types.KnowledgeBase{TenantID: 42}

	config := h.buildConfigResponse(ctx, models, kb, false)
	llm, ok := config["llm"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "https://tenant-private.example.com", llm["baseUrl"])

	body, err := json.Marshal(config)
	require.NoError(t, err)
	assert.NotContains(t, string(body), "sk-secret-do-not-leak")
}

// A share receiver's admin is an admin of its own workspace, not of the KB's:
// it sees whether credentials exist, never the owner's endpoints or buckets.
func TestBuildConfigResponse_ShareReceiverAdminSeesPresenceOnly(t *testing.T) {
	h := &InitializationHandler{}
	ctx := context.WithValue(context.Background(), types.TenantRoleContextKey, types.TenantRoleAdmin)
	ctx = types.WithCaller(ctx, types.Caller{TenantID: 42, UserID: "u", Role: types.TenantRoleAdmin})
	ctx = types.WithExecutionTenant(ctx, 7)
	models := []*types.Model{{
		Type:       types.ModelTypeKnowledgeQA,
		Name:       "owner-llm",
		Parameters: types.ModelParameters{BaseURL: "https://owner-private.example.com"},
	}}
	kb := &types.KnowledgeBase{TenantID: 7}
	legacy := &kb.StorageConfig //nolint:staticcheck // old KBs still carry the legacy COS config
	legacy.Provider = "cos"
	legacy.BucketName = "owner-bucket"
	legacy.Region = "ap-owner"
	legacy.AppID = "owner-app"
	legacy.SecretID = "id"
	legacy.SecretKey = "key"

	config := h.buildConfigResponse(ctx, models, kb, false)
	body, err := json.Marshal(config)
	require.NoError(t, err)
	for _, detail := range []string{"owner-private.example.com", "owner-bucket", "ap-owner", "owner-app"} {
		assert.NotContains(t, string(body), detail)
	}
	cos := config["multimodal"].(map[string]interface{})["cos"].(map[string]interface{})
	assert.Equal(t, map[string]bool{"secretId": true, "secretKey": true}, cos["credentials"])
}
