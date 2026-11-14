package tools

import (
	"context"
	"encoding/json"

	"github.com/Tencent/WeKnora/internal/types"
)

// MCPCallGuard can return a non-nil result to decline a new MCP invocation
// before it opens an approval or contacts the server. It grants no authority.
// Identity and arguments are the resolved native target, never proxy input.
type MCPCallGuard func(context.Context, string, string, json.RawMessage) (*types.ToolResult, error)

type mcpCallGuardKey struct{}

// SetMCPCallGuard applies only to this engine's registry, including MCP targets
// discovered later and direct tools rebuilt from refreshed service metadata.
func (r *ToolRegistry) SetMCPCallGuard(guard MCPCallGuard) { r.mcpCallGuard = guard }

func withMCPCallGuard(ctx context.Context, guard MCPCallGuard) context.Context {
	if guard == nil {
		return ctx
	}
	return context.WithValue(ctx, mcpCallGuardKey{}, guard)
}

func checkMCPCallGuard(ctx context.Context, service, tool string, args json.RawMessage) (*types.ToolResult, error) {
	guard, _ := ctx.Value(mcpCallGuardKey{}).(MCPCallGuard)
	if guard == nil {
		return nil, nil
	}
	// An already-issued parent approval must finish its own consume/resume
	// lifecycle. This fence prevents new duplicate calls and new prompt cards.
	if exec, ok := ToolExecFromContext(ctx); ok && exec.Answered != nil {
		return nil, nil
	}
	return guard(ctx, service, tool, args)
}
