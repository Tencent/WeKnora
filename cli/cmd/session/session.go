// Package sessioncmd holds `weknora session` command tree (list / view /
// delete / ask) for chat history and agent invocation.
//
// Package name `sessioncmd` (not `session`) so callers can `import sdk
// "github.com/Tencent/WeKnora/client"` and use `sdk.Session` without
// shadowing - same hygiene as `profilecmd`.
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
	}
	cmd.AddCommand(NewCmdList(f))
	cmd.AddCommand(NewCmdView(f))
	cmd.AddCommand(NewCmdDelete(f))
	cmd.AddCommand(NewCmdAsk(f))
	cmd.AddCommand(NewCmdContinueStream(f))
	return cmd
}
