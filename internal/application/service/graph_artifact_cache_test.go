package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewGraphArtifactKeyCoversExtractionInputs(t *testing.T) {
	base := testGraphArtifactRequest()
	baseKey, err := newGraphArtifactKey(base)
	require.NoError(t, err)
	assert.Equal(t, "chat.graph-extraction", baseKey.Stage)
	assert.Equal(t, uint16(1), baseKey.KeyVersion)

	tests := []struct {
		name   string
		mutate func(*graphArtifactRequest)
	}{
		{name: "tenant", mutate: func(r *graphArtifactRequest) { r.tenantID++ }},
		{name: "model", mutate: func(r *graphArtifactRequest) {
			r.model = &chatArtifactFakeModel{modelID: "model-2", modelName: "chat"}
		}},
		{name: "model revision", mutate: func(r *graphArtifactRequest) { r.modelRevision = "revision-2" }},
		{name: "chunk content", mutate: func(r *graphArtifactRequest) { r.messages[1].Content = "other" }},
		{name: "effective prompt", mutate: func(r *graphArtifactRequest) { r.messages[0].Content = "other" }},
		{name: "options", mutate: func(r *graphArtifactRequest) { r.options.MaxTokens++ }},
		{name: "prompt version", mutate: func(r *graphArtifactRequest) { r.promptVersion = "graph-prompt-v2" }},
		{name: "canonicalizer version", mutate: func(r *graphArtifactRequest) {
			r.canonicalizerVersion = "graph-v2"
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := testGraphArtifactRequest()
			tt.mutate(&request)
			key, keyErr := newGraphArtifactKey(request)
			require.NoError(t, keyErr)
			assert.NotEqual(t, baseKey, key)
		})
	}
}

func TestNewGraphArtifactKeyRejectsIncompleteExtractionMessages(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*graphArtifactRequest)
	}{
		{name: "missing messages", mutate: func(r *graphArtifactRequest) { r.messages = nil }},
		{name: "missing system prompt", mutate: func(r *graphArtifactRequest) { r.messages[0].Content = "" }},
		{name: "wrong system role", mutate: func(r *graphArtifactRequest) { r.messages[0].Role = "user" }},
		{name: "wrong user role", mutate: func(r *graphArtifactRequest) { r.messages[1].Role = "system" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := testGraphArtifactRequest()
			tt.mutate(&request)
			_, err := newGraphArtifactKey(request)
			require.Error(t, err)
		})
	}
}

func TestGraphArtifactCodecIsDeterministicAndRebindsNodeOwnership(t *testing.T) {
	first := &types.GraphData{
		Text: "source text",
		Node: []*types.GraphNode{
			{Name: "Beta", Chunks: []string{"old-chunk"}, Attributes: []string{"z", "a"}},
			{Name: "Alpha", Chunks: []string{"other-chunk"}},
		},
		Relation: []*types.GraphRelation{
			{Node1: "Beta", Node2: "Alpha", Type: "knows"},
		},
	}
	second := &types.GraphData{
		Node: []*types.GraphNode{
			{Name: "Alpha"},
			{Name: "Beta", Attributes: []string{"a", "z"}},
		},
		Relation: []*types.GraphRelation{
			{Node1: "Beta", Node2: "Alpha", Type: "knows"},
		},
	}

	firstPayload, err := encodeGraphArtifact(first)
	require.NoError(t, err)
	secondPayload, err := encodeGraphArtifact(second)
	require.NoError(t, err)
	assert.Equal(t, firstPayload, secondPayload)

	decoded, err := decodeGraphArtifact(firstPayload, "current-chunk")
	require.NoError(t, err)
	assert.Empty(t, decoded.Text)
	assert.Equal(t, []*types.GraphNode{
		{Name: "Alpha", Chunks: []string{"current-chunk"}},
		{Name: "Beta", Chunks: []string{"current-chunk"}, Attributes: []string{"a", "z"}},
	}, decoded.Node)
	assert.Equal(t, []*types.GraphRelation{
		{Node1: "Beta", Node2: "Alpha", Type: "knows", Chunks: []string{"current-chunk"}},
	}, decoded.Relation)

	assert.Equal(t, []string{"old-chunk"}, first.Node[0].Chunks, "encoding must not mutate provider output")
	assert.Equal(t, []string{"z", "a"}, first.Node[0].Attributes)
}

func TestGraphArtifactCodecRebindsRelationOwnership(t *testing.T) {
	graph := &types.GraphData{
		Node: []*types.GraphNode{
			{Name: "Alpha"},
			{Name: "Beta"},
		},
		Relation: []*types.GraphRelation{
			{Node1: "Alpha", Node2: "Beta", Type: "knows", Chunks: []string{"old-chunk"}},
		},
	}

	payload, err := encodeGraphArtifact(graph)
	require.NoError(t, err)
	assert.NotContains(t, string(payload), "old-chunk")

	decoded, err := decodeGraphArtifact(payload, "current-chunk")
	require.NoError(t, err)
	require.Len(t, decoded.Relation, 1)
	assert.Equal(t, []string{"current-chunk"}, decoded.Relation[0].Chunks)
	assert.Equal(t, []string{"old-chunk"}, graph.Relation[0].Chunks, "encoding must not mutate provider output")
}

func TestGraphArtifactCodecRejectsSelfRelation(t *testing.T) {
	_, err := encodeGraphArtifact(&types.GraphData{
		Node: []*types.GraphNode{{Name: "Alpha"}},
		Relation: []*types.GraphRelation{
			{Node1: "Alpha", Node2: "Alpha", Type: "knows"},
		},
	})
	require.ErrorContains(t, err, "self relation")
}

func TestGraphArtifactCodecValidatesSemanticGraph(t *testing.T) {
	tests := []struct {
		name  string
		graph *types.GraphData
	}{
		{name: "nil graph"},
		{name: "nil node", graph: &types.GraphData{Node: []*types.GraphNode{nil}}},
		{name: "empty node", graph: &types.GraphData{Node: []*types.GraphNode{{Name: " "}}}},
		{name: "duplicate node", graph: &types.GraphData{Node: []*types.GraphNode{{Name: "A"}, {Name: "A"}}}},
		{name: "nil relation", graph: &types.GraphData{Relation: []*types.GraphRelation{nil}}},
		{name: "incomplete relation", graph: &types.GraphData{
			Node:     []*types.GraphNode{{Name: "A"}, {Name: "B"}},
			Relation: []*types.GraphRelation{{Node1: "A", Node2: "B"}},
		}},
		{name: "unknown relation endpoint", graph: &types.GraphData{
			Node:     []*types.GraphNode{{Name: "A"}},
			Relation: []*types.GraphRelation{{Node1: "A", Node2: "B", Type: "knows"}},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := encodeGraphArtifact(tt.graph)
			require.Error(t, err)
		})
	}
}

func TestGraphArtifactCodecDeduplicatesRelationsAcceptedByParser(t *testing.T) {
	graph := &types.GraphData{
		Node: []*types.GraphNode{{Name: "A"}, {Name: "B"}},
		Relation: []*types.GraphRelation{
			{Node1: "A", Node2: "B", Type: "knows"},
			{Node1: "A", Node2: "B", Type: "knows"},
		},
	}

	payload, err := encodeGraphArtifact(graph)
	require.NoError(t, err)
	decoded, err := decodeGraphArtifact(payload, "chunk")
	require.NoError(t, err)
	assert.Len(t, decoded.Relation, 1)
}

func TestDecodeGraphArtifactCanonicalizesPersistedRelationOrderAndDuplicates(t *testing.T) {
	payload := []byte(`{
		"version":1,
		"nodes":[{"name":"B","attributes":[]},{"name":"A","attributes":[]}],
		"relations":[
			{"node1":"B","node2":"A","type":"z"},
			{"node1":"A","node2":"B","type":"a"},
			{"node1":"A","node2":"B","type":"a"}
		]
	}`)

	graph, err := decodeGraphArtifact(payload, "chunk")
	require.NoError(t, err)
	require.Len(t, graph.Node, 2)
	assert.Equal(t, "A", graph.Node[0].Name)
	require.Len(t, graph.Relation, 2)
	assert.Equal(t, "A", graph.Relation[0].Node1)
	assert.Equal(t, "B", graph.Relation[1].Node1)
}

func TestGraphArtifactCodecCachesEmptyGraph(t *testing.T) {
	payload, err := encodeGraphArtifact(&types.GraphData{})
	require.NoError(t, err)

	graph, err := decodeGraphArtifact(payload, "current-chunk")
	require.NoError(t, err)
	assert.Empty(t, graph.Node)
	assert.Empty(t, graph.Relation)
}

func TestDecodeGraphArtifactRejectsMalformedPayload(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		chunkID string
	}{
		{name: "invalid JSON", payload: `{`, chunkID: "chunk"},
		{name: "unknown field", payload: `{"version":1,"nodes":[],"relations":[],"extra":true}`, chunkID: "chunk"},
		{name: "trailing value", payload: `{"version":1,"nodes":[],"relations":[]} {}`, chunkID: "chunk"},
		{name: "unsupported version", payload: `{"version":2,"nodes":[],"relations":[]}`, chunkID: "chunk"},
		{name: "missing nodes", payload: `{"version":1,"relations":[]}`, chunkID: "chunk"},
		{name: "missing relations", payload: `{"version":1,"nodes":[]}`, chunkID: "chunk"},
		{name: "empty chunk ownership", payload: `{"version":1,"nodes":[],"relations":[]}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := decodeGraphArtifact([]byte(tt.payload), tt.chunkID)
			require.Error(t, err)
		})
	}
}

func TestDecodeGraphArtifactRejectsInvalidUTF8(t *testing.T) {
	payload := append([]byte(`{"version":1,"nodes":[{"name":"`), 0xff)
	payload = append(payload, []byte(`","attributes":[]}],"relations":[]}`)...)

	_, err := decodeGraphArtifact(payload, "chunk")
	require.ErrorContains(t, err, "UTF-8")
}

func TestCompleteGraphArtifactHitSkipsProviderAndRebindsOwnership(t *testing.T) {
	request := testGraphArtifactRequest()
	key, err := newGraphArtifactKey(request)
	require.NoError(t, err)
	payload, err := encodeGraphArtifact(&types.GraphData{
		Node: []*types.GraphNode{{Name: "Alpha"}},
	})
	require.NoError(t, err)
	store := newChatArtifactFakeStore()
	store.values[key] = payload
	providerCalls := 0

	graph, cacheHit, providerCalled, err := completeGraphArtifact(
		context.Background(),
		store,
		request,
		"current-chunk",
		func(context.Context) (*types.GraphData, error) {
			providerCalls++
			return nil, nil
		},
	)

	require.NoError(t, err)
	assert.True(t, cacheHit)
	assert.False(t, providerCalled)
	assert.Zero(t, providerCalls)
	require.Len(t, graph.Node, 1)
	assert.Equal(t, []string{"current-chunk"}, graph.Node[0].Chunks)
	assert.Zero(t, store.putCalls)
}

func TestCompleteGraphArtifactMissUsesPutIfAbsentWinner(t *testing.T) {
	request := testGraphArtifactRequest()
	winner, err := encodeGraphArtifact(&types.GraphData{
		Node: []*types.GraphNode{{Name: "Winner"}},
	})
	require.NoError(t, err)
	store := newChatArtifactFakeStore()
	store.putCanonical = winner

	graph, cacheHit, providerCalled, err := completeGraphArtifact(
		context.Background(),
		store,
		request,
		"current-chunk",
		func(context.Context) (*types.GraphData, error) {
			return &types.GraphData{Node: []*types.GraphNode{{Name: "Candidate"}}}, nil
		},
	)

	require.NoError(t, err)
	assert.False(t, cacheHit)
	assert.True(t, providerCalled)
	require.Len(t, graph.Node, 1)
	assert.Equal(t, "Winner", graph.Node[0].Name)
	assert.Equal(t, []string{"current-chunk"}, graph.Node[0].Chunks)
	assert.Equal(t, 1, store.putCalls)
}

func TestCompleteGraphArtifactMissFallsBackWhenConcurrentWinnerIsCorrupt(t *testing.T) {
	request := testGraphArtifactRequest()
	store := newChatArtifactFakeStore()
	store.putCanonical = []byte(`{`)

	graph, cacheHit, providerCalled, err := completeGraphArtifact(
		context.Background(),
		store,
		request,
		"current-chunk",
		func(context.Context) (*types.GraphData, error) {
			return &types.GraphData{Node: []*types.GraphNode{{Name: "Candidate"}}}, nil
		},
	)

	require.NoError(t, err)
	assert.False(t, cacheHit)
	assert.True(t, providerCalled)
	require.Len(t, graph.Node, 1)
	assert.Equal(t, "Candidate", graph.Node[0].Name)
	assert.Equal(t, []string{"current-chunk"}, graph.Node[0].Chunks)
}

func TestCompleteGraphArtifactCorruptHitRecomputesWithoutReusingWinner(t *testing.T) {
	request := testGraphArtifactRequest()
	key, err := newGraphArtifactKey(request)
	require.NoError(t, err)
	store := newChatArtifactFakeStore()
	store.values[key] = []byte(`{`)

	graph, cacheHit, providerCalled, err := completeGraphArtifact(
		context.Background(),
		store,
		request,
		"current-chunk",
		func(context.Context) (*types.GraphData, error) {
			return &types.GraphData{Node: []*types.GraphNode{{Name: "Fresh"}}}, nil
		},
	)

	require.NoError(t, err)
	assert.False(t, cacheHit)
	assert.True(t, providerCalled)
	require.Len(t, graph.Node, 1)
	assert.Equal(t, "Fresh", graph.Node[0].Name)
	assert.Zero(t, store.putCalls, "immutable corrupt winners cannot be replaced in this stage")
}

func TestCompleteGraphArtifactBypassDoesNotRequireCacheIdentity(t *testing.T) {
	request := testGraphArtifactRequest()
	request.model = nil
	request.modelRevision = ""

	graph, cacheHit, providerCalled, err := completeGraphArtifact(
		context.Background(),
		nil,
		request,
		"current-chunk",
		func(context.Context) (*types.GraphData, error) {
			return &types.GraphData{Node: []*types.GraphNode{{Name: "Fresh"}}}, nil
		},
	)

	require.NoError(t, err)
	assert.False(t, cacheHit)
	assert.True(t, providerCalled)
	assert.Equal(t, []string{"current-chunk"}, graph.Node[0].Chunks)
}

func TestCompleteGraphArtifactPropagatesFailures(t *testing.T) {
	request := testGraphArtifactRequest()
	t.Run("get", func(t *testing.T) {
		want := errors.New("get failed")
		store := newChatArtifactFakeStore()
		store.getErr = want
		_, _, providerCalled, err := completeGraphArtifact(
			context.Background(), store, request, "chunk",
			func(context.Context) (*types.GraphData, error) {
				t.Fatal("provider must not run after a store read failure")
				return nil, nil
			},
		)
		require.ErrorIs(t, err, want)
		assert.False(t, providerCalled)
	})
	t.Run("put", func(t *testing.T) {
		want := errors.New("put failed")
		store := newChatArtifactFakeStore()
		store.putErr = want
		_, _, providerCalled, err := completeGraphArtifact(
			context.Background(), store, request, "chunk",
			func(context.Context) (*types.GraphData, error) { return &types.GraphData{}, nil },
		)
		require.ErrorIs(t, err, want)
		assert.True(t, providerCalled)
	})
	t.Run("provider", func(t *testing.T) {
		want := errors.New("provider failed")
		_, _, providerCalled, err := completeGraphArtifact(
			context.Background(), newChatArtifactFakeStore(), request, "chunk",
			func(context.Context) (*types.GraphData, error) { return nil, want },
		)
		require.ErrorIs(t, err, want)
		assert.True(t, providerCalled)
	})
}

func TestExtractGraphArtifactReusesAcrossCurrentChunkOwnership(t *testing.T) {
	model := &chatArtifactFakeModel{
		modelID:   "model-1",
		modelName: "chat",
		response: &types.ChatResponse{Content: `[
			{"entity":"Alpha","entity_attributes":["person"]},
			{"entity":"Beta","entity_attributes":["person"]},
			{"entity1":"Alpha","entity2":"Beta","relation":"knows"}
		]`},
	}
	store := newChatArtifactFakeStore()
	template := &types.PromptTemplateStructured{
		Description: "Extract entities using these relation tags: %s",
		Tags:        []string{"knows"},
	}

	first, firstHit, firstProviderCalled, err := extractGraphArtifact(
		context.Background(), store, 7, "chunk-1", model, "revision-1", template, "same content",
	)
	require.NoError(t, err)
	second, secondHit, secondProviderCalled, err := extractGraphArtifact(
		context.Background(), store, 7, "chunk-2", model, "revision-1", template, "same content",
	)
	require.NoError(t, err)

	assert.False(t, firstHit)
	assert.True(t, firstProviderCalled)
	assert.True(t, secondHit)
	assert.False(t, secondProviderCalled)
	assert.Equal(t, 1, model.calls)
	assert.Equal(t, []string{"chunk-1"}, first.Node[0].Chunks)
	assert.Equal(t, []string{"chunk-1"}, first.Relation[0].Chunks)
	assert.Equal(t, []string{"chunk-2"}, second.Node[0].Chunks)
	assert.Equal(t, []string{"chunk-2"}, second.Relation[0].Chunks)
	require.NotNil(t, model.gotOptions.Thinking)
	assert.False(t, *model.gotOptions.Thinking)
	assert.Equal(t, 0.3, model.gotOptions.Temperature)
	assert.Equal(t, 4096, model.gotOptions.MaxTokens)
}

func testGraphArtifactRequest() graphArtifactRequest {
	thinking := false
	return graphArtifactRequest{
		tenantID:      7,
		model:         &chatArtifactFakeModel{modelID: "model-1", modelName: "chat"},
		modelRevision: "revision-1",
		messages: []chat.Message{
			{Role: "system", Content: "extract graph"},
			{Role: "user", Content: "chunk content"},
		},
		options: &chat.ChatOptions{
			Temperature: 0.3,
			MaxTokens:   4096,
			Thinking:    &thinking,
		},
		promptVersion:        "graph-prompt-v1",
		canonicalizerVersion: "graph-v1",
	}
}
