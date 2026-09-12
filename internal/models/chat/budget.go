package chat

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

// budgetChat applies the configured model ceiling to every generation path,
// including compaction and final-answer synthesis.
type budgetChat struct {
	inner         Chat
	contextWindow int
	maxOutput     int
}

func (c *budgetChat) OutputTokenLimit() int { return c.maxOutput }

func (c *budgetChat) options(opts *ChatOptions) *ChatOptions {
	bounded := ChatOptions{}
	if opts != nil {
		bounded = *opts
	}
	limit := c.maxOutput
	if c.contextWindow > 0 && (limit == 0 || limit > c.contextWindow) {
		limit = c.contextWindow
	}
	if limit > 0 {
		if bounded.MaxTokens <= 0 || bounded.MaxTokens > limit {
			bounded.MaxTokens = limit
		}
		if bounded.MaxCompletionTokens <= 0 || bounded.MaxCompletionTokens > limit {
			bounded.MaxCompletionTokens = limit
		}
	}
	return &bounded
}

func (c *budgetChat) Chat(ctx context.Context, messages []Message, opts *ChatOptions) (*types.ChatResponse, error) {
	return c.inner.Chat(ctx, messages, c.options(opts))
}

func (
	c *budgetChat,
) ChatStream(
	ctx context.Context,
	messages []Message,
	opts *ChatOptions,
) (
	<-chan types.StreamResponse,
	error,
) {
	return c.inner.ChatStream(ctx, messages, c.options(opts))
}

func validateBudget(config *ChatConfig) error {
	if config.ContextWindow < 0 || config.MaxOutputTokens < 0 {
		return fmt.Errorf("model context and output token limits must be non-negative")
	}
	if config.ContextWindow > 0 && config.MaxOutputTokens > config.ContextWindow {
		return fmt.Errorf("model output token limit exceeds context window")
	}
	return nil
}

func (c *budgetChat) GetModelName() string { return c.inner.GetModelName() }
func (c *budgetChat) GetModelID() string   { return c.inner.GetModelID() }

// RequestAccountingSupported reports whether the wrapped provider accounts for physical requests.
func (c *budgetChat) RequestAccountingSupported() bool {
	inner, ok := c.inner.(interface{ RequestAccountingSupported() bool })
	return ok && inner.RequestAccountingSupported()
}
