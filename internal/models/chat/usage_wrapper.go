package chat

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
)

type usageChat struct {
	inner  Chat
	record func(context.Context, types.ModelTokenUsage) error
}

func (c *usageChat) GetModelID() string   { return c.inner.GetModelID() }
func (c *usageChat) GetModelName() string { return c.inner.GetModelName() }

func (c *usageChat) startUsage(ctx context.Context) types.ModelTokenUsage {
	tenantID, _ := ctx.Value(types.SessionTenantIDContextKey).(uint64)
	if tenantID == 0 {
		tenantID = types.CallerFromContext(ctx).TenantID
	}
	if tenantID == 0 {
		return types.ModelTokenUsage{}
	}
	now := time.Now().UTC()
	return types.ModelTokenUsage{
		ID: uuid.NewString(), TenantID: tenantID,
		ModelID: c.GetModelID(), ModelName: c.GetModelName(),
		StartedAt: now, UsageDate: now.Format(time.DateOnly),
	}
}

func (c *usageChat) finishUsage(ctx context.Context, entry types.ModelTokenUsage, usage *types.TokenUsage) {
	if usage != nil {
		entry.UsageKnown = true
		entry.InputTokens = usage.PromptTokens
		entry.OutputTokens = usage.CompletionTokens
		entry.TotalTokens = usage.TotalTokens
	}
	// A canceled request can still contain provider-reported usage.
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	if err := c.record(writeCtx, entry); err != nil {
		logger.Warnf(ctx, "record model token usage (call %s): %v", entry.ID, err)
	}
}

func (c *usageChat) Chat(ctx context.Context, messages []Message, opts *ChatOptions) (*types.ChatResponse, error) {
	entry := c.startUsage(ctx)
	resp, err := c.inner.Chat(ctx, messages, opts)
	if entry.ID != "" {
		entry.Completed = err == nil && resp != nil && resp.FinishReason != types.FinishReasonIncomplete
		var usage *types.TokenUsage
		if resp != nil {
			u := &resp.Usage
			if u.PromptTokens != 0 || u.CompletionTokens != 0 || u.TotalTokens != 0 {
				usage = u
			}
		}
		c.finishUsage(ctx, entry, usage)
	}
	return resp, err
}

func (c *usageChat) ChatStream(
	ctx context.Context, messages []Message, opts *ChatOptions,
) (<-chan types.StreamResponse, error) {
	entry := c.startUsage(ctx)
	ch, err := c.inner.ChatStream(ctx, messages, opts)
	if entry.ID == "" {
		return ch, err
	}
	if err != nil || ch == nil {
		c.finishUsage(ctx, entry, nil)
		return ch, err
	}
	out := make(chan types.StreamResponse)
	go func() {
		defer close(out)
		var usage *types.TokenUsage
		var interrupted bool
		for chunk := range ch {
			if chunk.Usage != nil {
				snapshot := *chunk.Usage
				usage = &snapshot
			}
			interrupted = interrupted || chunk.ResponseType == types.ResponseTypeError ||
				chunk.FinishReason == types.FinishReasonIncomplete
			if chunk.Done && chunk.ResponseType == types.ResponseTypeAnswer {
				entry.Completed = true
			}
			// Keep observing the provider channel after cancellation. Forwarding
			// stops when the request ends, but already received usage still counts.
			select {
			case out <- chunk:
			case <-ctx.Done():
			}
		}
		entry.Completed = entry.Completed && !interrupted && ctx.Err() == nil
		c.finishUsage(ctx, entry, usage)
	}()
	return out, nil
}
