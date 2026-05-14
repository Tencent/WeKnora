package agentcmd

import (
	"context"
	"fmt"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/agent"
	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/format"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/text"
	sdk "github.com/Tencent/WeKnora/client"
)

// agentListFields enumerates the fields surfaced for `--json` discovery
// on `agent list`. Mirrors the json tags on sdk.Agent — nested Config is
// omitted because its sub-fields make filtering noisy (use `--jq` instead).
var agentListFields = []string{
	"id", "name", "description", "avatar",
	"is_builtin", "tenant_id", "created_by",
	"created_at", "updated_at",
}

// ListService is the narrow SDK surface this command depends on.
type ListService interface {
	ListAgents(ctx context.Context) ([]sdk.Agent, error)
}

// listResult is the typed payload emitted under data.items.
type listResult struct {
	Items []sdk.Agent `json:"items"`
}

// NewCmdList builds `weknora agent list`.
func NewCmdList(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List custom agents visible to the active tenant",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			jopts, err := cmdutil.CheckJSONFlags(c)
			if err != nil {
				return err
			}
			cli, err := f.Client()
			if err != nil {
				return err
			}
			return runList(c.Context(), jopts, cli)
		},
	}
	cmdutil.AddJSONFlags(cmd, agentListFields)
	agent.SetAgentHelp(cmd, "Lists tenant-visible agents (built-in + custom). Returns data.items: [{id, name, description, ...}]; empty array when none. Use --json id,name to project, --jq for arbitrary reshape.")
	return cmd
}

func runList(ctx context.Context, jopts *cmdutil.JSONOptions, svc ListService) error {
	items, err := svc.ListAgents(ctx)
	if err != nil {
		return cmdutil.WrapHTTP(err, "list agents")
	}
	if items == nil {
		items = []sdk.Agent{} // ensure JSON [] not null
	}
	// Default sort: updated_at desc — most recently-edited agents surface
	// first. Mirrors kb list / doc list behavior.
	sort.Slice(items, func(i, j int) bool {
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})

	if jopts.Enabled() {
		return format.WriteEnvelopeFiltered(
			iostreams.IO.Out,
			format.Success(listResult{Items: items}, nil),
			jopts.Fields, jopts.JQ,
		)
	}

	if len(items) == 0 {
		fmt.Fprintln(iostreams.IO.Out, "(no agents)")
		return nil
	}

	tw := tabwriter.NewWriter(iostreams.IO.Out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tBUILTIN\tUPDATED")
	now := time.Now()
	for _, a := range items {
		name := text.Truncate(40, a.Name)
		builtin := "-"
		if a.IsBuiltin {
			builtin = "yes"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", a.ID, name, builtin, text.FuzzyAgo(now, a.UpdatedAt))
	}
	return tw.Flush()
}
