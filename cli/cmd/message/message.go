// Package messagecmd implements `weknora message` — inspect and manage the
// messages inside chat sessions (the multi-turn substrate behind session ask).
package messagecmd

import (
	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
)

// NewCmd builds the `weknora message` command group.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "message",
		Short: "Inspect and manage messages inside chat sessions",
	}
	cmd.AddCommand(NewCmdList(f))
	cmd.AddCommand(NewCmdSearch(f))
	cmd.AddCommand(NewCmdDelete(f))
	return cmd
}
