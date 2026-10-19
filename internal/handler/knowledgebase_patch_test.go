package handler

import (
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplyKnowledgeBasePatchKeepsOmittedFields(t *testing.T) {
	name := "产品文档"
	kb := &types.KnowledgeBase{
		Name:        "旧名称",
		Description: "旧描述",
		ChunkingConfig: types.ChunkingConfig{
			ChunkSize:    512,
			ChunkOverlap: 50,
		},
		IndexingStrategy: types.IndexingStrategy{VectorEnabled: true, KeywordEnabled: true},
	}

	got, err := applyKnowledgeBasePatch(kb, PatchKnowledgeBaseRequest{Name: &name}, nil)
	require.NoError(t, err)
	assert.Equal(t, "产品文档", got.Name)
	assert.Equal(t, "旧描述", got.Description, "an omitted description stays")
	assert.Nil(t, got.Config)

	cleared := ""
	got, err = applyKnowledgeBasePatch(kb, PatchKnowledgeBaseRequest{Description: &cleared}, nil)
	require.NoError(t, err)
	assert.Equal(t, "旧名称", got.Name)
	assert.Empty(t, got.Description)

	blank := "  "
	_, err = applyKnowledgeBasePatch(kb, PatchKnowledgeBaseRequest{Name: &blank}, nil)
	require.Error(t, err)

	got, err = applyKnowledgeBasePatch(kb, PatchKnowledgeBaseRequest{}, map[string]json.RawMessage{
		"indexing_strategy": json.RawMessage(`{"wiki_enabled":true}`),
		"chunking_config":   json.RawMessage(`{"chunk_size":800}`),
	})
	require.NoError(t, err)
	require.NotNil(t, got.Config)
	require.NotNil(t, got.Config.IndexingStrategy)
	assert.True(t, got.Config.IndexingStrategy.VectorEnabled, "an omitted indexing flag stays on")
	assert.True(t, got.Config.IndexingStrategy.KeywordEnabled)
	assert.True(t, got.Config.IndexingStrategy.WikiEnabled)
	assert.False(t, got.Config.IndexingStrategy.GraphEnabled)
	assert.Equal(t, 800, got.Config.ChunkingConfig.ChunkSize)
	assert.Equal(t, 50, got.Config.ChunkingConfig.ChunkOverlap, "an omitted chunking field stays")
}

func TestFillOmittedIndexingFlagsKeepsDefaults(t *testing.T) {
	raw := []byte(`{"name":"产品文档","indexing_strategy":{"wiki_enabled":true}}`)
	kb := &types.KnowledgeBase{}
	require.NoError(t, json.Unmarshal(raw, kb))
	fillOmittedIndexingFlags(raw, kb)
	assert.True(t, kb.IndexingStrategy.VectorEnabled)
	assert.True(t, kb.IndexingStrategy.KeywordEnabled)
	assert.True(t, kb.IndexingStrategy.WikiEnabled)
	assert.False(t, kb.IndexingStrategy.GraphEnabled)

	raw = []byte(`{"indexing_strategy":{"vector_enabled":false,"keyword_enabled":false,` +
		`"wiki_enabled":false,"graph_enabled":true}}`)
	explicit := &types.KnowledgeBase{}
	require.NoError(t, json.Unmarshal(raw, explicit))
	fillOmittedIndexingFlags(raw, explicit)
	assert.False(t, explicit.IndexingStrategy.VectorEnabled)
	assert.True(t, explicit.IndexingStrategy.GraphEnabled)
}
