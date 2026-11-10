package tools

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
)

type execCtxKey struct{}

// ToolExecContext is attached to context during agent tool execution (per tool call).
type ToolExecContext struct {
	SessionID          string
	AssistantMessageID string
	RequestID          string
	ToolCallID         string
	UserID             string // principal storage ID of the originating session; used by HITL gates for authorization (issue #1173)
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
	// Snapshot, when set, captures the run so it can be resumed from this
	// call. A tool that raises a suspendable prompt passes the result along;
	// nil means the run cannot be suspended and prompts wait synchronously.
	Snapshot func() (data []byte, version int, err error)
	// Answered is set on the call a resumed run was suspended on: the prompt
	// it raised, now answered. The tool acts on the answer instead of
	// raising the prompt again.
	Answered *types.AgentPrompt
	// Mode says whether the client can show and answer prompts; IM and
	// MCP-server runs have no such surface.
	Mode types.RunMode
}

// Attended reports whether a client can answer prompts now.
func (m *ToolExecContext) Attended() bool { return m != nil && m.Mode.Attended() }

// CanAsk reports whether the run may raise an approval, now or by suspending
// until its owner answers.
func (m *ToolExecContext) CanAsk() bool {
	return m != nil && m.Mode.Normalize() != types.RunModeUnattended
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
