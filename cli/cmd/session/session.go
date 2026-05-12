// Package sessioncmd holds `weknora session` command tree (list / view /
// delete) for chat history.
//
// Package name `sessioncmd` (not `session`) so callers can `import sdk
// "github.com/Tencent/WeKnora/client"` and use `sdk.Session` without
// shadowing — same hygiene as `contextcmd`.
package sessioncmd

import (
	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
)

// NewCmd builds the `weknora session` parent command.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "Manage chat sessions",
		Args:  cobra.NoArgs,
		Run:   func(c *cobra.Command, _ []string) { _ = c.Help() },
	}
	cmd.AddCommand(NewCmdList(f))
	cmd.AddCommand(NewCmdView(f))
	cmd.AddCommand(NewCmdDelete(f))
	return cmd
}
