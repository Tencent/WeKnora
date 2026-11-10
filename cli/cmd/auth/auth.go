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

// modeFromRefs maps the per-profile TokenRef / APIKeyRef presence to a
// canonical credential-mode token. Bearer wins when both are present -
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
		Short: "Manage authentication credentials and profiles",
	}
	cmd.AddCommand(NewCmdLogin(f, nil))
	cmd.AddCommand(NewCmdLogout(f))
	cmd.AddCommand(NewCmdList(f))
	cmd.AddCommand(NewCmdRefresh(f))
	cmd.AddCommand(NewCmdStatus(f))
	cmd.AddCommand(NewCmdToken(f))
	return cmd
}
