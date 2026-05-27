// Package agentcmd holds the `weknora agent` command tree:
// list / view / create / edit / delete / status / check. The directory is
// named `agent/` to match the cobra subcommand; the Go package is `agentcmd`
// to avoid colliding with cobra's *cobra.Command identifier.
//
// "agent" in this subtree refers to WeKnora's user-defined Custom
// Agents (server resource: GET/POST /agents/...) and handles CRUD
// operations only. Agent invocation has moved to `weknora session ask
// --agent <id>` (see cli/cmd/session/ask.go).
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
		Short: "Manage custom agents (CRUD + status/check)",
		Long: `Custom Agents bundle a system prompt, model, tool allow-list, and KB
scope into an addressable resource. Create, edit, list, view, check,
or delete agents. To invoke an agent, use: weknora session ask --agent <id>`,
	}
	cmd.AddCommand(NewCmdList(f))
	cmd.AddCommand(NewCmdView(f))
	cmd.AddCommand(NewCmdCreate(f))
	cmd.AddCommand(NewCmdEdit(f))
	cmd.AddCommand(NewCmdDelete(f))
	cmd.AddCommand(NewCmdStatus(f))
	cmd.AddCommand(NewCmdCheck(f))
	return cmd
}
