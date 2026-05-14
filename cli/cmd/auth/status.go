package auth

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/aiclient"
	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/format"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

// authStatusFields enumerates the fields surfaced for `--json` discovery
// on `auth status`. Single-resource shape: filter applies to data itself.
var authStatusFields = []string{
	"context", "user_id", "email", "tenant_id", "tenant_name",
}

// StatusService is the narrow SDK surface auth status depends on.
type StatusService interface {
	GetCurrentUser(ctx context.Context) (*sdk.CurrentUserResponse, error)
}

// statusResult is the typed payload emitted by `--json`.
type statusResult struct {
	Context    string `json:"context"`
	UserID     string `json:"user_id,omitempty"`
	Email      string `json:"email,omitempty"`
	TenantID   uint64 `json:"tenant_id,omitempty"`
	TenantName string `json:"tenant_name,omitempty"`
}

// NewCmdStatus builds the `weknora auth status` command.
func NewCmdStatus(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the active context, principal, and token state",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			jopts, err := cmdutil.CheckJSONFlags(c)
			if err != nil {
				return err
			}
			cli, err := f.Client()
			if err != nil {
				return err
			}
			return runStatus(c.Context(), jopts, f, cli)
		},
	}
	cmdutil.AddJSONFlags(cmd, authStatusFields)
	aiclient.SetAgentHelp(cmd, "Live-checks the active credential by calling /auth/me. Returns {context, user_id, email, tenant_id, tenant_name}. Errors: auth.unauthenticated when token is invalid or missing (run `auth login` / `auth refresh`).")
	return cmd
}

func runStatus(ctx context.Context, jopts *cmdutil.JSONOptions, f *cmdutil.Factory, svc StatusService) error {
	if svc == nil {
		return cmdutil.NewError(cmdutil.CodeAuthUnauthenticated, "no SDK client available; run `weknora auth login`")
	}
	resp, err := svc.GetCurrentUser(ctx)
	if err != nil {
		return cmdutil.WrapHTTP(err, "fetch current user")
	}
	user := resp.Data.User
	tenant := resp.Data.Tenant

	cfg, err := f.Config()
	if err != nil {
		return err
	}

	if jopts.Enabled() {
		result := statusResult{Context: cfg.CurrentContext}
		if user != nil {
			result.UserID = user.ID
			result.Email = user.Email
			result.TenantID = user.TenantID
		}
		if tenant != nil {
			result.TenantName = tenant.Name
		}
		return format.WriteJSONFiltered(iostreams.IO.Out, result, jopts.Fields, jopts.JQ)
	}

	host := ""
	if c, ok := cfg.Contexts[cfg.CurrentContext]; ok {
		host = c.Host
	}
	fmt.Fprintf(iostreams.IO.Out, "context: %s\n", cfg.CurrentContext)
	fmt.Fprintf(iostreams.IO.Out, "host:    %s\n", host)
	if user != nil {
		fmt.Fprintf(iostreams.IO.Out, "user:    %s (%s)\n", user.Email, user.ID)
		fmt.Fprintf(iostreams.IO.Out, "tenant:  %d", user.TenantID)
		if tenant != nil {
			fmt.Fprintf(iostreams.IO.Out, " (%s)", tenant.Name)
		}
		fmt.Fprintln(iostreams.IO.Out)
	}
	return nil
}
