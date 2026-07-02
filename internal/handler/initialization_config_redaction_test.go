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
	assert.Equal(t, "https://tenant-private.example.com", llm["baseUrl"])

	body, err := json.Marshal(config)
	require.NoError(t, err)
	assert.NotContains(t, string(body), "sk-secret-do-not-leak")
}
