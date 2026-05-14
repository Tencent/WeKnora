// Package mcpcmd holds the `weknora mcp` command tree.
//
// MCP (Model Context Protocol; https://spec.modelcontextprotocol.io/) is
// the JSON-RPC 2.0 wire protocol agentic IDEs use to call external tools.
// `weknora mcp serve` exposes a curated subset of the CLI's read surface
// as MCP tools so an IDE-side agent can list / view / search / chat against
// the user's active WeKnora context without shelling out to the CLI per
// call.
//
// Package name is `mcpcmd` to avoid shadowing `cli/internal/mcp` (the
// transport-and-handlers implementation). Same naming hygiene as
// `agentcmd` / `sessioncmd`.
package mcpcmd

import (
	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
)

// NewCmd builds the `weknora mcp` parent. Called from cli/cmd/root.go.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Run weknora as a Model Context Protocol server",
		Long: `Exposes weknora's read surface as MCP tools so agentic IDE clients
(Claude Code, Cursor, Continue, Zed) can call them over JSON-RPC.

Initial tool surface is read-only and curated: kb_list / kb_view /
doc_list / doc_view / doc_download / search_chunks / chat / agent_list /
agent_invoke. Destructive verbs (create / delete / upload) are deliberately
excluded — the agent should ask the user before mutating; the CLI's
exit-10 protocol covers that path.`,
		Args: cobra.NoArgs,
		Run:  func(c *cobra.Command, _ []string) { _ = c.Help() },
	}
	cmd.AddCommand(NewCmdServe(f))
	return cmd
}
