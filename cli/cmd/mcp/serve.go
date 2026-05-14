package mcpcmd

import (
	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/aiclient"
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
	aiclient.SetAgentHelp(cmd, "Long-lived stdio MCP server. stdin/stdout for JSON-RPC, stderr for logs. Exposes 9 read-only tools; destructive verbs intentionally excluded. Auth inherited from active context — exit and relaunch with --context to switch. chat / agent_invoke accumulate server-side and return a single result (MCP spec has no streaming-content extension).")
	return cmd
}
