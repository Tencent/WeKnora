package tools

import (
	"context"

	"github.com/Tencent/WeKnora/internal/event"
)

type execCtxKey struct{}

// ToolExecContext is attached to context during agent tool execution (per tool call).
type ToolExecContext struct {
	SessionID          string
	AssistantMessageID string
	RequestID          string
	ToolCallID         string
	EventBus           *event.EventBus
	// ApprovalCtx is the parent ctx WITHOUT defaultToolExecTimeout; used when the tool
	// must wait for human approval that may exceed normal tool exec timeout (issue #1173).
	// Falls back to the per-tool execCtx when nil.
	ApprovalCtx context.Context
}

// WithToolExecContext returns ctx that carries ToolExecContext for MCP approval and similar features.
func WithToolExecContext(ctx context.Context, meta *ToolExecContext) context.Context {
	if meta == nil {
		return ctx
	}
	return context.WithValue(ctx, execCtxKey{}, meta)
}

// ToolExecFromContext returns metadata attached by the agent engine, if any.
func ToolExecFromContext(ctx context.Context) (*ToolExecContext, bool) {
	v := ctx.Value(execCtxKey{})
	if v == nil {
		return nil, false
	}
	meta, ok := v.(*ToolExecContext)
	return meta, ok && meta != nil
}
