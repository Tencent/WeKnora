package contextcmd

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

// contextRemoveFields enumerates the fields surfaced for `--json` discovery on
// `context remove`. The result reports the disposition of the removed entry.
var contextRemoveFields = []string{
	"name", "removed", "was_current",
}

// removeResult is the typed payload emitted under data on success.
type removeResult struct {
	Name       string `json:"name"`
	Removed    bool   `json:"removed"`
	WasCurrent bool   `json:"was_current"`
}

// NewCmdRemove builds `weknora context remove`. Drops the entry from
// config.yaml and best-effort clears keyring references. Removing a
// non-current context is low-friction (no prompt). Removing the *current*
// context triggers the destructive-write confirmation protocol (exit 10),
// because subsequent commands will have no default connection target.
func NewCmdRemove(f *cmdutil.Factory) *cobra.Command {
	opts := &RemoveOptions{}
	cmd := &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a context (drops entry, clears keyring refs)",
		Long: `Deletes the named context from config.yaml and best-effort clears any
keyring references it owned (matches ` + "`weknora auth logout`" + `).

Removing the current context also clears CurrentContext - subsequent commands
will error until you select another with ` + "`weknora context use <name>`" + ` or pick
one up via the global ` + "`--context`" + ` flag. Because that change is observable in
every later command, removing the current context requires explicit -y/--yes
in scripted / --json invocations (exit code 10; see cli/README.md).`,
		Example: `  weknora context remove staging              # remove non-current → no prompt
  weknora context remove production -y        # remove current → confirm`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			jopts, err := cmdutil.CheckJSONFlags(c)
			if err != nil {
				return err
			}
			opts.Yes, _ = c.Flags().GetBool("yes")
			store, err := f.Secrets()
			if err != nil {
				return err
			}
			return runRemove(opts, jopts, args[0], store, f.Prompter())
		},
	}
	cmdutil.AddJSONFlags(cmd, contextRemoveFields)
	return cmd
}

func runRemove(opts *RemoveOptions, jopts *cmdutil.JSONOptions, name string, store secrets.Store, p prompt.Prompter) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, exists := cfg.Contexts[name]
	if !exists {
		return notFoundError(name, cfg)
	}
	wasCurrent := name == cfg.CurrentContext

	jsonOut := jopts.Enabled()
	// Confirmation only fires for removing the current context - non-current
	// remove uses the same low-friction policy as `auth logout`.
	if wasCurrent {
		if err := cmdutil.ConfirmDestructive(p, opts.Yes, jsonOut, "current context", name); err != nil {
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
	clearContextSecrets(store, ctx, name)

	result := removeResult{Name: name, Removed: true, WasCurrent: wasCurrent}
	if jsonOut {
		return jopts.Emit(iostreams.IO.Out, result)
	}
	if wasCurrent {
		fmt.Fprintf(iostreams.IO.Out, "✓ Removed context %s (current context cleared - run `weknora context use <name>` to pick another)\n", name)
	} else {
		fmt.Fprintf(iostreams.IO.Out, "✓ Removed context %s\n", name)
	}
	return nil
}

// clearContextSecrets mirrors auth/logout.go: best-effort delete every secret
// slot the context references. Errors are swallowed so a missing keyring
// entry doesn't block remove (same policy as `auth logout`).
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
