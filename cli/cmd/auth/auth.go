// Package auth holds the cobra commands for authentication
// (login / logout / list / refresh / status / token).
package auth

import (
	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
)

// Credential-mode tokens used in the JSON output of auth list / login /
// status / token. The string names describe the HTTP credential type rather
// than the login flow (e.g. JWT → bearer regardless of whether it was
// obtained via password or refresh) so an agent can branch directly on the
// header to construct: `Authorization: Bearer <token>` (ModeBearer) or
// `X-API-Key: <token>` (ModeAPIKey).
const (
	ModeBearer  = "bearer"
	ModeAPIKey  = "api-key"
	ModeUnknown = "unknown"
)

// modeFromRefs maps the per-context TokenRef / APIKeyRef presence to a
// canonical credential-mode token. Bearer wins when both are present —
// matches the precedence in cmdutil.buildClient.
func modeFromRefs(apiKeyRef, tokenRef string) string {
	switch {
	case tokenRef != "":
		return ModeBearer
	case apiKeyRef != "":
		return ModeAPIKey
	default:
		return ModeUnknown
	}
}

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
	cmd.AddCommand(NewCmdRefresh(f))
	cmd.AddCommand(NewCmdStatus(f))
	cmd.AddCommand(NewCmdToken(f))
	return cmd
}
