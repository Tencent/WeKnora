package auth

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/agent"
	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/format"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

type RefreshOptions struct {
	Name    string // --name: target context (defaults to current)
	JSONOut bool
}

// refreshResult is the typed payload emitted under data on success. Token
// values are intentionally NOT included — emitting them would leak secrets
// into stdout / agent transcripts. Agents needing to verify the new token
// can re-run `weknora auth status` (live API check).
type refreshResult struct {
	Context string `json:"context"`
}

// NewCmdRefresh builds `weknora auth refresh`. Renews the JWT access
// token by spending the stored refresh_token via POST /auth/refresh —
// the standard OAuth refresh-token grant.
//
// API-key contexts are rejected — they have no refresh semantic;
// rotate the key via the server UI instead.
func NewCmdRefresh(f *cmdutil.Factory) *cobra.Command {
	opts := &RefreshOptions{}
	cmd := &cobra.Command{
		Use:   "refresh",
		Short: "Renew the JWT access token using the stored refresh token",
		Long: `Reads the refresh token previously stored by ` + "`weknora auth login`" + ` and
exchanges it for a new access + refresh token pair via POST /api/v1/auth/refresh.
Both new tokens replace the existing entries in the OS keyring.

API-key contexts are rejected with input.invalid_argument — they have no
refresh semantic. Rotate the key in the server UI instead.`,
		Example: `  weknora auth refresh                 # refresh the current context
  weknora auth refresh --name staging  # refresh a specific context`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return runRefresh(c.Context(), opts, f, defaultRefresher)
		},
	}
	cmd.Flags().StringVar(&opts.Name, "name", "", "Context to refresh (defaults to the current context)")
	cmd.Flags().BoolVar(&opts.JSONOut, "json", false, "Output JSON envelope")
	agent.SetAgentHelp(cmd, "Renews the access token using the stored refresh token. Errors with auth.token_expired when refresh itself is rejected — surface the hint to re-run auth login.")
	return cmd
}

// defaultRefresher constructs a fresh, unauthenticated SDK client targeting
// host — the /auth/refresh endpoint reads the refresh token from the body,
// so no bearer / api-key header is needed.
func defaultRefresher(host string) cmdutil.Refresher {
	return sdk.NewClient(host)
}

func runRefresh(ctx context.Context, opts *RefreshOptions, f *cmdutil.Factory, refresherFor func(host string) cmdutil.Refresher) error {
	cfg, err := f.Config()
	if err != nil {
		return err
	}
	name := opts.Name
	if name == "" {
		name = cfg.CurrentContext
	}
	if name == "" {
		return cmdutil.NewError(cmdutil.CodeAuthUnauthenticated,
			"no current context configured; run `weknora auth login` to set one up")
	}
	c, ok := cfg.Contexts[name]
	if !ok {
		return cmdutil.NewError(cmdutil.CodeLocalContextNotFound,
			fmt.Sprintf("context not found: %s", name))
	}
	if c.Host == "" {
		return cmdutil.NewError(cmdutil.CodeLocalConfigCorrupt,
			fmt.Sprintf("context %q has no host", name))
	}
	if c.RefreshRef == "" {
		hint := "api-key contexts can't be refreshed — rotate the key in the server UI and run `weknora auth login --with-token`"
		if c.APIKeyRef == "" {
			hint = "no refresh token stored — run `weknora auth login` to authenticate"
		}
		return &cmdutil.Error{
			Code:    cmdutil.CodeInputInvalidArgument,
			Message: fmt.Sprintf("context %q has no refresh token", name),
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

	if opts.JSONOut {
		return format.WriteEnvelope(iostreams.IO.Out,
			format.Success(refreshResult{Context: name}, &format.Meta{Context: name}))
	}
	fmt.Fprintf(iostreams.IO.Out, "✓ Refreshed access token for context %s\n", name)
	return nil
}
