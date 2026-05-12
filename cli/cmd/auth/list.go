package auth

import (
	"fmt"
	"sort"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/agent"
	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/format"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
)

type ListOptions struct {
	JSONOut bool
}

type listEntry struct {
	Name    string `json:"name"`
	Host    string `json:"host"`
	User    string `json:"user,omitempty"`
	Mode    string `json:"mode"` // "api-key" / "password" / "unknown"
	Current bool   `json:"current"`
}

// NewCmdList builds `weknora auth list`. Per-host enumeration: render one
// row per registered context, marking the active one. Reads only
// ~/.config/weknora/config.yaml — no network, no keyring touch.
func NewCmdList(f *cmdutil.Factory) *cobra.Command {
	opts := &ListOptions{}
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List configured authentication contexts",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return runList(opts, f)
		},
	}
	cmd.Flags().BoolVar(&opts.JSONOut, "json", false, "Output JSON envelope")
	agent.SetAgentHelp(cmd, "Lists configured auth contexts (name/host/user/mode/current). Read-only, no network, no keyring access. Use to confirm context names before --context or `auth login --name`.")
	return cmd
}

func runList(opts *ListOptions, f *cmdutil.Factory) error {
	cfg, err := f.Config()
	if err != nil {
		return err
	}
	entries := make([]listEntry, 0, len(cfg.Contexts))
	for name, c := range cfg.Contexts {
		entries = append(entries, listEntry{
			Name:    name,
			Host:    c.Host,
			User:    c.User,
			Mode:    inferMode(c.APIKeyRef, c.TokenRef),
			Current: name == cfg.CurrentContext,
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

	if opts.JSONOut {
		return cmdutil.NewJSONExporter().Write(iostreams.IO.Out,
			format.Success(entries, &format.Meta{Context: cfg.CurrentContext}))
	}
	if len(entries) == 0 {
		fmt.Fprintln(iostreams.IO.Out, "No contexts configured. Run `weknora auth login` to create one.")
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

// inferMode reports which credential shape the context was logged in with.
// A context with both refs set (which shouldn't happen with the current
// login flow but might appear in hand-edited configs) is treated as
// password — JWT wins because it's the more capable mode.
func inferMode(apiKeyRef, tokenRef string) string {
	switch {
	case tokenRef != "":
		return "password"
	case apiKeyRef != "":
		return "api-key"
	default:
		return "unknown"
	}
}
