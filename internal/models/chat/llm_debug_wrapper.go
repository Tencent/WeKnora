package chat

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// debugChat wraps a Chat implementation and logs all calls to the LLM debug log.
// It works with both RemoteAPIChat and OllamaChat (or any future Chat impl).
type debugChat struct {
	inner Chat
}

func (d *debugChat) GetModelName() string { return d.inner.GetModelName() }
func (d *debugChat) GetModelID() string   { return d.inner.GetModelID() }

func (d *debugChat) Chat(ctx context.Context, messages []Message, opts *ChatOptions) (*types.ChatResponse, error) {
	callStart := time.Now()
	resp, err := d.inner.Chat(ctx, messages, opts)
	logLLMDebugCall(ctx, d.inner.GetModelName(), messages, opts, resp, err, time.Since(callStart))
	return resp, err
}

func (d *debugChat) ChatStream(ctx context.Context, messages []Message, opts *ChatOptions) (<-chan types.StreamResponse, error) {
	callStart := time.Now()
	ch, err := d.inner.ChatStream(ctx, messages, opts)
	if err != nil {
		logLLMDebugStream(ctx, d.inner.GetModelName(), messages, opts, "", nil, nil, err, time.Since(callStart))
		return ch, err
	}
	if ch == nil {
		return nil, nil
	}

	wrapped := make(chan types.StreamResponse)
	go func() {
		defer close(wrapped)
		var content strings.Builder
		var usage *types.TokenUsage
		var toolCalls []types.LLMToolCall
		var streamErr error

		for resp := range ch {
			if resp.ResponseType == types.ResponseTypeAnswer && resp.Content != "" {
				content.WriteString(resp.Content)
			}
			if resp.ResponseType == types.ResponseTypeError {
				streamErr = fmt.Errorf("%s", resp.Content)
			}
			if resp.Usage != nil {
				usage = resp.Usage
			}
			if len(resp.ToolCalls) > 0 {
				toolCalls = resp.ToolCalls
			}
			wrapped <- resp
		}

		logLLMDebugStream(ctx, d.inner.GetModelName(), messages, opts,
			content.String(), toolCalls, usage, streamErr, time.Since(callStart))
	}()

	return wrapped, nil
}

// wrapChatDebug wraps a Chat if LLM debug logging is enabled.
func wrapChatDebug(c Chat, err error) (Chat, error) {
	if err != nil || !logger.LLMDebugEnabled() {
		return c, err
	}
	return &debugChat{inner: c}, nil
}
