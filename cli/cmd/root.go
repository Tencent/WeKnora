// Package cmd holds the cobra command tree. main.go calls Execute().
package cmd

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	agentcmd "github.com/Tencent/WeKnora/cli/cmd/agent"
	apicmd "github.com/Tencent/WeKnora/cli/cmd/api"
	"github.com/Tencent/WeKnora/cli/cmd/auth"
	chatcmd "github.com/Tencent/WeKnora/cli/cmd/chat"
	chunkcmd "github.com/Tencent/WeKnora/cli/cmd/chunk"
	"github.com/Tencent/WeKnora/cli/cmd/doc"
	"github.com/Tencent/WeKnora/cli/cmd/doctor"
	"github.com/Tencent/WeKnora/cli/cmd/kb"
	linkcmd "github.com/Tencent/WeKnora/cli/cmd/link"
	mcpcmd "github.com/Tencent/WeKnora/cli/cmd/mcp"
	profilecmd "github.com/Tencent/WeKnora/cli/cmd/profile"
	"github.com/Tencent/WeKnora/cli/cmd/search"
	sessioncmd "github.com/Tencent/WeKnora/cli/cmd/session"
	"github.com/Tencent/WeKnora/cli/internal/build"
	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
)

// resolveFormatEarly scans raw argv for --format before cobra's command
// dispatch. This ensures globalFormatMode is set before any cobra-side
// validator fires (unknown flag, arg count, etc.), so PrintError routes
// those errors through the JSON envelope when --format json is in effect.
//
// Call order: resolveFormatEarly → cobra Execute → PersistentPreRunE (which
// re-runs CheckFormatFlag and calls SetFormatMode again with the same value).
func resolveFormatEarly(args []string) {
	var mode string
	for i, a := range args {
		if a == "--format" && i+1 < len(args) {
			mode = strings.ToLower(args[i+1])
			break
		}
		if strings.HasPrefix(a, "--format=") {
			mode = strings.ToLower(strings.TrimPrefix(a, "--format="))
			break
		}
	}
	if mode == "" {
		if v := os.Getenv("WEKNORA_FORMAT"); v != "" {
			mode = strings.ToLower(v)
		}
	}
	switch mode {
	case "json", "ndjson", "text":
		cmdutil.SetFormatMode(mode)
	case "":
		// nothing to set; leave globalFormatMode at its zero value
	default:
		// Invalid format value: promote to json so the subsequent
		// rejection error (from CheckFormatFlag) still emits as an envelope
		// rather than prose. The real validation error will still fire.
		cmdutil.SetFormatMode("json")
	}
}

// Execute is the entry point invoked by main(). Returns the process exit code.
// The passed context is wired to OS signals (SIGINT / SIGTERM) by main so
// commands that respect cmd.Context() can run their cancellation cleanup.
func Execute(ctx context.Context) int {
	// Resolve --format early so cobra-side errors (unknown flag, arg-count
	// violations) still route through PrintError's JSON envelope path when
	// --format json is in effect. PersistentPreRunE will call SetFormatMode
	// again after full flag parse - idempotent when the value matches.
	resolveFormatEarly(os.Args[1:])
	root := NewRootCmd(cmdutil.New())
	if err := root.ExecuteContext(ctx); err != nil {
		// Errors go to stderr. Stdout stays
		// empty (or holds partial success the command produced) so
		// downstream `--format json | jq` pipelines never filter error shapes
		// out of the success stream. The typed exit code (3/4/5/6/7/10)
		// carries the error class.
		mapped := MapCobraError(err)
		cmdutil.PrintError(iostreams.IO.Err, mapped)
		return cmdutil.ExitCode(mapped)
	}
	return 0
}

// MapCobraError tags the textually-emitted cobra errors as cmdutil.FlagError
// so they exit 2 like other user invocation mistakes. SetFlagErrorFunc handles
// flag parse errors at parse time; this catches positional/Args validation
// errors and unknown subcommands that propagate as plain errors.
//
// Pinned to cobra v1.10 message formats (cobra/args.go: ExactArgs / NoArgs;
// cobra/command.go: required-flag / unknown-command). TestMapCobraError_PinnedPrefixes
// guards against a silent break on cobra bumps.
//
// Exported so the acceptance/contract test helper can reuse the mapping
// when replicating Execute()'s stderr error-path in-process.
func MapCobraError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	for _, prefix := range cobraFlagErrorPrefixes {
		if strings.HasPrefix(msg, prefix) {
			return cmdutil.NewFlagError(err)
		}
	}
	return err
}

// cobraFlagErrorPrefixes lists the text prefixes cobra uses for invocation
// problems we want to surface as exit 2. Pinned per cobra v1.10.
var cobraFlagErrorPrefixes = []string{
	"unknown command ",
	"required flag(s)",
	"accepts ",          // ExactArgs / RangeArgs / etc. - `accepts N arg(s), received M`
	"requires at least", // MinimumNArgs
	"requires at most",  // MaximumNArgs
	"unknown flag",
	"invalid argument \"", // pflag type-coercion: `invalid argument "foo" for "--flag" flag`
}

// NewRootCmd builds the cobra tree. Splitting it from Execute() lets tests
// drive the tree directly with their own factory. Exported so the
// acceptance/contract suite can construct the tree in-process.
func NewRootCmd(f *cmdutil.Factory) *cobra.Command {
	v, commit, date := build.Info()
	cmd := &cobra.Command{
		Use:   "weknora",
		Short: "WeKnora CLI",
		Long: `Command-line client for the WeKnora RAG server. Manage knowledge bases
and documents, run hybrid search, chat with grounded answers, or expose
a curated read-only MCP tool surface for AI agents.`,
		Example: `  weknora auth login --host=https://kb.example.com
  weknora kb list
  weknora chat "summarise the design doc"
  weknora doctor --format json`,
		SilenceUsage:  true,
		SilenceErrors: true,
		// Version makes cobra auto-register a `--version` global flag that
		// prints this string. We accept both `--version` and a `version`
		// subcommand; the subcommand still owns the richer `--format json` output
		// (build commit + date).
		Version: fmt.Sprintf("%s (commit %s, built %s)", v, commit, date),
		PersistentPreRunE: func(c *cobra.Command, args []string) error {
			// Propagate the global --profile flag (or WEKNORA_PROFILE env) into
			// the Factory for this invocation only - single-shot override, no disk write.
			// Flag takes precedence over env; env takes precedence over config file.
			if v, _ := c.Flags().GetString("profile"); v != "" {
				f.ProfileOverride = v
			} else if v := os.Getenv("WEKNORA_PROFILE"); v != "" {
				f.ProfileOverride = v
			}
			// Pin --format mode for cmdutil.PrintError envelope vs prose decision.
			// Safe on commands that don't register --format: CheckFormatFlag returns
			// {Mode:""}, ResolveDefault falls back to TTY detection.
			if fopts, err := cmdutil.CheckFormatFlag(c); err == nil && fopts != nil {
				fopts.FromEnv()
				fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())
				cmdutil.SetFormatMode(string(fopts.Mode))
			}
			// Record the resolved profile for envelope.profile and NDJSON init.profile.
			cmdutil.SetProfile(f.ActiveProfile())
			// Resolve --log-level / WEKNORA_LOG_LEVEL and apply to the SDK
			// debug logger before any SDK call is made. Returns a typed error
			// when --log-level was passed explicitly with an invalid value
			// (matches --format validation strictness).
			return f.ApplyLogLevel(c, iostreams.IO.Err)
		},
	}
	// Match `weknora version` line format so both forms output the same.
	cmd.SetVersionTemplate("weknora {{.Version}}\n")
	addGlobalFlags(cmd)
	// Wrap cobra's flag-parsing errors as FlagError so cmdutil.ExitCode maps
	// them to exit 2. "unknown command" errors are detected by message prefix
	// in Execute() since cobra emits them as plain errors.
	cmd.SetFlagErrorFunc(func(c *cobra.Command, err error) error {
		return cmdutil.NewFlagError(err)
	})

	cmd.AddCommand(newVersionCmd(f))
	cmd.AddCommand(auth.NewCmdAuth(f))
	cmd.AddCommand(search.NewCmdSearch(f))
	cmd.AddCommand(doctor.NewCmd(f))
	cmd.AddCommand(kb.NewCmd(f))
	cmd.AddCommand(profilecmd.NewCmd(f))
	cmd.AddCommand(linkcmd.NewCmd(f))
	cmd.AddCommand(linkcmd.NewCmdUnlink())
	cmd.AddCommand(doc.NewCmd(f))
	cmd.AddCommand(apicmd.NewCmd(f))
	cmd.AddCommand(chatcmd.NewCmd(f))
	cmd.AddCommand(sessioncmd.NewCmd(f))
	cmd.AddCommand(agentcmd.NewCmd(f))
	cmd.AddCommand(chunkcmd.NewCmdChunk(f))
	cmd.AddCommand(mcpcmd.NewCmd(f))
	installUnknownSubcommandGuard(cmd)
	return cmd
}

// addGlobalFlags registers persistent flags available on every subcommand.
// Only flags whose behavior is actually wired are listed - a flag that
// accepts values but does nothing is a worse contract than no flag.
func addGlobalFlags(cmd *cobra.Command) {
	pf := cmd.PersistentFlags()
	pf.BoolP("yes", "y", false, "Skip confirmation prompts on destructive operations")
	pf.String("profile", "", "Override the active profile for this invocation (no disk write)")
	// --log-level is registered as a persistent (global) flag because the SDK
	// debug logger is initialised once at factory time before any command runs,
	// so the flag must be visible on all subcommands. Unlike --format (which
	// only some commands honour and is registered per-command, Method D),
	// --log-level applies uniformly to all SDK calls.
	cmdutil.AddLogLevelFlag(cmd)
	// --format and --jq are persistent globals so unknown-subcommand paths
	// (e.g. `weknora fooo --format json`) reach the typed-envelope guard
	// instead of being rejected as "unknown flag" exit 2 by cobra. Commands
	// that don't produce JSON output (e.g. `completion bash`) ignore the flag
	// rather than error — the unified agent contract is worth the trade.
	pf.String("format", "", "Output format: text | json | ndjson (default: json)")
	pf.StringP("jq", "q", "", "Filter JSON output using a jq `expression` (requires --format json|ndjson)")
}

// versionFields enumerates the fields surfaced for `--format json` discovery on
// `version`. Mirrors the version object payload.
var versionFields = []string{"version", "commit", "date"}

// newVersionCmd is the only leaf command shipped in the foundation PR. It
// doubles as the smoke test that proves Factory + iostreams + cobra wiring works.
func newVersionCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Show CLI build metadata",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			fopts, err := cmdutil.CheckFormatFlag(c)
			if err != nil {
				return err
			}
			fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())
			v, commit, date := build.Info()
			if fopts.WantsJSON() {
				return fopts.Emit(c.OutOrStdout(), map[string]string{
					"version": v,
					"commit":  commit,
					"date":    date,
				}, nil)
			}
			fmt.Fprintf(c.OutOrStdout(), "weknora %s (commit %s, built %s)\n", v, commit, date)
			return nil
		},
	}
	cmdutil.AddFormatFlag(cmd, versionFields...)
	return cmd
}

// installUnknownSubcommandGuard recursively attaches a RunE that emits a typed
// envelope error when a parent command is invoked with no matching subcommand
// (e.g. `weknora kb bogus`). Without this, cobra falls back to a free-form
// "unknown command" string error via legacyArgs validation.
//
// cobra's legacyArgs (args.go) fires at Find() time when Args == nil:
// for root commands it rejects any unrecognised positional before RunE runs.
// Setting cobra.ArbitraryArgs bypasses that check so our RunE receives the
// unknown arg and can emit the typed envelope instead.
func installUnknownSubcommandGuard(cmd *cobra.Command) {
	if cmd.HasSubCommands() && cmd.Run == nil && cmd.RunE == nil {
		cmd.RunE = unknownSubcommandRunE
		cmd.Args = cobra.ArbitraryArgs
	}
	for _, c := range cmd.Commands() {
		installUnknownSubcommandGuard(c)
	}
}

func unknownSubcommandRunE(cmd *cobra.Command, args []string) error {
	// Group command invoked with no subcommand (e.g. `weknora kb`):
	// show help rather than emit a confusing `unknown ""` error.
	if len(args) == 0 {
		return cmd.Help()
	}
	unknown := args[0]
	available := availableSubcommandNames(cmd)
	return cmdutil.NewError(
		cmdutil.CodeInputUnknownSubcommand,
		fmt.Sprintf("unknown subcommand %q for %q", unknown, cmd.CommandPath()),
	).
		WithHint(fmt.Sprintf("available subcommands: %s", strings.Join(available, ", "))).
		WithRetryCommand(cmd.CommandPath() + " --help").
		WithDetail(map[string]any{
			"unknown":      unknown,
			"command_path": cmd.CommandPath(),
			"available":    available,
		})
}

func availableSubcommandNames(cmd *cobra.Command) []string {
	var names []string
	for _, c := range cmd.Commands() {
		if c.Hidden || c.Name() == "help" || c.Name() == "completion" {
			continue
		}
		names = append(names, c.Name())
	}
	sort.Strings(names)
	return names
}
