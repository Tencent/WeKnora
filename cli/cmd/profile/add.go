package profilecmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/config"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
)

type AddOptions struct {
	Host   string
	User   string
	DryRun bool
}

// profileAddFields enumerates the fields surfaced for `--format json` discovery on
// `profile add`. The result describes the newly-registered profile.
var profileAddFields = []string{
	"name", "host", "user", "current",
}

// addResult is the typed payload emitted under data on success.
type addResult struct {
	Name    string `json:"name"`
	Host    string `json:"host"`
	User    string `json:"user,omitempty"`
	Current bool   `json:"current"`
}

// NewCmdAdd builds `weknora profile add`. Registers a *credentialless*
// connection target - host + optional user only. Credentials for the new
// profile are attached separately with `weknora auth login --name <n>`,
// separating "where" the CLI talks to (the host) and "how" it authenticates
// (the credential). If you want one command for both, run
// `weknora auth login --name <n> --host <h>` instead.
func NewCmdAdd(f *cmdutil.Factory) *cobra.Command {
	opts := &AddOptions{}
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Register a new profile (host without credentials)",
		Long: `Add a new profile entry to config.yaml. Stores host (and optionally
user) but does NOT prompt for credentials. Use ` + "`weknora auth login --name <n>`" + ` to
attach credentials in a single step instead, or run ` + "`weknora auth login --name <n>`" + ` after
` + "`weknora profile add`" + ` to fill them in.

The first profile added is auto-selected as the current profile. Subsequent
adds leave the current profile untouched.`,
		Example: `  weknora profile add staging --host https://staging.example.com
  weknora profile add prod    --host https://prod.example.com --user alice@example.com`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			fopts, err := cmdutil.CheckFormatFlag(c)
			if err != nil {
				return err
			}
			fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())
			// Pure-local validation runs before the dry-run gate so --dry-run
			// rejects identically to the live path. Same typed errors as runAdd
			// (kept there for direct-call callers).
			name := args[0]
			if err := cmdutil.ValidateProfileName(name); err != nil {
				return err
			}
			host, err := cmdutil.NormalizeHost(opts.Host)
			if err != nil {
				return err
			}
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if _, exists := cfg.Profiles[name]; exists {
				return &cmdutil.Error{
					Code:    cmdutil.CodeResourceAlreadyExists,
					Message: fmt.Sprintf("profile %q already exists", name),
					Hint:    fmt.Sprintf("use a different name, or run `weknora profile remove %s` first", name),
				}
			}
			if handled, err := cmdutil.HandleDryRun(c, opts.DryRun, cmdutil.DryRunPlan{
				Action: "profile.add",
				Args: map[string]any{
					"name": name,
					"host": host,
				},
			}); handled {
				return err
			}
			return runAddWithConfig(opts, fopts, name, host, cfg)
		},
	}
	cmd.Flags().StringVar(&opts.Host, "host", "", "Server base URL, e.g. https://kb.example.com (required)")
	cmd.Flags().StringVar(&opts.User, "user", "", "Account email shown in 'profile list' (optional, cosmetic only)")
	cmdutil.AddFormatFlag(cmd, profileAddFields...)
	cmdutil.AddDryRunFlag(cmd, &opts.DryRun)
	_ = cmd.MarkFlagRequired("host")
	return cmd
}

// runAdd is the legacy direct-call entrypoint: revalidates inputs and loads
// config. Preserved for tests / external callers that bypass the cobra layer.
// The cobra RunE path validates earlier and delegates to runAddWithConfig.
func runAdd(opts *AddOptions, fopts *cmdutil.FormatOptions, name string) error {
	if err := cmdutil.ValidateProfileName(name); err != nil {
		return err
	}
	host, err := cmdutil.NormalizeHost(opts.Host)
	if err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if _, exists := cfg.Profiles[name]; exists {
		return &cmdutil.Error{
			Code:    cmdutil.CodeResourceAlreadyExists,
			Message: fmt.Sprintf("profile %q already exists", name),
			Hint:    fmt.Sprintf("use a different name, or run `weknora profile remove %s` first", name),
		}
	}
	return runAddWithConfig(opts, fopts, name, host, cfg)
}

// runAddWithConfig performs the side-effectful write. Inputs are assumed to be
// pre-validated (ValidateProfileName, NormalizeHost, dup-check) by the caller.
func runAddWithConfig(opts *AddOptions, fopts *cmdutil.FormatOptions, name, host string, cfg *config.Config) error {
	if cfg.Profiles == nil {
		cfg.Profiles = map[string]config.Profile{}
	}
	cfg.Profiles[name] = config.Profile{Host: host, User: opts.User}
	wasFirst := cfg.CurrentProfile == ""
	if wasFirst {
		cfg.CurrentProfile = name
	}
	if err := config.Save(cfg); err != nil {
		return cmdutil.Wrapf(cmdutil.CodeLocalFileIO, err, "save config")
	}

	if fopts.WantsJSON() {
		return fopts.Emit(iostreams.IO.Out, addResult{Name: name, Host: host, User: opts.User, Current: wasFirst}, nil)
	}
	if wasFirst {
		fmt.Fprintf(iostreams.IO.Out, "✓ Added profile %s (now current). Run `weknora auth login --name %s` to attach credentials.\n", name, name)
	} else {
		fmt.Fprintf(iostreams.IO.Out, "✓ Added profile %s. Run `weknora auth login --name %s` to attach credentials.\n", name, name)
	}
	return nil
}
