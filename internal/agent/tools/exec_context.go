package tools

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/event"
)

type execCtxKey struct{}

// ToolExecContext is attached to context during agent tool execution (per tool call).
type ToolExecContext struct {
	SessionID          string
	AssistantMessageID string
	RequestID          string
	ToolCallID         string
	UserID             string // owner of the originating session; used by HITL gates for authorization (issue #1173)
	EventBus           *event.EventBus
	// ApprovalCtx is the parent ctx WITHOUT defaultToolExecTimeout; used when the tool
	// must wait for human approval that may exceed normal tool exec timeout (issue #1173).
	// Falls back to the per-tool execCtx when nil.
	ApprovalCtx context.Context
	// ExecTimeout mirrors the per-tool exec timeout the engine applied to the
	// outer ctx. Tools that legitimately consume that ctx (e.g. MCP human
	// approval) can re-derive a fresh timeout from ApprovalCtx using this
	// value instead of hard-coding a duration. Zero means "fallback to 60s".
	ExecTimeout time.Duration
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
