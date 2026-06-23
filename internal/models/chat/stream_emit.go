package chat

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
func (e *thinkingEmitter) emit(ch chan types.StreamResponse, content string) {
	e.active = true
	ch <- types.StreamResponse{
		ResponseType: types.ResponseTypeThinking,
		Content:      content,
		Done:         false,
	}
}

// finish emits the single thinking-done marker if one is owed. Safe to call
// multiple times; only the first call after an emit sends anything.
func (e *thinkingEmitter) finish(ch chan types.StreamResponse) {
	if !e.active {
		return
	}
	e.active = false
	ch <- types.StreamResponse{
		ResponseType: types.ResponseTypeThinking,
		Done:         true,
	}
}
