package types

import (
	"context"
	"github.com/Tencent/WeKnora/internal/mcp/headertemplate"
)

const MCPHeaderContextKey ContextKey = "MCPHeaderContext"

func WithMCPHeaderContext(ctx context.Context, snapshot *headertemplate.Context) context.Context {
	return context.WithValue(ctx, MCPHeaderContextKey, snapshot)
}

func MCPHeaderContextFromContext(ctx context.Context) *headertemplate.Context {
	if ctx == nil {
		return nil
	}
	snapshot, _ := ctx.Value(MCPHeaderContextKey).(*headertemplate.Context)
	return snapshot
}
