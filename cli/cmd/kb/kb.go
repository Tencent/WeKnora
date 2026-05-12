// Package kb holds the `weknora kb` command tree: list / view / create /
// edit / delete / pin / unpin / empty. Verb set follows common CRUD
// vocabulary (list/view/create/edit/delete) plus pin/unpin and empty.
package kb

import (
	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
)

// NewCmd builds the `weknora kb` parent command.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "kb",
		Short: "Manage knowledge bases",
		Args:  cobra.NoArgs,
		Run:   func(c *cobra.Command, _ []string) { _ = c.Help() },
	}
	cmd.AddCommand(NewCmdList(f))
	cmd.AddCommand(NewCmdView(f))
	cmd.AddCommand(NewCmdCreate(f))
	cmd.AddCommand(NewCmdEdit(f))
	cmd.AddCommand(NewCmdDelete(f))
	cmd.AddCommand(NewCmdPin(f))
	cmd.AddCommand(NewCmdUnpin(f))
	cmd.AddCommand(NewCmdEmpty(f))
	return cmd
}
