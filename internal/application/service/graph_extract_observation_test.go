package service

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type graphObservationChat struct {
	mu sync.Mutex

	response  string
	err       error
	requests  int
	operation types.IngestionOperation
}

func (c *graphObservationChat) Chat(
	ctx context.Context,
	_ []chat.Message,
	_ *chat.ChatOptions,
) (*types.ChatResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.requests++
	c.operation = types.IngestionOperationFromContext(ctx)
	if c.err != nil {
		return nil, c.err
	}
	return &types.ChatResponse{Content: c.response}, nil
}

func (c *graphObservationChat) ChatStream(
	context.Context,
	[]chat.Message,
	*chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	return nil, nil
}

func (c *graphObservationChat) GetModelName() string {
	return "graph-observation-chat"
}

func (c *graphObservationChat) GetModelID() string {
	return "graph-chat-test"
}

func (c *graphObservationChat) Snapshot() (
	int,
	types.IngestionOperation,
) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.requests, c.operation
}

func TestGraphExtractObservation_RecordsRealChatRequest(
	t *testing.T,
) {
	model := &graphObservationChat{
		response: `[
  {"entity":"Alice","entity_attributes":["person"]},
  {"entity":"Bob","entity_attributes":["person"]},
  {"entity1":"Alice","entity2":"Bob","relation":"knows"}
]`,
	}
	template := &types.PromptTemplateStructured{
		Description: "Extract a graph from the supplied content.",
	}

	graph, output, err := extractGraphWithObservation(
		context.Background(),
		model,
		template,
		"Alice knows Bob.",
	)
	require.NoError(t, err)
	require.NotNil(t, graph)
	require.Len(t, graph.Node, 2)
	require.Len(t, graph.Relation, 1)

	requests, operation := model.Snapshot()
	require.Equal(t, 1, requests)
	require.Equal(
		t,
		types.IngestionOperationGraphExtractChunk,
		operation,
	)
	require.Equal(
		t,
		string(types.IngestionOperationGraphExtractChunk),
		output["operation"],
	)
	require.Equal(t, types.StagePostProcess, output["stage"])
	require.Equal(t, "chat", output["model_type"])
	require.EqualValues(t, requests, output["request_count"])
	require.EqualValues(t, 1, output["total_items"])
	require.EqualValues(t, 1, output["computed_items"])
	require.EqualValues(t, 0, output["reused_items"])
	require.Equal(
		t,
		string(types.IngestionCacheStatusNotSupported),
		output["cache_status"],
	)
	require.Equal(t, true, output["success"])
	require.Positive(t, output["input_chars"])
	require.Positive(t, output["output_chars"])
}

func TestGraphExtractObservation_FailurePreservesRequestCount(
	t *testing.T,
) {
	expectedError := errors.New("graph chat provider failed")
	model := &graphObservationChat{err: expectedError}
	template := &types.PromptTemplateStructured{
		Description: "Extract a graph from the supplied content.",
	}

	graph, output, err := extractGraphWithObservation(
		context.Background(),
		model,
		template,
		"Alice knows Bob.",
	)
	require.ErrorIs(t, err, expectedError)
	require.Nil(t, graph)

	requests, operation := model.Snapshot()
	require.Equal(t, 1, requests)
	require.Equal(
		t,
		types.IngestionOperationGraphExtractChunk,
		operation,
	)
	require.EqualValues(t, requests, output["request_count"])
	require.EqualValues(t, 1, output["total_items"])
	require.EqualValues(t, 0, output["computed_items"])
	require.Equal(t, false, output["success"])
}
