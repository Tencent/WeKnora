// Package configcmd holds the `weknora config` command tree.
//
// Currently a single read-only `view` subcommand that prints the resolved
// CLI configuration and its resolution chain. No mutation, no network.
//
// Package name `configcmd` (not `config`) avoids colliding with the
// internal/config package and matches the cmd-subpackage naming pattern.
// The cobra Use: string is "config" — what users type.
package configcmd

import (
	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
)

// NewCmd builds the `weknora config` parent command.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect the CLI's resolved configuration",
	}
	cmd.AddCommand(NewCmdView(f))
	return cmd
}
