package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
)

// TestUpdateKnowledgeBaseAppliesVLMConfig guards issue #1723: the KB update
// API previously had no way to change a knowledge base's multimodal (VLM)
// config, because neither the HTTP request nor the service method carried it.
func TestUpdateKnowledgeBaseAppliesVLMConfig(t *testing.T) {
	repo := newFakeKBRepo()
	repo.rows["kb-1"] = &types.KnowledgeBase{ID: "kb-1", Name: "old", TenantID: 1}
	svc := &knowledgeBaseService{repo: repo}
	ctx := context.Background()

	// nil vlmConfig must preserve whatever is already stored.
	kb, err := svc.UpdateKnowledgeBase(ctx, "kb-1", "n", "d", nil, nil)
	require.NoError(t, err)
	assert.False(t, kb.VLMConfig.Enabled, "absent vlm_config must not enable multimodal")

	// Providing vlm_config must persist it — this was silently dropped before #1723.
	kb, err = svc.UpdateKnowledgeBase(ctx, "kb-1", "n", "d", nil,
		&types.VLMConfig{Enabled: true, ModelID: "vlm-1"})
	require.NoError(t, err)
	assert.True(t, kb.VLMConfig.Enabled)
	assert.Equal(t, "vlm-1", kb.VLMConfig.ModelID)
}
