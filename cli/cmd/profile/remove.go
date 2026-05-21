package profilecmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/config"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/prompt"
	"github.com/Tencent/WeKnora/cli/internal/secrets"
)

type RemoveOptions struct {
	Yes bool // sourced from the global -y/--yes persistent flag (matches `kb delete`)
}

// profileRemoveFields enumerates the fields surfaced for `--format json` discovery on
// `profile remove`. The result reports the disposition of the removed entry.
var profileRemoveFields = []string{
	"name", "removed", "was_current",
}

// removeResult is the typed payload emitted under data on success.
type removeResult struct {
	Name       string `json:"name"`
	Removed    bool   `json:"removed"`
	WasCurrent bool   `json:"was_current"`
}

// NewCmdRemove builds `weknora profile remove`. Drops the entry from
// config.yaml and best-effort clears keyring references. Removing a
// non-current profile is low-friction (no prompt). Removing the *current*
// profile triggers the destructive-write confirmation protocol (exit 10),
// because subsequent commands will have no default connection target.
func NewCmdRemove(f *cmdutil.Factory) *cobra.Command {
	opts := &RemoveOptions{}
	cmd := &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a profile (drops entry, clears keyring refs)",
		Long: `Deletes the named profile from config.yaml and best-effort clears any
keyring references it owned (matches ` + "`weknora auth logout`" + `).

Removing the current profile also clears CurrentContext - subsequent commands
will error until you select another with ` + "`weknora profile use <name>`" + ` or pick
one up via the global ` + "`--profile`" + ` flag. Because that change is observable in
every later command, removing the current profile requires explicit -y/--yes
in scripted / --format json invocations (exit code 10; see cli/README.md).`,
		Example: `  weknora profile remove staging              # remove non-current → no prompt
  weknora profile remove production -y        # remove current → confirm`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			fopts, err := cmdutil.CheckFormatFlag(c)
			if err != nil {
				return err
			}
			fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())
			opts.Yes, _ = c.Flags().GetBool("yes")
			store, err := f.Secrets()
			if err != nil {
				return err
			}
			return runRemove(opts, fopts, args[0], store, f.Prompter())
		},
	}
	cmdutil.AddFormatFlag(cmd, profileRemoveFields...)
	cmdutil.SetAgentHelp(cmd, cmdutil.AgentHelp{
		UsedFor:       "remove a named profile and its stored credentials",
		RequiredFlags: []string{"<name> (positional)"},
		Examples: []string{
			"weknora profile remove staging",
			"weknora profile remove production -y",
		},
		Warnings: []string{
			"profile remove drops the named profile and its stored credentials. Never auto-add -y.",
		},
	})
	return cmd
}

func runRemove(opts *RemoveOptions, fopts *cmdutil.FormatOptions, name string, store secrets.Store, p prompt.Prompter) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, exists := cfg.Contexts[name]
	if !exists {
		return notFoundError(name, cfg)
	}
	wasCurrent := name == cfg.CurrentContext

	jsonOut := fopts.WantsJSON()
	// Confirmation only fires for removing the current profile - non-current
	// remove uses the same low-friction policy as `auth logout`.
	if wasCurrent {
		if err := cmdutil.ConfirmDestructive(p, opts.Yes, jsonOut, "current profile", name, "profile.remove", fmt.Sprintf("weknora profile remove %s -y", name)); err != nil {
			return err
		}
	}

	// Config first, secrets after: a crash in between leaves an orphan
	// keyring entry but no dangling config ref (same ordering as auth logout).
	delete(cfg.Contexts, name)
	if wasCurrent {
		cfg.CurrentContext = ""
	}
	if err := config.Save(cfg); err != nil {
		return cmdutil.Wrapf(cmdutil.CodeLocalFileIO, err, "save config")
	}
	clearProfileSecrets(store, ctx, name)

	result := removeResult{Name: name, Removed: true, WasCurrent: wasCurrent}
	if jsonOut {
		return fopts.Emit(iostreams.IO.Out, result, nil)
	}
	if wasCurrent {
		fmt.Fprintf(iostreams.IO.Out, "✓ Removed profile %s (current profile cleared - run `weknora profile use <name>` to pick another)\n", name)
	} else {
		fmt.Fprintf(iostreams.IO.Out, "✓ Removed profile %s\n", name)
	}
	return nil
}

// clearProfileSecrets mirrors auth/logout.go: best-effort delete every secret
// slot the profile references. Errors are swallowed so a missing keyring
// entry doesn't block remove (same policy as `auth logout`).
func clearProfileSecrets(store secrets.Store, c config.Context, name string) {
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
