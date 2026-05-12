package contextcmd

import (
	"fmt"
	"sort"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/agent"
	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/config"
	"github.com/Tencent/WeKnora/cli/internal/format"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
)

// ListOptions captures `weknora context list` flag state.
type ListOptions struct {
	JSONOut bool
}

// listEntry is one row in the rendered table / one element of envelope.data.
type listEntry struct {
	Name    string `json:"name"`
	Host    string `json:"host"`
	User    string `json:"user,omitempty"`
	Current bool   `json:"current"`
}

// NewCmdList builds `weknora context list`. Same listing shape as gh
// `auth status` (per-host enumeration with an active marker). The kubectl
// equivalent is `config get-contexts` — same data, different surface
// convention (kubectl uses hyphenated subcommands; we use noun-verb).
// Reads only config.yaml — no network, no keyring touch.
func NewCmdList(f *cmdutil.Factory) *cobra.Command {
	opts := &ListOptions{}
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List configured contexts",
		Long: `Show every context registered in ~/.config/weknora/config.yaml. The
active context (used by subsequent commands when --context is unset) is
marked with a leading "*". No network requests are issued.

The credential mode (api-key vs password) is intentionally not shown here —
run "weknora auth list" for that. "context list" is the catalog of *where*
the CLI can talk to; "auth list" is the catalog of *how*.`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return runList(opts)
		},
	}
	cmd.Flags().BoolVar(&opts.JSONOut, "json", false, "Output JSON envelope")
	agent.SetAgentHelp(cmd, "Lists CLI contexts (name/host/user/current). Read-only, no network. Use this before context use to verify the target name exists.")
	return cmd
}

func runList(opts *ListOptions) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	entries := make([]listEntry, 0, len(cfg.Contexts))
	for name, c := range cfg.Contexts {
		entries = append(entries, listEntry{
			Name:    name,
			Host:    c.Host,
			User:    c.User,
			Current: name == cfg.CurrentContext,
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

	if opts.JSONOut {
		return format.WriteEnvelope(iostreams.IO.Out,
			format.Success(entries, &format.Meta{Context: cfg.CurrentContext}))
	}
	if len(entries) == 0 {
		fmt.Fprintln(iostreams.IO.Out, "No contexts configured. Run `weknora auth login` (or `weknora context add`) to create one.")
		return nil
	}
	tw := tabwriter.NewWriter(iostreams.IO.Out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "  NAME\tHOST\tUSER")
	for _, e := range entries {
		marker := "  "
		if e.Current {
			marker = "* "
		}
		fmt.Fprintf(tw, "%s%s\t%s\t%s\n", marker, e.Name, e.Host, format.DashIfEmpty(e.User))
	}
	return tw.Flush()
}
