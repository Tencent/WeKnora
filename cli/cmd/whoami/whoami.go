// Package whoami implements `weknora whoami` — a top-level standalone leaf,
// the simplified counterpart of `auth status` that prints only user_id and
// tenant_id (spec §1.2). Use `auth status` for full diagnostics.
package whoami

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

// Options captures the (sparse) configuration of `weknora whoami`.
type Options struct {
	JSONOut bool
}

// Service is the narrow SDK surface this command depends on.
type Service interface {
	GetCurrentUser(ctx context.Context) (*sdk.CurrentUserResponse, error)
}

// result is the typed payload emitted by `--json`.
type result struct {
	UserID   string `json:"user_id"`
	TenantID uint64 `json:"tenant_id,omitempty"`
}

// NewCmd builds the `weknora whoami` command.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{}
	cmd := &cobra.Command{
		Use:   "whoami",
		Short: "Print user_id and tenant_id of the active context",
		RunE: func(c *cobra.Command, _ []string) error {
			cli, err := f.Client()
			if err != nil {
				return err
			}
			return runWhoami(c.Context(), opts, cli)
		},
	}
	cmd.Flags().BoolVar(&opts.JSONOut, "json", false, "Output JSON envelope")
	agent.SetAgentHelp(cmd, "Returns the active context principal as JSON envelope. Quick identity check; for full diagnostics use `weknora auth status`.")
	return cmd
}

func runWhoami(ctx context.Context, opts *Options, svc Service) error {
	if svc == nil {
		return cmdutil.NewError(cmdutil.CodeAuthUnauthenticated, "no SDK client available; run `weknora auth login`")
	}
	resp, err := svc.GetCurrentUser(ctx)
	if err != nil {
		return cmdutil.Wrapf(cmdutil.ClassifyHTTPError(err), err, "fetch current user")
	}
	if resp == nil || resp.Data.User == nil {
		return cmdutil.NewError(cmdutil.CodeAuthUnauthenticated, "server returned empty user; run `weknora auth login`")
	}
	r := result{UserID: resp.Data.User.ID}
	if resp.Data.Tenant != nil {
		r.TenantID = resp.Data.Tenant.ID
	}
	if opts.JSONOut {
		return cmdutil.NewJSONExporter().Write(iostreams.IO.Out, format.Success(r, nil))
	}
	fmt.Fprintf(iostreams.IO.Out, "user_id:   %s\n", r.UserID)
	if r.TenantID != 0 {
		fmt.Fprintf(iostreams.IO.Out, "tenant_id: %d\n", r.TenantID)
	}
	return nil
}
