package mcpcmd

import (
	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/agent"
	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	mcpserver "github.com/Tencent/WeKnora/cli/internal/mcp"
)

// NewCmdServe builds `weknora mcp serve`. Currently stdio-only; HTTP
// (streamable / SSE) is roadmap 5-7.
func NewCmdServe(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run an MCP server over stdio",
		Long: `Speaks JSON-RPC 2.0 on stdin/stdout to an MCP client. Logs go to
stderr; the data channel is reserved for protocol traffic.

Authentication is inherited from the active context (or --context). The
server eagerly resolves the SDK client at startup — if no context is
configured, the process exits with auth.unauthenticated before any MCP
handshake. This way an IDE-side agent sees a clear failure mode rather
than a server that handshakes successfully then errors on every tool.

To use with Claude Code, add to ~/.claude/mcp_servers.json:

    {
      "weknora": {
        "command": "weknora",
        "args": ["mcp", "serve"]
      }
    }`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			// Eagerly construct the SDK client. Surfaces auth /
			// configuration problems before any MCP handshake.
			cli, err := f.Client()
			if err != nil {
				return err
			}
			return mcpserver.RunStdio(c.Context(), cli)
		},
	}
	agent.SetAgentHelp(cmd, "Long-lived stdio MCP server. Reads JSON-RPC requests from stdin, writes responses to stdout, logs to stderr. Surfaces 9 read-only tools (kb_list/kb_view/doc_list/doc_view/doc_download/search_chunks/chat/agent_list/agent_invoke); destructive verbs are intentionally excluded. Auth is inherited from the active context — to switch, exit and re-launch with --context. Long tools (chat / agent_invoke) accumulate the LLM stream server-side and return a single CallToolResult — no MCP streaming-content extension, per spec 2025-06-18 (tools.mdx).")
	return cmd
}
