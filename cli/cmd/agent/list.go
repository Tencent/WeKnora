package agentcmd

import (
	"context"
	"fmt"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/text"
	sdk "github.com/Tencent/WeKnora/client"
)

// agentListFields enumerates the fields surfaced for `--json` discovery
// on `agent list`. Mirrors the json tags on sdk.Agent - nested Config is
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

// ListOptions captures `agent list` filter flag state.
type ListOptions struct {
	// Limit caps the returned slice client-side. 0 = no cap, 1..10000 = explicit.
	// The agent list SDK is unpaginated; --all-pages is intentionally not
	// exposed because it would be a no-op.
	Limit int
}

// NewCmdList builds `weknora agent list`.
func NewCmdList(f *cmdutil.Factory) *cobra.Command {
	opts := &ListOptions{}
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
			return runList(c.Context(), opts, jopts, cli)
		},
	}
	cmd.Flags().IntVarP(&opts.Limit, "limit", "L", 30, "Maximum results to return (0 = no cap, 1..10000 = explicit)")
	cmdutil.AddJSONFlags(cmd, agentListFields)
	return cmd
}

func runList(ctx context.Context, opts *ListOptions, jopts *cmdutil.JSONOptions, svc ListService) error {
	if opts == nil {
		opts = &ListOptions{}
	}
	if opts.Limit < 0 || opts.Limit > 10000 {
		return &cmdutil.Error{
			Code:    cmdutil.CodeInputInvalidArgument,
			Message: fmt.Sprintf("--limit must be in 0..10000 (0 = no cap), got %d", opts.Limit),
		}
	}
	items, err := svc.ListAgents(ctx)
	if err != nil {
		return cmdutil.WrapHTTP(err, "list agents")
	}
	if items == nil {
		items = []sdk.Agent{} // ensure JSON [] not null
	}
	// Default sort: updated_at desc - most recently-edited agents surface
	// first. Mirrors kb list / doc list behavior.
	sort.Slice(items, func(i, j int) bool {
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	// --limit applies after sort so the cap returns the top-N most-recent.
	if opts.Limit > 0 && len(items) > opts.Limit {
		items = items[:opts.Limit]
	}

	if jopts.Enabled() {
		return jopts.Emit(iostreams.IO.Out, items)
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
