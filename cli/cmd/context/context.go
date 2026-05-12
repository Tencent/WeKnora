// Package contextcmd holds `weknora context` command tree
// (list / add / remove / use). Uses the `<noun> <verb>` shape
// consistent with the rest of this CLI.
//
// Package name `contextcmd` (not `context`) to avoid shadowing stdlib context.
// The cobra Use: string is "context" — this is what users type.
package contextcmd

import (
	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
)

// NewCmd builds the `weknora context` parent command.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "context",
		Short: "Manage CLI contexts (named connection targets)",
		Args:  cobra.NoArgs,
		Run:   func(c *cobra.Command, _ []string) { _ = c.Help() },
	}
	cmd.AddCommand(NewCmdList(f))
	cmd.AddCommand(NewCmdAdd(f))
	cmd.AddCommand(NewCmdRemove(f))
	cmd.AddCommand(NewCmdUse(f))
	return cmd
}
