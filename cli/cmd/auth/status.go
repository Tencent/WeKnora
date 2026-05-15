package auth

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

// authStatusFields enumerates the fields surfaced for `--json` discovery
// on `auth status`. Single-resource shape: filter applies to data itself.
var authStatusFields = []string{
	"context", "user_id", "username", "email", "is_active",
	"can_access_all_tenants", "tenant_id", "tenant_name",
}

// StatusService is the narrow SDK surface auth status depends on.
type StatusService interface {
	GetCurrentUser(ctx context.Context) (*sdk.CurrentUserResponse, error)
}

// statusResult is the typed payload emitted by `--json`. Mirrors the
// SDK AuthUser + AuthTenant projection so agents can branch on
// can_access_all_tenants (cross-tenant admin) and is_active (disabled
// account) without a second round-trip.
type statusResult struct {
	Context             string `json:"context"`
	UserID              string `json:"user_id,omitempty"`
	Username            string `json:"username,omitempty"`
	Email               string `json:"email,omitempty"`
	IsActive            bool   `json:"is_active,omitempty"`
	CanAccessAllTenants bool   `json:"can_access_all_tenants,omitempty"`
	TenantID            uint64 `json:"tenant_id,omitempty"`
	TenantName          string `json:"tenant_name,omitempty"`
}

// NewCmdStatus builds the `weknora auth status` command.
func NewCmdStatus(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the active context, principal, and token state",
		Long: `Live-check the active credential by calling /auth/me. Reports the user
and tenant the server resolves the credential to.

Exits with auth.unauthenticated when the token is invalid or missing - run
` + "`weknora auth login`" + ` (or ` + "`auth refresh`" + ` for JWT contexts) to recover.
For JWT contexts the SDK transparently refreshes on 401, so this command
usually only surfaces a hard auth failure.`,
		Args: cobra.NoArgs,
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
			result.Username = user.Username
			result.Email = user.Email
			result.IsActive = user.IsActive
			result.CanAccessAllTenants = user.CanAccessAllTenants
			result.TenantID = user.TenantID
		}
		if tenant != nil {
			result.TenantName = tenant.Name
		}
		return jopts.Emit(iostreams.IO.Out, result)
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
