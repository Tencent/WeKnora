package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type artifactCacheChatModel struct {
	modelID   string
	modelName string
	response  string
	calls     int
}

func (m *artifactCacheChatModel) Chat(
	_ context.Context,
	_ []chat.Message,
	_ *chat.ChatOptions,
) (*types.ChatResponse, error) {
	m.calls++
	return &types.ChatResponse{Content: m.response}, nil
}

func (m *artifactCacheChatModel) ChatStream(
	context.Context,
	[]chat.Message,
	*chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	return nil, nil
}

func (m *artifactCacheChatModel) GetModelName() string { return m.modelName }
func (m *artifactCacheChatModel) GetModelID() string   { return m.modelID }

func TestArtifactCacheKeysInvalidateOnInputs(t *testing.T) {
	model := &artifactCacheChatModel{modelID: "model-a", modelName: "name-a"}
	otherModel := &artifactCacheChatModel{modelID: "model-b", modelName: "name-a"}
	template := &types.PromptTemplateStructured{
		Description: "extract {{graph}}",
		Tags:        []string{"tag-a"},
	}
	extractCfg := types.ExtractConfig{
		Enabled:            true,
		CustomInstructions: "domain rules",
		Tags:               []string{"tag-a"},
	}
	chunk := &types.Chunk{ID: "chunk-a", Content: "Graph content", ContentHash: types.ContentHash("Graph content", "")}

	graphKey := graphChunkCacheKey(chunk, model, template, extractCfg)
	assert.Less(t, len(graphKey), 255)
	assert.Equal(t, graphKey, graphChunkCacheKey(chunk, model, template, extractCfg))
	assert.NotEqual(t, graphKey, graphChunkCacheKey(
		&types.Chunk{ID: "chunk-a", Content: "changed", ContentHash: types.ContentHash("changed", "")},
		model, template, extractCfg,
	))
	assert.NotEqual(t, graphKey, graphChunkCacheKey(chunk, otherModel, template, extractCfg))
	changedTemplate := *template
	changedTemplate.Description = "changed"
	assert.NotEqual(t, graphKey, graphChunkCacheKey(chunk, model, &changedTemplate, extractCfg))
	changedCfg := extractCfg
	changedCfg.CustomInstructions = "changed"
	assert.NotEqual(t, graphKey, graphChunkCacheKey(chunk, model, template, changedCfg))

	summaryKey := summaryCacheKey("knowledge-a", "content", model, "prompt", "English", 1000, 512)
	assert.Less(t, len(summaryKey), 255)
	assert.Equal(t, summaryKey, summaryCacheKey("knowledge-a", " content ", model, "prompt", "English", 1000, 512))
	assert.NotEqual(t, summaryKey, summaryCacheKey("knowledge-a", "changed", model, "prompt", "English", 1000, 512))
	assert.NotEqual(t, summaryKey, summaryCacheKey("knowledge-a", "content", otherModel, "prompt", "English", 1000, 512))
	assert.NotEqual(t, summaryKey, summaryCacheKey("knowledge-a", "content", model, "changed", "English", 1000, 512))
	assert.NotEqual(t, summaryKey, summaryCacheKey("knowledge-a", "content", model, "prompt", "Chinese", 1000, 512))
	assert.NotEqual(t, summaryKey, summaryCacheKey("knowledge-a", "content", model, "prompt", "English", 2000, 512))
	assert.NotEqual(t, summaryKey, summaryCacheKey("knowledge-a", "content", model, "prompt", "English", 1000, 1024))

	questionKey := questionCacheKey("content", "prev", "next", "doc", model, "prompt", "custom", "English", 3)
	assert.Less(t, len(questionKey), 255)
	assert.Equal(t, questionKey, questionCacheKey(" content ", "prev", "next", "doc", model, "prompt", "custom", "English", 3))
	assert.NotEqual(t, questionKey, questionCacheKey("changed", "prev", "next", "doc", model, "prompt", "custom", "English", 3))
	assert.NotEqual(t, questionKey, questionCacheKey("content", "changed", "next", "doc", model, "prompt", "custom", "English", 3))
	assert.NotEqual(t, questionKey, questionCacheKey("content", "prev", "changed", "doc", model, "prompt", "custom", "English", 3))
	assert.NotEqual(t, questionKey, questionCacheKey("content", "prev", "next", "changed", model, "prompt", "custom", "English", 3))
	assert.NotEqual(t, questionKey, questionCacheKey("content", "prev", "next", "doc", otherModel, "prompt", "custom", "English", 3))
	assert.NotEqual(t, questionKey, questionCacheKey("content", "prev", "next", "doc", model, "changed", "custom", "English", 3))
	assert.NotEqual(t, questionKey, questionCacheKey("content", "prev", "next", "doc", model, "prompt", "changed", "English", 3))
	assert.NotEqual(t, questionKey, questionCacheKey("content", "prev", "next", "doc", model, "prompt", "custom", "Chinese", 3))
	assert.NotEqual(t, questionKey, questionCacheKey("content", "prev", "next", "doc", model, "prompt", "custom", "English", 4))
}

func TestCloneGraphDataForChunkRebindsWithoutMutatingCachedGraph(t *testing.T) {
	cached := &types.GraphData{
		Text: "source",
		Node: []*types.GraphNode{{
			Name:       "Acme",
			Chunks:     []string{"old-chunk"},
			Attributes: []string{"company"},
		}},
		Relation: []*types.GraphRelation{{
			Node1: "Acme",
			Node2: "Search",
			Type:  "builds",
		}},
	}

	got := cloneGraphDataForChunk(cached, "current-chunk")

	require.NotNil(t, got)
	require.Len(t, got.Node, 1)
	assert.Equal(t, []string{"current-chunk"}, got.Node[0].Chunks)
	assert.Equal(t, []string{"old-chunk"}, cached.Node[0].Chunks)
	got.Node[0].Attributes[0] = "mutated"
	assert.Equal(t, "company", cached.Node[0].Attributes[0])
	require.Len(t, got.Relation, 1)
	got.Relation[0].Type = "changed"
	assert.Equal(t, "builds", cached.Relation[0].Type)
}

func TestGenerateQuestionsWithContextUsesCache(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	repo := newFakeContentCacheRepo()
	svc := &knowledgeService{
		config: &config.Config{Conversation: &config.ConversationConfig{
			GenerateQuestionsPrompt: "Generate {{question_count}} questions for {{doc_name}} in {{language}}.\n{{context}}\n{{content}}",
		}},
		contentCacheRepo: repo,
	}
	model := &artifactCacheChatModel{
		modelID:  "question-model",
		response: "1. What is Acme?\n2. How does Acme use search?",
	}

	first, err := svc.generateQuestionsWithContext(ctx, model,
		"Acme builds search tools.", "Before", "After", "Doc", 2, "Prefer concise questions.")
	require.NoError(t, err)
	assert.Len(t, first, 2)
	assert.Equal(t, 1, model.calls)

	second, err := svc.generateQuestionsWithContext(ctx, model,
		"Acme builds search tools.", "Before", "After", "Doc", 2, "Prefer concise questions.")
	require.NoError(t, err)
	assert.Equal(t, first, second)
	assert.Equal(t, 1, model.calls)

	_, err = svc.generateQuestionsWithContext(ctx, model,
		"Acme builds search tools.", "Before", "After", "Doc", 3, "Prefer concise questions.")
	require.NoError(t, err)
	assert.Equal(t, 2, model.calls)
}

func TestGenerateQuestionsWithContextCacheErrorsFallback(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	repo := newFakeContentCacheRepo()
	repo.getErr = errors.New("cache read failed")
	repo.upsertErr = errors.New("cache write failed")
	svc := &knowledgeService{
		config: &config.Config{Conversation: &config.ConversationConfig{
			GenerateQuestionsPrompt: "Generate {{question_count}} questions.\n{{content}}",
		}},
		contentCacheRepo: repo,
	}
	model := &artifactCacheChatModel{
		modelID:  "question-model",
		response: "1. What is Acme?",
	}

	questions, err := svc.generateQuestionsWithContext(ctx, model,
		"Acme builds search tools.", "", "", "Doc", 1, "")

	require.NoError(t, err)
	assert.Equal(t, []string{"What is Acme?"}, questions)
	assert.Equal(t, 1, model.calls)
}
