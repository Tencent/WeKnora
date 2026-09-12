package chat

import (
	"context"
	"time"
)

import "github.com/Tencent/WeKnora/internal/types"

// thinkingEmitter owns the "reasoning then answer" hand-off that every
// streaming Chat implementation shares: thinking chunks are forwarded as they
// arrive, and exactly one thinking-done marker is emitted before the first
// answer token (or when the stream ends without one). Centralizing the
// bookkeeping keeps the OpenAI-compatible and Ollama stream loops in sync.
type thinkingEmitter struct {
	active bool
}

// emit forwards a reasoning chunk and records that a thinking-done marker is
// still owed.
func (e *thinkingEmitter) emit(ctx context.Context, ch chan types.StreamResponse, content string) {
	e.active = true
	emitStream(ctx, ch, types.StreamResponse{
		ResponseType: types.ResponseTypeThinking,
		Content:      content,
		Done:         false,
	})
}

// finish emits the single thinking-done marker if one is owed. Safe to call
// multiple times; only the first call after an emit sends anything.
func (e *thinkingEmitter) finish(ctx context.Context, ch chan types.StreamResponse) {
	if !e.active {
		return
	}
	e.active = false
	emitStream(ctx, ch, types.StreamResponse{
		ResponseType: types.ResponseTypeThinking,
		Done:         true,
	})
}

// emitStream releases blocked producers when their consumer cancels.
func emitStream(ctx context.Context, ch chan types.StreamResponse, response types.StreamResponse) bool {
	if response.Done && response.ResponseType != types.ResponseTypeThinking && ctx.Err() != nil {
		timer := time.NewTimer(100 * time.Millisecond)
		defer timer.Stop()
		select {
		case ch <- response:
			return true
		case <-timer.C:
			return false
		}
	}
	select {
	case ch <- response:
		return true
	case <-ctx.Done():
		return false
	}
}
