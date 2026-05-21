package profilecmd

import (
	"fmt"
	"sort"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/config"
	"github.com/Tencent/WeKnora/cli/internal/format"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/output"
)

type ListOptions struct{}

// profileListFields enumerates the fields surfaced for `--format json` discovery on
// `profile list`. Each entry is a per-profile summary row.
var profileListFields = []string{
	"name", "host", "user", "current",
}

type listEntry struct {
	Name    string `json:"name"`
	Host    string `json:"host"`
	User    string `json:"user,omitempty"`
	Current bool   `json:"current"`
}

// NewCmdList builds `weknora profile list`. Per-host enumeration with an
// active marker. Reads only config.yaml - no network, no keyring touch.
func NewCmdList(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List configured profiles",
		Long: `Show every profile registered in ~/.config/weknora/config.yaml. The
active profile (used by subsequent commands when --profile is unset) is
marked with a leading "*". No network requests are issued.

The credential mode (api-key vs password) is intentionally not shown here -
run "weknora auth list" for that. "profile list" is the catalog of *where*
the CLI can talk to; "auth list" is the catalog of *how*.`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			fopts, err := cmdutil.CheckFormatFlag(c)
			if err != nil {
				return err
			}
			fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())
			return runList(fopts)
		},
	}
	cmdutil.AddFormatFlag(cmd, profileListFields...)
	return cmd
}

func runList(fopts *cmdutil.FormatOptions) error {
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

	if fopts.WantsJSON() {
		meta := &output.Meta{Count: len(entries)}
		return fopts.Emit(iostreams.IO.Out, entries, meta)
	}
	if len(entries) == 0 {
		fmt.Fprintln(iostreams.IO.Out, "No profiles configured. Run `weknora auth login` (or `weknora profile add`) to create one.")
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
