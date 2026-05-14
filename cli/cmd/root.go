// Package cmd holds the cobra command tree. main.go calls Execute().
package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	agentcmd "github.com/Tencent/WeKnora/cli/cmd/agent"
	apicmd "github.com/Tencent/WeKnora/cli/cmd/api"
	"github.com/Tencent/WeKnora/cli/cmd/auth"
	chatcmd "github.com/Tencent/WeKnora/cli/cmd/chat"
	contextcmd "github.com/Tencent/WeKnora/cli/cmd/context"
	"github.com/Tencent/WeKnora/cli/cmd/doc"
	"github.com/Tencent/WeKnora/cli/cmd/doctor"
	"github.com/Tencent/WeKnora/cli/cmd/kb"
	linkcmd "github.com/Tencent/WeKnora/cli/cmd/link"
	mcpcmd "github.com/Tencent/WeKnora/cli/cmd/mcp"
	"github.com/Tencent/WeKnora/cli/cmd/search"
	sessioncmd "github.com/Tencent/WeKnora/cli/cmd/session"
	"github.com/Tencent/WeKnora/cli/internal/build"
	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/format"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
)

// Execute is the entry point invoked by main(). Returns the process exit code.
func Execute() int {
	root := NewRootCmd(cmdutil.New())
	if err := root.Execute(); err != nil {
		// Errors go to stderr (matches gh/aws/stripe). Stdout stays
		// empty (or holds partial success the command produced) so
		// downstream `--json | jq` pipelines never filter error shapes
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
	"invalid argument", // pflag type-coercion failure (e.g. --limit=foo)
}

// NewRootCmd builds the cobra tree. Splitting it from Execute() lets tests
// drive the tree directly with their own factory. Exported so the
// acceptance/contract suite can construct the tree in-process.
func NewRootCmd(f *cmdutil.Factory) *cobra.Command {
	v, commit, date := build.Info()
	cmd := &cobra.Command{
		Use:   "weknora",
		Short: "WeKnora CLI - RAG knowledge base from your terminal",
		Long: `WeKnora CLI lets you authenticate, browse knowledge bases, and run
hybrid searches against a WeKnora server from your shell or an AI agent.`,
		Example: `  weknora auth login --host=https://kb.example.com   # one-time setup
  weknora kb list                                    # list knowledge bases
  weknora kb view <id>                               # show one
  weknora search chunks "your question" --kb=<id>    # hybrid retrieval
  weknora doctor --json                              # health check (agent-readable)`,
		SilenceUsage:  true,
		SilenceErrors: true,
		// Version makes cobra auto-register a `--version` global flag that
		// prints this string. We accept both `--version` and a `version`
		// subcommand; the subcommand still owns the richer `--json` output
		// (build commit + date).
		Version: fmt.Sprintf("%s (commit %s, built %s)", v, commit, date),
		PersistentPreRun: func(c *cobra.Command, args []string) {
			// Propagate the global --context flag into the Factory for this
			// invocation only - single-shot override, no disk write.
			if v, _ := c.Flags().GetString("context"); v != "" {
				f.ContextOverride = v
			}
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
	cmd.AddCommand(contextcmd.NewCmd(f))
	cmd.AddCommand(linkcmd.NewCmd(f))
	cmd.AddCommand(linkcmd.NewCmdUnlink())
	cmd.AddCommand(doc.NewCmd(f))
	cmd.AddCommand(apicmd.NewCmd(f))
	cmd.AddCommand(chatcmd.NewCmd(f))
	cmd.AddCommand(sessioncmd.NewCmd(f))
	cmd.AddCommand(agentcmd.NewCmd(f))
	cmd.AddCommand(mcpcmd.NewCmd(f))
	return cmd
}

// addGlobalFlags registers persistent flags available on every subcommand.
// Only flags whose behavior is actually wired are listed - a flag that
// accepts values but does nothing is a worse contract than no flag.
func addGlobalFlags(cmd *cobra.Command) {
	pf := cmd.PersistentFlags()
	pf.BoolP("yes", "y", false, "Skip confirmation prompts on destructive operations")
	pf.String("context", "", "Override the active context for this invocation (no disk write)")
}

// versionFields enumerates the fields surfaced for `--json` discovery on
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
			jopts, err := cmdutil.CheckJSONFlags(c)
			if err != nil {
				return err
			}
			v, commit, date := build.Info()
			if jopts.Enabled() {
				return format.WriteJSONFiltered(
					c.OutOrStdout(),
					map[string]string{
						"version": v,
						"commit":  commit,
						"date":    date,
					},
					jopts.Fields, jopts.JQ,
				)
			}
			fmt.Fprintf(c.OutOrStdout(), "weknora %s (commit %s, built %s)\n", v, commit, date)
			return nil
		},
	}
	cmdutil.AddJSONFlags(cmd, versionFields)
	return cmd
}
