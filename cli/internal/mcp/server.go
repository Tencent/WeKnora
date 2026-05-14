// Package mcp wires the curated weknora tool set to an
// modelcontextprotocol/go-sdk server. RunStdio is the entry point invoked
// by `weknora mcp serve`.
//
// Design notes:
//
//   - Tool surface is hand-curated rather than auto-derived from the cobra
//     tree (which would expose auth/link/completion/destructive verbs that
//     don't belong on an agent-callable surface).
//   - Long-running tools (chat / agent_invoke) accumulate the LLM SSE
//     stream server-side and return a single CallToolResult — MCP spec
//     2025-06-18 does not define streamed tool-result content, so the
//     accumulate-and-return pattern is the canonical path.
//   - Handlers receive ctx for cancellation; mid-LLM-stream cancellation
//     propagates to the SDK via context, which closes the SSE connection.
package mcp

import (
	"context"
	"fmt"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Tencent/WeKnora/cli/internal/build"
)

// ServiceClient bundles the SDK methods the tool registry needs. *sdk.Client
// satisfies it; tests substitute a fake to exercise the tool handlers
// in-process without standing up a real WeKnora server.
//
// Embedding the full SDK Client would couple every tool test to every SDK
// method; declaring the narrow surface here keeps the seam tight.
type ServiceClient interface {
	knowledgeBaseService
	knowledgeService
	chatService
	agentService
}

// RunStdio constructs the MCP server, registers the curated 9 tools, and
// blocks reading JSON-RPC from stdin until the client disconnects or ctx
// is cancelled. Returns the underlying transport error (if any); the cobra
// RunE caller maps it through the usual cmdutil exit-code path.
func RunStdio(ctx context.Context, svc ServiceClient) error {
	v, _, _ := build.Info()
	server := mcpsdk.NewServer(
		&mcpsdk.Implementation{
			Name:    "weknora",
			Version: v,
		},
		nil,
	)
	registerTools(server, svc)
	if err := server.Run(ctx, &mcpsdk.StdioTransport{}); err != nil {
		return fmt.Errorf("mcp serve: %w", err)
	}
	return nil
}
