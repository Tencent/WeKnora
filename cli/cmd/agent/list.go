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
	"github.com/Tencent/WeKnora/cli/internal/output"
	"github.com/Tencent/WeKnora/cli/internal/text"
	sdk "github.com/Tencent/WeKnora/client"
)

// agentListFields enumerates the fields surfaced for `--format json` discovery
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
			fopts, err := cmdutil.CheckFormatFlag(c)
			if err != nil {
				return err
			}
			fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())
			// Validate static input before building the client so a bad --limit
			// returns input.invalid_argument (exit 5), not an auth error (exit 3).
			if err := validateListOpts(opts); err != nil {
				return err
			}
			cli, err := f.Client()
			if err != nil {
				return err
			}
			return runList(c.Context(), opts, fopts, cli)
		},
	}
	cmd.Flags().IntVarP(&opts.Limit, "limit", "L", 30, "Maximum results to return — client-side cap; meta.has_more/total_count report the full size (1..10000)")
	cmdutil.AddFormatFlag(cmd, agentListFields...)
	cmdutil.SetAgentHelp(cmd, cmdutil.AgentHelp{
		UsedFor:  "List custom agents visible to the active tenant. The SDK returns all agents in one call (no server-side pagination); meta.count reflects the full tenant set, --limit caps client-side.",
		Examples: []string{"weknora agent list --format json", "weknora agent list --limit 10 --format json"},
		Output:   "envelope.data is an array of Agent objects with id, name, is_builtin; meta.total_count is the full tenant set and meta.has_more=true means --limit truncated it (raise --limit to get the rest)",
	})
	return cmd
}

// validateListOpts checks --limit. Called from RunE before the client is built
// (so a bad value surfaces as exit 5, not an auth error) and at runList's top
// for direct callers; idempotent and nil-safe.
func validateListOpts(opts *ListOptions) error {
	if opts == nil {
		return nil
	}
	if opts.Limit < 1 || opts.Limit > 10000 {
		return &cmdutil.Error{
			Code:    cmdutil.CodeInputInvalidArgument,
			Message: fmt.Sprintf("--limit must be in 1..10000, got %d", opts.Limit),
		}
	}
	return nil
}

func runList(ctx context.Context, opts *ListOptions, fopts *cmdutil.FormatOptions, svc ListService) error {
	if opts == nil {
		opts = &ListOptions{}
	}
	if err := validateListOpts(opts); err != nil {
		return err
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
	// The agent list SDK is unpaginated — it returns every agent in one
	// call — so the CLI holds the true total and can tell the caller
	// whether the client-side --limit dropped any. total_count is the full
	// count; has_more flags that --limit truncated it (raise --limit to get
	// the rest — there is no server cursor to continue with).
	total := len(items)
	truncated := false
	if opts.Limit > 0 && len(items) > opts.Limit {
		items = items[:opts.Limit]
		truncated = true
	}

	if fopts.WantsJSON() {
		meta := &output.Meta{Count: output.IntPtr(len(items)), HasMore: truncated, TotalCount: output.IntPtr(total)}
		return fopts.Emit(iostreams.IO.Out, items, meta)
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
