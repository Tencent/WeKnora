package auth

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

type RefreshOptions struct {
	Name string // --name: target profile (defaults to current)
}

// authRefreshFields enumerates the fields surfaced for `--format json` discovery
// on `auth refresh`. Token values are intentionally omitted - see refreshResult.
var authRefreshFields = []string{"profile"}

// refreshResult is the typed payload emitted under data on success. Token
// values are intentionally NOT included - emitting them would leak secrets
// into stdout / agent transcripts. Agents needing to verify the new token
// can re-run `weknora auth status` (live API check).
type refreshResult struct {
	Profile string `json:"profile"`
}

// NewCmdRefresh builds `weknora auth refresh`. Renews the JWT access
// token by spending the stored refresh_token via POST /auth/refresh -
// the standard OAuth refresh-token grant.
//
// API-key profiles are rejected - they have no refresh semantic;
// rotate the key via the server UI instead.
func NewCmdRefresh(f *cmdutil.Factory) *cobra.Command {
	opts := &RefreshOptions{}
	cmd := &cobra.Command{
		Use:   "refresh",
		Short: "Renew the JWT access token using the stored refresh token",
		Long: `Reads the refresh token previously stored by ` + "`weknora auth login`" + ` and
exchanges it for a new access + refresh token pair via POST /api/v1/auth/refresh.
Both new tokens replace the existing entries in the OS keyring.

API-key profiles are rejected with input.invalid_argument - they have no
refresh semantic. Rotate the key in the server UI instead.`,
		Example: `  weknora auth refresh                 # refresh the current profile
  weknora auth refresh --name staging  # refresh a specific profile`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			fopts, err := cmdutil.CheckFormatFlag(c)
			if err != nil {
				return err
			}
			fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())
			return runRefresh(c.Context(), opts, fopts, f, defaultRefresher)
		},
	}
	cmd.Flags().StringVar(&opts.Name, "name", "", "Profile to refresh (defaults to the current profile)")
	cmdutil.AddFormatFlag(cmd, authRefreshFields...)
	return cmd
}

// defaultRefresher constructs a fresh, unauthenticated SDK client targeting
// host - the /auth/refresh endpoint reads the refresh token from the body,
// so no bearer / api-key header is needed.
func defaultRefresher(host string) cmdutil.Refresher {
	return sdk.NewClient(host)
}

func runRefresh(ctx context.Context, opts *RefreshOptions, fopts *cmdutil.FormatOptions, f *cmdutil.Factory, refresherFor func(host string) cmdutil.Refresher) error {
	cfg, err := f.Config()
	if err != nil {
		return err
	}
	name := opts.Name
	if name == "" {
		name = cfg.CurrentProfile
	}
	if name == "" {
		return cmdutil.NewError(cmdutil.CodeAuthUnauthenticated,
			"no current profile configured; run `weknora auth login` to set one up")
	}
	c, ok := cfg.Profiles[name]
	if !ok {
		return cmdutil.NewError(cmdutil.CodeLocalProfileNotFound,
			fmt.Sprintf("profile not found: %s", name))
	}
	if c.Host == "" {
		return cmdutil.NewError(cmdutil.CodeLocalConfigCorrupt,
			fmt.Sprintf("profile %q has no host", name))
	}
	if c.RefreshRef == "" {
		hint := "api-key profiles can't be refreshed - rotate the key in the server UI and run `weknora auth login --with-token`"
		if c.APIKeyRef == "" {
			hint = "no refresh token stored - run `weknora auth login` to authenticate"
		}
		return &cmdutil.Error{
			Code:    cmdutil.CodeInputInvalidArgument,
			Message: fmt.Sprintf("profile %q has no refresh token", name),
			Hint:    hint,
		}
	}

	store, err := f.Secrets()
	if err != nil {
		return err
	}
	if _, err := cmdutil.RefreshAndPersist(ctx, store, refresherFor(c.Host), name); err != nil {
		return err
	}

	if fopts.WantsJSON() {
		return fopts.Emit(iostreams.IO.Out, refreshResult{Profile: name}, nil)
	}
	fmt.Fprintf(iostreams.IO.Out, "✓ Refreshed access token for profile %s\n", name)
	return nil
}
