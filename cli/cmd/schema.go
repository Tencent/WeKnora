package cmd

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/output"
)

// schemaRisk mirrors the destructive-write risk annotation in the schema view.
type schemaRisk struct {
	Level  string `json:"level"`
	Action string `json:"action"`
}

// schemaFlag is one command-local flag in the schema view.
type schemaFlag struct {
	Name      string `json:"name"`
	Shorthand string `json:"shorthand,omitempty"`
	Type      string `json:"type"`
	Default   string `json:"default,omitempty"`
	Usage     string `json:"usage,omitempty"`
}

// commandSchema is the full machine-readable contract for one command. It is a
// superset of cmdutil.AgentHelp augmented with the resolved command path, the
// destructive-write risk, and the command's local flags — so an agent can
// learn how to invoke a command without scraping prose help.
type commandSchema struct {
	Command       string       `json:"command"`
	UsedFor       string       `json:"used_for,omitempty"`
	RequiredFlags []string     `json:"required_flags,omitempty"`
	Examples      []string     `json:"examples,omitempty"`
	Output        string       `json:"output,omitempty"`
	Warnings      []string     `json:"warnings,omitempty"`
	Risk          *schemaRisk  `json:"risk,omitempty"`
	Flags         []schemaFlag `json:"flags,omitempty"`
}

// schemaIndexEntry is one row of the no-argument `weknora schema` index.
type schemaIndexEntry struct {
	Command string `json:"command"`
	UsedFor string `json:"used_for,omitempty"`
}

// newCmdSchema builds the `weknora schema` introspection command. With no args
// it lists every leaf command and its purpose; with a command path it prints
// that command's full contract. This makes the structured help (otherwise only
// reachable via WEKNORA_AGENT_HELP=1 on --help) a first-class, discoverable
// command — mirroring how mainstream agent-first CLIs expose schema introspection.
func newCmdSchema() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "schema [command...]",
		Short: "Machine-readable contract for a command (or the whole surface)",
		Long: `Print the machine-readable contract for a command, or — with no argument —
an index of every command and what it is used for.

  weknora schema                 # index: every leaf command + used_for
  weknora schema kb create       # the contract for one command
  weknora schema doc update      # used_for, flags, examples, output, risk

This is the discoverable form of WEKNORA_AGENT_HELP=1 <cmd> --help: an agent
can enumerate the surface and learn how to call any command without scraping
human help prose.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(c *cobra.Command, args []string) error {
			fopts, err := cmdutil.CheckFormatFlag(c)
			if err != nil {
				return err
			}
			fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())
			root := c.Root()
			if len(args) == 0 {
				return emitSchemaIndex(root, fopts)
			}
			target, err := resolveSchemaTarget(root, args)
			if err != nil {
				return err
			}
			return emitCommandSchema(target, fopts)
		},
	}
	cmdutil.AddFormatFlag(cmd, "command", "used_for", "required_flags", "examples", "output", "warnings", "risk", "flags")
	cmdutil.SetAgentHelp(cmd, cmdutil.AgentHelp{
		UsedFor: "introspect the CLI surface: list every command (no args) or print one command's contract (used_for, flags, examples, output, risk).",
		Examples: []string{
			"weknora schema",
			"weknora schema kb create",
			"weknora schema doc update --format json",
		},
		Output: "with no args, envelope.data is an array of {command, used_for}; with a command path, envelope.data is {command, used_for, required_flags, examples, output, warnings, risk, flags}",
	})
	return cmd
}

// resolveSchemaTarget walks the command tree to the command named by args.
// Returns a typed input.unknown_subcommand error (with did-you-mean) when the
// path does not resolve to a real command.
func resolveSchemaTarget(root *cobra.Command, args []string) (*cobra.Command, error) {
	// Tolerate the quoted multi-word form the no-arg `schema` index prints as a
	// command label (e.g. `schema "agent create"`): re-split each arg on
	// whitespace so an agent can paste a label verbatim and resolve it the same
	// as `schema agent create`.
	flat := make([]string, 0, len(args))
	for _, a := range args {
		flat = append(flat, strings.Fields(a)...)
	}
	args = flat

	target, rest, err := root.Find(args)
	// Find returns root (with the args unconsumed) when nothing matched; a
	// fully-resolved leaf returns itself with its positional args as rest.
	if err != nil || target == nil || target == root {
		unknown := args[0]
		available := availableSubcommandNames(root)
		hint := fmt.Sprintf("available top-level commands: %s", strings.Join(available, ", "))
		if sug := cmdutil.SuggestClosest(unknown, available); len(sug) > 0 {
			hint = fmt.Sprintf("did you mean: %s? (run `weknora schema` to list all)", strings.Join(sug, ", "))
		}
		return nil, cmdutil.NewError(cmdutil.CodeInputUnknownSubcommand,
			fmt.Sprintf("no command named %q", strings.Join(args, " "))).
			WithHint(hint).
			WithRetryArgv([]string{"weknora", "schema"})
	}
	// Leftover args that are not the command's own resource positionals mean a
	// deeper path was requested that doesn't exist (e.g. `schema kb bogus`).
	if len(rest) > 0 && target.HasSubCommands() {
		return nil, cmdutil.NewError(cmdutil.CodeInputUnknownSubcommand,
			fmt.Sprintf("no subcommand %q under %q", rest[0], strings.TrimPrefix(target.CommandPath(), "weknora "))).
			WithHint(fmt.Sprintf("available: %s", strings.Join(availableSubcommandNames(target), ", "))).
			WithRetryArgv(append([]string{"weknora", "schema"}, strings.Fields(strings.TrimPrefix(target.CommandPath(), "weknora "))...))
	}
	return target, nil
}

// emitCommandSchema renders one command's contract.
func emitCommandSchema(target *cobra.Command, fopts *cmdutil.FormatOptions) error {
	cs := buildCommandSchema(target)
	if fopts.WantsJSON() {
		return fopts.Emit(iostreams.IO.Out, cs, nil)
	}
	return writeCommandSchemaText(iostreams.IO.Out, cs)
}

// buildCommandSchema assembles the schema view from the command's AgentHelp,
// risk annotation, and local (non-inherited) flags.
func buildCommandSchema(target *cobra.Command) commandSchema {
	cs := commandSchema{Command: strings.TrimPrefix(target.CommandPath(), "weknora ")}
	if ah, ok := cmdutil.AgentHelpFor(target); ok {
		cs.UsedFor = ah.UsedFor
		cs.RequiredFlags = ah.RequiredFlags
		cs.Examples = ah.Examples
		cs.Output = ah.Output
		cs.Warnings = ah.Warnings
	} else {
		cs.UsedFor = target.Short
	}
	if level, action, ok := cmdutil.GetRisk(target); ok {
		cs.Risk = &schemaRisk{Level: level, Action: action}
	}
	target.LocalFlags().VisitAll(func(f *pflag.Flag) {
		if f.Hidden {
			return
		}
		cs.Flags = append(cs.Flags, schemaFlag{
			Name:      f.Name,
			Shorthand: f.Shorthand,
			Type:      f.Value.Type(),
			Default:   f.DefValue,
			Usage:     f.Usage,
		})
	})
	return cs
}

// writeCommandSchemaText renders one command's contract as readable prose for
// the human (--format text) path.
func writeCommandSchemaText(w io.Writer, cs commandSchema) error {
	fmt.Fprintf(w, "weknora %s\n", cs.Command)
	if cs.UsedFor != "" {
		fmt.Fprintf(w, "\n%s\n", cs.UsedFor)
	}
	if cs.Risk != nil {
		fmt.Fprintf(w, "\nRisk: %s (%s) — confirmation-gated (exit 10) unless -y\n", cs.Risk.Action, cs.Risk.Level)
	}
	if len(cs.RequiredFlags) > 0 {
		fmt.Fprintf(w, "\nRequired:\n")
		for _, r := range cs.RequiredFlags {
			fmt.Fprintf(w, "  - %s\n", r)
		}
	}
	if len(cs.Flags) > 0 {
		fmt.Fprintf(w, "\nFlags:\n")
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		for _, f := range cs.Flags {
			name := "--" + f.Name
			if f.Shorthand != "" {
				name += ", -" + f.Shorthand
			}
			fmt.Fprintf(tw, "  %s\t%s\t%s\n", name, f.Type, f.Usage)
		}
		_ = tw.Flush()
	}
	if cs.Output != "" {
		fmt.Fprintf(w, "\nOutput: %s\n", cs.Output)
	}
	if len(cs.Examples) > 0 {
		fmt.Fprintf(w, "\nExamples:\n")
		for _, e := range cs.Examples {
			fmt.Fprintf(w, "  %s\n", e)
		}
	}
	if len(cs.Warnings) > 0 {
		fmt.Fprintf(w, "\nAI agents:\n")
		for _, msg := range cs.Warnings {
			fmt.Fprintf(w, "  - %s\n", msg)
		}
	}
	return nil
}

// emitSchemaIndex lists every leaf command and its purpose, sorted by path.
func emitSchemaIndex(root *cobra.Command, fopts *cmdutil.FormatOptions) error {
	var entries []schemaIndexEntry
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			if sub.Hidden || sub.Name() == "help" || sub.Name() == "completion" {
				continue
			}
			if sub.HasSubCommands() {
				walk(sub)
				continue
			}
			e := schemaIndexEntry{Command: strings.TrimPrefix(sub.CommandPath(), "weknora ")}
			if ah, ok := cmdutil.AgentHelpFor(sub); ok {
				e.UsedFor = ah.UsedFor
			} else {
				e.UsedFor = sub.Short
			}
			entries = append(entries, e)
		}
	}
	walk(root)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Command < entries[j].Command })

	if fopts.WantsJSON() {
		return fopts.Emit(iostreams.IO.Out, entries, &output.Meta{Count: output.IntPtr(len(entries))})
	}
	for _, e := range entries {
		fmt.Fprintf(iostreams.IO.Out, "%-28s %s\n", e.Command, e.UsedFor)
	}
	return nil
}
