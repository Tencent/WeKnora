// Package auth holds the cobra commands for authentication
// (login / logout / list / status). Token refresh is deferred to v0.3 once
// the SDK gains automatic 401 retry against the stored refresh_token.
package auth

import (
	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
)

// NewCmdAuth builds the `weknora auth` command tree and registers its
// subcommands. Called from cli/cmd/root.go.
func NewCmdAuth(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage authentication credentials and contexts",
		// NoArgs makes cobra emit its canonical `unknown command "X" for
		// "weknora auth"` for any positional, which mapCobraError tags as
		// FlagError → exit 2. Run (not RunE) is required: a parent with
		// neither Run nor RunE short-circuits to help and skips Args
		// validation entirely.
		Args: cobra.NoArgs,
		Run:  func(c *cobra.Command, _ []string) { _ = c.Help() },
	}
	cmd.AddCommand(NewCmdLogin(f, nil))
	cmd.AddCommand(NewCmdLogout(f))
	cmd.AddCommand(NewCmdList(f))
	cmd.AddCommand(NewCmdStatus(f))
	return cmd
}
