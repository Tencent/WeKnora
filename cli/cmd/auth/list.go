package auth

import (
	"fmt"
	"sort"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/format"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/output"
)

type ListOptions struct{}

// authListFields enumerates the fields surfaced for `--format json` discovery on
// `auth list`. Each entry is a per-profile summary row.
var authListFields = []string{
	"name", "host", "user", "mode", "current",
}

type listEntry struct {
	Name    string `json:"name"`
	Host    string `json:"host"`
	User    string `json:"user,omitempty"`
	Mode    string `json:"mode"` // ModeBearer / ModeAPIKey / ModeUnknown
	Current bool   `json:"current"`
}

// NewCmdList builds `weknora auth list`. Per-host enumeration: render one
// row per registered profile, marking the active one. Reads only
// ~/.config/weknora/config.yaml - no network, no keyring touch.
func NewCmdList(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List configured authentication profiles",
		Long:  `Show every configured profile (name, host, user, mode, current). Read-only; no network or keyring access.`,
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			fopts, err := cmdutil.CheckFormatFlag(c)
			if err != nil {
				return err
			}
			fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())
			return runList(fopts, f)
		},
	}
	cmdutil.AddFormatFlag(cmd, authListFields...)
	return cmd
}

func runList(fopts *cmdutil.FormatOptions, f *cmdutil.Factory) error {
	cfg, err := f.Config()
	if err != nil {
		return err
	}
	entries := make([]listEntry, 0, len(cfg.Profiles))
	for name, c := range cfg.Profiles {
		entries = append(entries, listEntry{
			Name:    name,
			Host:    c.Host,
			User:    c.User,
			Mode:    modeFromRefs(c.APIKeyRef, c.TokenRef),
			Current: name == cfg.CurrentProfile,
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

	if fopts.WantsJSON() {
		meta := &output.Meta{Count: len(entries)}
		return fopts.Emit(iostreams.IO.Out, entries, meta)
	}
	if len(entries) == 0 {
		fmt.Fprintln(iostreams.IO.Out, "No profiles configured. Run `weknora auth login` to create one.")
		return nil
	}
	tw := tabwriter.NewWriter(iostreams.IO.Out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "  NAME\tHOST\tUSER\tMODE")
	for _, e := range entries {
		marker := "  "
		if e.Current {
			marker = "* "
		}
		fmt.Fprintf(tw, "%s%s\t%s\t%s\t%s\n", marker, e.Name, e.Host, format.DashIfEmpty(e.User), e.Mode)
	}
	return tw.Flush()
}
