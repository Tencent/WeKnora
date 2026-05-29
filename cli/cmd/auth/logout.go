package auth

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/config"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/secrets"
)

type LogoutOptions struct {
	Name   string // --name: target a specific profile (default: current)
	All    bool   // --all: clear every profile
	Yes    bool   // sourced from the global -y/--yes persistent flag
	DryRun bool
}

// authLogoutFields enumerates the fields surfaced for `--format json` discovery
// on `auth logout`. The result is the list of profile names that were
// logged out.
var authLogoutFields = []string{"removed"}

// logoutResult is the typed payload emitted under data.
type logoutResult struct {
	Removed []string `json:"removed"`
}

// NewCmdLogout builds `weknora auth logout`. Clears stored credentials
// (keyring + file fallback) and removes the profile entry from config.yaml.
// No server-side revocation - local-only credential clear.
func NewCmdLogout(f *cmdutil.Factory) *cobra.Command {
	opts := &LogoutOptions{}
	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Remove stored credentials for a profile",
		Long: `Clear keyring + file-fallback secrets for one profile (or all of
them with --all) and drop the profile entry from ~/.config/weknora/config.yaml.

Note: this does NOT revoke the credential server-side - for API keys, you
must rotate them in the server UI; for JWT, the token will continue to be
accepted until it expires.`,
		Example: `  weknora auth logout                       # current profile
  weknora auth logout --name staging        # specific profile
  weknora auth logout --all`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			fopts, err := cmdutil.CheckFormatFlag(c)
			if err != nil {
				return err
			}
			fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())
			opts.Yes, _ = c.Flags().GetBool("yes")
			// Pure-local validation runs before the dry-run gate so --dry-run
			// rejects identically to the live path. Same typed errors as
			// runLogout (kept there for direct-call callers).
			cfg, cfgErr := f.Config()
			if cfgErr != nil {
				return cfgErr
			}
			if len(cfg.Profiles) == 0 {
				return cmdutil.NewError(cmdutil.CodeAuthUnauthenticated, "no profiles configured; nothing to log out")
			}
			if opts.Name != "" {
				if err := cmdutil.ValidateProfileName(opts.Name); err != nil {
					return err
				}
			}
			if _, err := pickLogoutTargets(opts, cfg); err != nil {
				return err
			}
			if opts.DryRun {
				planArgs := map[string]any{"name": opts.Name}
				if opts.All {
					planArgs = map[string]any{"all": true}
				}
				if handled, err := cmdutil.HandleDryRun(c, true, cmdutil.DryRunPlan{
					Action: "auth.logout",
					Args:   planArgs,
				}); handled {
					return err
				}
			}
			return runLogout(opts, fopts, f)
		},
	}
	cmd.Flags().StringVar(&opts.Name, "name", "", "Profile to log out (defaults to the current profile)")
	cmd.Flags().BoolVar(&opts.All, "all", false, "Log out of every configured profile")
	cmdutil.AddFormatFlag(cmd, authLogoutFields...)
	cmdutil.AddDryRunFlag(cmd, &opts.DryRun)
	cmd.MarkFlagsMutuallyExclusive("name", "all")
	cmdutil.SetRisk(cmd, "auth.logout")
	cmdutil.SetAgentHelp(cmd, cmdutil.AgentHelp{
		UsedFor: "clear stored credentials for one profile (or all) and remove the profile from config",
		Examples: []string{
			"weknora auth logout",
			"weknora auth logout --name staging",
			"weknora auth logout --all",
		},
		Warnings: []string{
			"Requires explicit user approval (exit 10 / input.confirmation_required); never auto-add -y.",
			"auth logout clears local credentials for this profile; server-side session continues until expiry.",
		},
	})
	return cmd
}

func runLogout(opts *LogoutOptions, fopts *cmdutil.FormatOptions, f *cmdutil.Factory) error {
	cfg, err := f.Config()
	if err != nil {
		return err
	}
	if len(cfg.Profiles) == 0 {
		return cmdutil.NewError(cmdutil.CodeAuthUnauthenticated, "no profiles configured; nothing to log out")
	}
	// Reject shell-metacharacter names so opts.Name is safe to interpolate
	// into envelope.error.retry_command. Validation is name-only — the
	// profile may or may not exist in cfg.Profiles; the existence check
	// happens in pickLogoutTargets.
	if opts.Name != "" {
		if err := cmdutil.ValidateProfileName(opts.Name); err != nil {
			return err
		}
	}

	targets, err := pickLogoutTargets(opts, cfg)
	if err != nil {
		return err
	}

	// Destructive-write protocol: confirm before clearing credentials.
	scope := fmt.Sprintf("%d profile(s) [%s]", len(targets), strings.Join(targets, ", "))
	retryCmd := "weknora auth logout -y"
	if opts.All {
		retryCmd = "weknora auth logout --all -y"
	} else if opts.Name != "" {
		retryCmd = fmt.Sprintf("weknora auth logout --name %s -y", opts.Name)
	}
	if err := cmdutil.ConfirmDestructive(f.Prompter(), opts.Yes, fopts.WantsJSON(), "auth credentials", scope, "auth.logout", retryCmd); err != nil {
		return err
	}

	store, err := f.Secrets()
	if err != nil {
		return err
	}
	for _, name := range targets {
		clearProfileSecrets(store, cfg.Profiles[name], name)
		delete(cfg.Profiles, name)
	}
	// If we removed the active profile, pick a remaining one (deterministic by
	// map order would be flaky - leave CurrentProfile empty so the next
	// invocation surfaces a clear "no current profile" error rather than
	// silently switching).
	if _, stillExists := cfg.Profiles[cfg.CurrentProfile]; !stillExists {
		cfg.CurrentProfile = ""
	}
	if err := config.Save(cfg); err != nil {
		return cmdutil.Wrapf(cmdutil.CodeLocalFileIO, err, "save config")
	}

	if fopts.WantsJSON() {
		return fopts.Emit(iostreams.IO.Out, logoutResult{Removed: targets}, nil)
	}
	fmt.Fprintf(iostreams.IO.Out, "✓ Logged out of %d profile(s): %s\n", len(targets), strings.Join(targets, ", "))
	return nil
}

// pickLogoutTargets resolves the set of profiles to clear from flags + config.
func pickLogoutTargets(opts *LogoutOptions, cfg *config.Config) ([]string, error) {
	if opts.All {
		names := make([]string, 0, len(cfg.Profiles))
		for n := range cfg.Profiles {
			names = append(names, n)
		}
		return names, nil
	}
	name := opts.Name
	if name == "" {
		name = cfg.CurrentProfile
	}
	if name == "" {
		return nil, cmdutil.NewError(cmdutil.CodeInputMissingFlag,
			"no current profile set; pass --name <profile> or --all")
	}
	if _, ok := cfg.Profiles[name]; !ok {
		return nil, cmdutil.NewError(cmdutil.CodeLocalProfileNotFound,
			fmt.Sprintf("profile %q not found in config", name))
	}
	return []string{name}, nil
}

// clearProfileSecrets best-effort deletes every secret slot the profile
// references. Errors are swallowed because a missing secret is a no-op
// (tested in keyring_test.go) - we don't want a stale ref to block logout.
func clearProfileSecrets(store secrets.Store, c config.Profile, name string) {
	if c.TokenRef != "" {
		_ = store.Delete(name, "access")
	}
	if c.RefreshRef != "" {
		_ = store.Delete(name, "refresh")
	}
	if c.APIKeyRef != "" {
		_ = store.Delete(name, "api_key")
	}
}
