package service

import (
	"context"
	"sync"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

// chatRequestSnapshot is an immutable copy of Chat calls observed around a
// production ingestion operation.
type chatRequestSnapshot struct {
	RequestCount int
	ErrorCount   int
	TotalItems   int
	InputChars   int
	OutputChars  int
}

// ingestionObservedChat records calls entering the Chat interface while
// delegating unchanged requests and responses to the wrapped model.
type ingestionObservedChat struct {
	inner     chat.Chat
	operation types.IngestionOperation

	mu sync.Mutex

	requestCount int
	errorCount   int
	totalItems   int
	inputChars   int
	outputChars  int
}

var _ chat.Chat = (*ingestionObservedChat)(nil)

func newIngestionObservedChat(
	inner chat.Chat,
	operation types.IngestionOperation,
) *ingestionObservedChat {
	return &ingestionObservedChat{
		inner:     inner,
		operation: operation,
	}
}

func (c *ingestionObservedChat) Chat(
	ctx context.Context,
	messages []chat.Message,
	options *chat.ChatOptions,
) (*types.ChatResponse, error) {
	if types.IngestionOperationFromContext(ctx) == "" {
		ctx = types.WithIngestionOperation(ctx, c.operation)
	}
	tracked := c.recordRequest(ctx, messages)

	response, err := c.inner.Chat(ctx, messages, options)
	if tracked && err != nil {
		c.recordError()
	}
	if tracked && response != nil {
		c.recordOutput(response.Content)
	}
	return response, err
}

func (c *ingestionObservedChat) ChatStream(
	ctx context.Context,
	messages []chat.Message,
	options *chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	if types.IngestionOperationFromContext(ctx) == "" {
		ctx = types.WithIngestionOperation(ctx, c.operation)
	}
	tracked := c.recordRequest(ctx, messages)
	stream, err := c.inner.ChatStream(ctx, messages, options)
	if tracked && err != nil {
		c.recordError()
	}
	return stream, err
}

func (c *ingestionObservedChat) GetModelName() string {
	return c.inner.GetModelName()
}

func (c *ingestionObservedChat) GetModelID() string {
	return c.inner.GetModelID()
}

func (c *ingestionObservedChat) Snapshot() chatRequestSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()

	return chatRequestSnapshot{
		RequestCount: c.requestCount,
		ErrorCount:   c.errorCount,
		TotalItems:   c.totalItems,
		InputChars:   c.inputChars,
		OutputChars:  c.outputChars,
	}
}

func (c *ingestionObservedChat) recordRequest(
	ctx context.Context,
	messages []chat.Message,
) bool {
	operation := types.IngestionOperationFromContext(ctx)
	if operation != c.operation {
		return false
	}
	inputChars := 0
	for _, message := range messages {
		inputChars += len(message.Content)
		for _, part := range message.MultiContent {
			inputChars += len(part.Text)
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.requestCount++
	c.totalItems++
	c.inputChars += inputChars
	return true
}

func (c *ingestionObservedChat) recordOutput(content string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.outputChars += len(content)
}

func (c *ingestionObservedChat) recordError() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.errorCount++
}

func chatOperationOutput(
	operation types.IngestionOperation,
	stage string,
	model chat.Chat,
	observation chatRequestSnapshot,
	success bool,
) types.JSONMap {
	computedItems := 0
	if success {
		computedItems = observation.TotalItems
	}

	return types.IngestionOperationObservation{
		Operation: operation,
		Stage:     stage,
		ModelID:   model.GetModelID(),
		ModelType: "chat",

		OperationCount: 1,
		RequestCount:   observation.RequestCount,
		BatchCount:     observation.RequestCount,

		TotalItems:    observation.TotalItems,
		ComputedItems: computedItems,
		ReusedItems:   0,

		InputChars:  observation.InputChars,
		OutputChars: observation.OutputChars,

		CacheStatus: types.IngestionCacheStatusNotSupported,
		Success:     success,
	}.ToJSONMap()
}

func mergeObservationOutput(output, observation types.JSONMap) {
	for key, value := range observation {
		output[key] = value
	}
}

func logUnspannedChatObservation(
	ctx context.Context,
	operation types.IngestionOperation,
	stage string,
	model chat.Chat,
	observation chatRequestSnapshot,
	metadata types.JSONMap,
) types.JSONMap {
	output := chatOperationOutput(
		operation,
		stage,
		model,
		observation,
		observation.ErrorCount == 0,
	)
	output["observation_sink"] = "structured_log"
	for key, value := range metadata {
		if _, exists := output[key]; !exists {
			output[key] = value
		}
	}
	logger.GetLogger(ctx).
		WithFields(logger.Fields(output)).
		Info("ingestion chat observation")
	return output
}
