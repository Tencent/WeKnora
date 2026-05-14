package auth

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/aiclient"
	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/config"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/secrets"
)

type LogoutOptions struct {
	Name string // --name: target a specific context (default: current)
	All  bool   // --all: clear every context
}

// authLogoutFields enumerates the fields surfaced for `--json` discovery
// on `auth logout`. The result is the list of context names that were
// logged out.
var authLogoutFields = []string{"removed"}

// logoutResult is the typed payload emitted under data.
type logoutResult struct {
	Removed []string `json:"removed"`
}

// NewCmdLogout builds `weknora auth logout`. Clears stored credentials
// (keyring + file fallback) and removes the context entry from config.yaml.
// No server-side revocation — local-only credential clear.
func NewCmdLogout(f *cmdutil.Factory) *cobra.Command {
	opts := &LogoutOptions{}
	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Remove stored credentials for a context",
		Long: `Clear keyring + file-fallback secrets for one context (or all of
them with --all) and drop the context entry from ~/.config/weknora/config.yaml.

Note: this does NOT revoke the credential server-side — for API keys, you
must rotate them in the server UI; for JWT, the token will continue to be
accepted until it expires.`,
		Example: `  weknora auth logout                       # current context
  weknora auth logout --name staging        # specific context
  weknora auth logout --all`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			jopts, err := cmdutil.CheckJSONFlags(c)
			if err != nil {
				return err
			}
			return runLogout(opts, jopts, f)
		},
	}
	cmd.Flags().StringVar(&opts.Name, "name", "", "Context to log out (defaults to the current context)")
	cmd.Flags().BoolVar(&opts.All, "all", false, "Log out of every configured context")
	cmdutil.AddJSONFlags(cmd, authLogoutFields)
	cmd.MarkFlagsMutuallyExclusive("name", "all")
	aiclient.SetAgentHelp(cmd, "Clears local credentials only; the server-side token / api-key continues to be valid until expired or rotated. Returns data.removed: [...names]. Errors: auth.unauthenticated when no contexts configured.")
	return cmd
}

func runLogout(opts *LogoutOptions, jopts *cmdutil.JSONOptions, f *cmdutil.Factory) error {
	cfg, err := f.Config()
	if err != nil {
		return err
	}
	if len(cfg.Contexts) == 0 {
		return cmdutil.NewError(cmdutil.CodeAuthUnauthenticated, "no contexts configured; nothing to log out")
	}

	targets, err := pickLogoutTargets(opts, cfg)
	if err != nil {
		return err
	}

	store, err := f.Secrets()
	if err != nil {
		return err
	}
	for _, name := range targets {
		clearContextSecrets(store, cfg.Contexts[name], name)
		delete(cfg.Contexts, name)
	}
	// If we removed the active context, pick a remaining one (deterministic by
	// map order would be flaky — leave CurrentContext empty so the next
	// invocation surfaces a clear "no current context" error rather than
	// silently switching).
	if _, stillExists := cfg.Contexts[cfg.CurrentContext]; !stillExists {
		cfg.CurrentContext = ""
	}
	if err := config.Save(cfg); err != nil {
		return cmdutil.Wrapf(cmdutil.CodeLocalFileIO, err, "save config")
	}

	if jopts.Enabled() {
		return jopts.Emit(iostreams.IO.Out, logoutResult{Removed: targets})
	}
	fmt.Fprintf(iostreams.IO.Out, "✓ Logged out of %d context(s): %s\n", len(targets), strings.Join(targets, ", "))
	return nil
}

// pickLogoutTargets resolves the set of contexts to clear from flags + config.
func pickLogoutTargets(opts *LogoutOptions, cfg *config.Config) ([]string, error) {
	if opts.All {
		names := make([]string, 0, len(cfg.Contexts))
		for n := range cfg.Contexts {
			names = append(names, n)
		}
		return names, nil
	}
	name := opts.Name
	if name == "" {
		name = cfg.CurrentContext
	}
	if name == "" {
		return nil, cmdutil.NewError(cmdutil.CodeInputMissingFlag,
			"no current context set; pass --name <ctx> or --all")
	}
	if _, ok := cfg.Contexts[name]; !ok {
		return nil, cmdutil.NewError(cmdutil.CodeLocalContextNotFound,
			fmt.Sprintf("context %q not found in config", name))
	}
	return []string{name}, nil
}

// clearContextSecrets best-effort deletes every secret slot the context
// references. Errors are swallowed because a missing secret is a no-op
// (tested in keyring_test.go) — we don't want a stale ref to block logout.
func clearContextSecrets(store secrets.Store, c config.Context, name string) {
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
