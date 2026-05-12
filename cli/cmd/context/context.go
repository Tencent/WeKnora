// Package contextcmd holds `weknora context` command tree
// (list / add / remove / use). Uses the gh-style `<noun> <verb>` shape
// consistent with the rest of this CLI. kubectl exposes the same set
// of operations as flat hyphenated subcommands (`config get-contexts /
// set-context / delete-context / use-context`) — a different idiom we
// don't adopt because it would make `context` an outlier in our tree.
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
