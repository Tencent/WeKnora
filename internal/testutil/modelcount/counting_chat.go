package modelcount

import (
	"context"
	"errors"
	"sync"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

// ChatCall contains non-sensitive metadata for one call entering chat.Chat.
type ChatCall struct {
	Operation    types.IngestionOperation
	MessageCount int
	InputChars   int
	Streaming    bool
}

// ChatSnapshot is an immutable copy of calls observed by CountingChat.
type ChatSnapshot struct {
	RequestCount       int
	StreamRequestCount int
	TotalInputMessages int
	InputChars         int
	OutputChars        int
	Calls              []ChatCall
}

// CountingChat is a thread-safe chat fake for ingestion observation tests.
// It records only counts and sizes; prompts and message bodies are never kept.
type CountingChat struct {
	mu sync.Mutex

	ModelName string
	ModelID   string
	Response  string
	Err       error
	FailOn    int

	requestCount       int
	streamRequestCount int
	totalInputMessages int
	inputChars         int
	outputChars        int
	calls              []ChatCall
}

var _ chat.Chat = (*CountingChat)(nil)

func (c *CountingChat) Chat(
	ctx context.Context,
	messages []chat.Message,
	_ *chat.ChatOptions,
) (*types.ChatResponse, error) {
	requestNumber := c.recordRequest(ctx, messages, false)
	if err := c.requestError(requestNumber); err != nil {
		return nil, err
	}

	c.mu.Lock()
	response := c.Response
	c.outputChars += len(response)
	c.mu.Unlock()
	return &types.ChatResponse{Content: response}, nil
}

func (c *CountingChat) ChatStream(
	ctx context.Context,
	messages []chat.Message,
	_ *chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	requestNumber := c.recordRequest(ctx, messages, true)
	if err := c.requestError(requestNumber); err != nil {
		return nil, err
	}

	c.mu.Lock()
	response := c.Response
	c.outputChars += len(response)
	c.mu.Unlock()

	result := make(chan types.StreamResponse, 1)
	result <- types.StreamResponse{Content: response, Done: true}
	close(result)
	return result, nil
}

func (c *CountingChat) GetModelName() string {
	if c.ModelName == "" {
		return "counting-chat"
	}
	return c.ModelName
}

func (c *CountingChat) GetModelID() string {
	if c.ModelID == "" {
		return "counting-chat"
	}
	return c.ModelID
}

func (c *CountingChat) Snapshot() ChatSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()

	calls := make([]ChatCall, len(c.calls))
	copy(calls, c.calls)
	return ChatSnapshot{
		RequestCount:       c.requestCount,
		StreamRequestCount: c.streamRequestCount,
		TotalInputMessages: c.totalInputMessages,
		InputChars:         c.inputChars,
		OutputChars:        c.outputChars,
		Calls:              calls,
	}
}

func (c *CountingChat) recordRequest(
	ctx context.Context,
	messages []chat.Message,
	streaming bool,
) int {
	inputChars := 0
	for _, message := range messages {
		inputChars += len(message.Content)
		for _, part := range message.MultiContent {
			inputChars += len(part.Text)
		}
	}

	operation := types.IngestionOperationFromContext(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requestCount++
	if streaming {
		c.streamRequestCount++
	}
	c.totalInputMessages += len(messages)
	c.inputChars += inputChars
	c.calls = append(c.calls, ChatCall{
		Operation:    operation,
		MessageCount: len(messages),
		InputChars:   inputChars,
		Streaming:    streaming,
	})
	return c.requestCount
}

func (c *CountingChat) requestError(requestNumber int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.FailOn > 0 && requestNumber == c.FailOn {
		return errors.New("counting chat configured request failure")
	}
	return c.Err
}
