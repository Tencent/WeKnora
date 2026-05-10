// Package cmd holds the cobra command tree. main.go calls Execute().
package cmd

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	apicmd "github.com/Tencent/WeKnora/cli/cmd/api"
	"github.com/Tencent/WeKnora/cli/cmd/auth"
	chatcmd "github.com/Tencent/WeKnora/cli/cmd/chat"
	contextcmd "github.com/Tencent/WeKnora/cli/cmd/context"
	"github.com/Tencent/WeKnora/cli/cmd/doc"
	"github.com/Tencent/WeKnora/cli/cmd/doctor"
	"github.com/Tencent/WeKnora/cli/cmd/kb"
	linkcmd "github.com/Tencent/WeKnora/cli/cmd/link"
	"github.com/Tencent/WeKnora/cli/cmd/search"
	"github.com/Tencent/WeKnora/cli/internal/agent"
	"github.com/Tencent/WeKnora/cli/internal/build"
	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/format"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
)

// Execute is the entry point invoked by main(). Returns the process exit code.
func Execute() int {
	root := NewRootCmd(cmdutil.New())
	// ExecuteC returns the actually-invoked leaf (or root when invocation
	// failed before dispatch); we use it to honor the leaf's --json and
	// inherited --format without walking the tree ourselves.
	cmd, err := root.ExecuteC()
	if err == nil {
		return 0
	}
	err = MapCobraError(err)
	if WantsJSONOutput(cmd) {
		cmdutil.PrintErrorEnvelope(iostreams.IO.Out, err)
	} else {
		cmdutil.PrintError(iostreams.IO.Err, err)
	}
	return cmdutil.ExitCode(err)
}

// WantsJSONOutput reports whether cmd was invoked with --json, so error
// output matches the success format. Persistent flags inherit automatically
// via cmd.Flags().
//
// Falls back to scanning os.Args when cobra never reached the leaf — e.g.
// unknown subcommand or unknown flag at root level. Without this, `weknora
// bogus --json` would emit a human stderr line instead of the envelope the
// agent asked for.
//
// Exported so the acceptance/contract test helper can replicate Execute()'s
// envelope-printing path without having to call os.Exit-bound Execute() itself.
func WantsJSONOutput(cmd *cobra.Command) bool {
	if v, err := cmd.Flags().GetBool("json"); err == nil && v {
		return true
	}
	return argsRequestJSON(os.Args[1:])
}

// argsRequestJSON scans a flag-only slice for --json in the forms pflag
// accepts. Used as a fallback when cobra short-circuits before flag parsing
// (unknown command / unknown flag at root). Mirrors only the subset of pflag
// bool parsing relevant here — `--json=false` is treated as not-JSON,
// matching pflag.
func argsRequestJSON(args []string) bool {
	for _, a := range args {
		switch {
		case a == "--json":
			return true
		case strings.HasPrefix(a, "--json="):
			if isPflagTruthy(strings.TrimPrefix(a, "--json=")) {
				return true
			}
		}
	}
	return false
}

// isPflagTruthy mirrors pflag's bool parsing for "--flag=<v>" tokens.
// pflag delegates to strconv.ParseBool, which accepts 1/t/T/TRUE/true/True
// as truthy and 0/f/F/FALSE/false/False as falsy. Anything else errors.
func isPflagTruthy(v string) bool {
	b, err := strconv.ParseBool(v)
	return err == nil && b
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
// Exported so the acceptance/contract test helper can reuse the mapping when
// replicating Execute()'s error-envelope path in-process.
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
	"accepts ",          // ExactArgs / RangeArgs / etc. — `accepts N arg(s), received M`
	"requires at least", // MinimumNArgs
	"requires at most",  // MaximumNArgs
	"unknown flag",
	"invalid argument", // pflag type-coercion failure (e.g. --top-k=foo)
}

// NewRootCmd builds the cobra tree. Splitting it from Execute() lets tests
// drive the tree directly with their own factory. Exported so the
// acceptance/contract suite can construct the tree in-process.
func NewRootCmd(f *cmdutil.Factory) *cobra.Command {
	v, commit, date := build.Info()
	cmd := &cobra.Command{
		Use:   "weknora",
		Short: "WeKnora CLI — RAG knowledge base from your terminal",
		Long: `WeKnora CLI lets you authenticate, browse knowledge bases, and run
hybrid searches against a WeKnora server from your shell or an AI agent.`,
		Example: `  weknora auth login --host=https://kb.example.com   # one-time setup
  weknora kb list                                    # list knowledge bases
  weknora kb view <id>                               # show one
  weknora search "your question" --kb=<id>           # hybrid retrieval
  weknora doctor --json                              # health check (agent-readable)`,
		SilenceUsage:  true,
		SilenceErrors: true,
		// Version makes cobra auto-register a `--version` global flag that
		// prints this string. Mainstream CLIs (gh / kubectl / aws / gcloud)
		// all accept both `--version` and a `version` subcommand; the
		// subcommand still owns the richer `--json` envelope output.
		Version: fmt.Sprintf("%s (commit %s, built %s)", v, commit, date),
		PersistentPreRun: func(c *cobra.Command, args []string) {
			// Propagate the global --context flag into the Factory for this
			// invocation only. Spec §1.2: single-shot override, no disk write.
			if v, _ := c.Flags().GetString("context"); v != "" {
				f.ContextOverride = v
			}
		},
	}
	// Match `weknora version` line format so both forms output the same.
	cmd.SetVersionTemplate("weknora {{.Version}}\n")
	addGlobalFlags(cmd)
	cmd.SetHelpFunc(agentAwareHelpFunc(cmd.HelpFunc()))
	// Wrap cobra's flag-parsing errors as FlagError so cmdutil.ExitCode maps
	// them to exit 2 (gh-style). "unknown command" errors are detected by
	// message prefix in Execute() since cobra emits them as plain errors.
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
	cmd.AddCommand(doc.NewCmd(f))
	cmd.AddCommand(apicmd.NewCmd(f))
	cmd.AddCommand(chatcmd.NewCmd(f))
	return cmd
}

// addGlobalFlags registers persistent flags available on every subcommand.
// Only flags whose behavior is actually wired are listed — a flag that
// accepts values but does nothing is a worse contract than no flag.
func addGlobalFlags(cmd *cobra.Command) {
	pf := cmd.PersistentFlags()
	pf.BoolP("yes", "y", false, "Skip confirmation prompts on destructive operations")
	pf.String("context", "", "Override the active context for this invocation (no disk write)")
	pf.Bool("dry-run", false, "Preview the operation without executing (write commands only; read commands ignore)")
}

// agentAwareHelpFunc wraps cobra's default help to append the AI agent guidance
// (Annotations[agent.AIAgentHelpKey]) only when an AI coding agent env var is
// detected (CLAUDECODE / CURSOR_AGENT). Help-only render — no behavior switch.
// Stripe pkg/cmd/templates.go pattern, but reduced from mode-switch (v0.2 ADR-3).
func agentAwareHelpFunc(orig func(*cobra.Command, []string)) func(*cobra.Command, []string) {
	return func(c *cobra.Command, args []string) {
		orig(c, args)
		if agent.DetectAIAgent() == "" {
			return
		}
		extra := agent.FormatAgentGuidance(c)
		if extra == "" {
			return
		}
		w := c.OutOrStdout()
		fmt.Fprintln(w)
		fmt.Fprintln(w, "AI Agent guidance:")
		fmt.Fprintln(w, "  "+extra)
	}
}

// newVersionCmd is the only leaf command shipped in the foundation PR. It
// doubles as the smoke test that proves Factory + iostreams + cobra wiring works.
func newVersionCmd(f *cmdutil.Factory) *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Show CLI build metadata",
		RunE: func(c *cobra.Command, args []string) error {
			v, commit, date := build.Info()
			if jsonOut {
				return cmdutil.NewJSONExporter().Write(c.OutOrStdout(), format.Success(map[string]string{
					"version": v,
					"commit":  commit,
					"date":    date,
				}, nil))
			}
			fmt.Fprintf(c.OutOrStdout(), "weknora %s (commit %s, built %s)\n", v, commit, date)
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output JSON envelope")
	return cmd
}
