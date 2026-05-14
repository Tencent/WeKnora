package contextcmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/config"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
)

type AddOptions struct {
	Host string
	User string
}

// contextAddFields enumerates the fields surfaced for `--json` discovery on
// `context add`. The result describes the newly-registered context.
var contextAddFields = []string{
	"name", "host", "user", "current",
}

// addResult is the typed payload emitted under data on success.
type addResult struct {
	Name    string `json:"name"`
	Host    string `json:"host"`
	User    string `json:"user,omitempty"`
	Current bool   `json:"current"`
}

// NewCmdAdd builds `weknora context add`. Registers a *credentialless*
// connection target - host + optional user only. Credentials for the new
// context are attached separately with `weknora auth login --name <n>`,
// separating "where" the CLI talks to (the host) and "how" it authenticates
// (the credential). If you want one command for both, run
// `weknora auth login --name <n> --host <h>` instead.
func NewCmdAdd(f *cmdutil.Factory) *cobra.Command {
	opts := &AddOptions{}
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Register a new context (host without credentials)",
		Long: `Add a new context entry to config.yaml. Stores host (and optionally
user) but does NOT prompt for credentials. Use ` + "`weknora auth login --name <n>`" + ` to
attach credentials in a single step instead, or run ` + "`weknora auth login --name <n>`" + ` after
` + "`weknora context add`" + ` to fill them in.

The first context added is auto-selected as the current context. Subsequent
adds leave the current context untouched.`,
		Example: `  weknora context add staging --host https://staging.example.com
  weknora context add prod    --host https://prod.example.com --user alice@example.com`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			jopts, err := cmdutil.CheckJSONFlags(c)
			if err != nil {
				return err
			}
			return runAdd(opts, jopts, args[0])
		},
	}
	cmd.Flags().StringVar(&opts.Host, "host", "", "Server base URL, e.g. https://kb.example.com (required)")
	cmd.Flags().StringVar(&opts.User, "user", "", "Account email shown in 'context list' (optional, cosmetic only)")
	cmdutil.AddJSONFlags(cmd, contextAddFields)
	_ = cmd.MarkFlagRequired("host")
	return cmd
}

func runAdd(opts *AddOptions, jopts *cmdutil.JSONOptions, name string) error {
	if err := validateName(name); err != nil {
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
	if _, exists := cfg.Contexts[name]; exists {
		return &cmdutil.Error{
			Code:    cmdutil.CodeResourceAlreadyExists,
			Message: fmt.Sprintf("context %q already exists", name),
			Hint:    fmt.Sprintf("use a different name, or run `weknora context remove %s` first", name),
		}
	}
	if cfg.Contexts == nil {
		cfg.Contexts = map[string]config.Context{}
	}
	cfg.Contexts[name] = config.Context{Host: host, User: opts.User}
	wasFirst := cfg.CurrentContext == ""
	if wasFirst {
		cfg.CurrentContext = name
	}
	if err := config.Save(cfg); err != nil {
		return cmdutil.Wrapf(cmdutil.CodeLocalFileIO, err, "save config")
	}

	if jopts.Enabled() {
		return jopts.Emit(iostreams.IO.Out, addResult{Name: name, Host: host, User: opts.User, Current: wasFirst})
	}
	if wasFirst {
		fmt.Fprintf(iostreams.IO.Out, "✓ Added context %s (now current). Run `weknora auth login --name %s` to attach credentials.\n", name, name)
	} else {
		fmt.Fprintf(iostreams.IO.Out, "✓ Added context %s. Run `weknora auth login --name %s` to attach credentials.\n", name, name)
	}
	return nil
}

// validateName enforces the allowlist advertised in the --help hint: letters,
// digits, dash, underscore, dot. The `.` exception lets emails / DNS-like
// names through; the path-traversal `..` is structurally rejected by a
// separate guard because it would let a hand-edited config.yaml claim a
// context whose name walks out of the keyring namespace.
func validateName(name string) error {
	if name == "" {
		return &cmdutil.Error{
			Code:    cmdutil.CodeInputInvalidArgument,
			Message: "context name must not be empty",
		}
	}
	if name == "." || name == ".." || strings.Contains(name, "/") || strings.Contains(name, "\\") {
		return &cmdutil.Error{
			Code:    cmdutil.CodeInputInvalidArgument,
			Message: fmt.Sprintf("context name %q is reserved or path-like", name),
			Hint:    "use letters, digits, dashes, underscores, or dots",
		}
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-' || r == '_' || r == '.':
			continue
		default:
			return &cmdutil.Error{
				Code:    cmdutil.CodeInputInvalidArgument,
				Message: fmt.Sprintf("context name %q contains invalid character %q", name, r),
				Hint:    "use letters, digits, dashes, underscores, or dots",
			}
		}
	}
	return nil
}

