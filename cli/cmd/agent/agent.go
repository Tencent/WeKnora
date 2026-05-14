// Package agentcmd holds the `weknora agent` command tree:
// list / view / invoke. The directory is named `agent/` (matches cobra
// noun-verb convention) but the Go package is `agentcmd` to avoid
// colliding with cobra's *cobra.Command identifier.
//
// "agent" in this subtree refers to WeKnora's user-defined Custom
// Agents (server resource: GET/POST /agents/...). The CLI's
// `agent invoke` calls /agent-chat/:session_id which dispatches the
// agent's configured workflow (system prompt, allowed tools, KB scope,
// retrieval thresholds).
package agentcmd

import (
	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
)

// NewCmd builds the `weknora agent` parent and registers leaves. Called
// from cli/cmd/root.go.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Manage and invoke custom agents",
		Long: `Custom Agents bundle a system prompt, model, tool allow-list, and KB
scope into an addressable resource. List visible agents, view a single
agent's configuration, or invoke an agent against a query.`,
		Args: cobra.NoArgs,
		Run:  func(c *cobra.Command, _ []string) { _ = c.Help() },
	}
	cmd.AddCommand(NewCmdList(f))
	cmd.AddCommand(NewCmdView(f))
	cmd.AddCommand(NewCmdInvoke(f))
	return cmd
}
