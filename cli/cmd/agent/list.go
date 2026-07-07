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
)

// agentListFields enumerates the fields surfaced for `--format json` discovery
// on `agent list`. Mirrors the json tags on sdk.Agent - nested Config is
// omitted because its sub-fields make filtering noisy (use `--jq` instead).
var agentListFields = []string{
	"id", "name", "description", "avatar",
	"is_builtin", "tenant_id", "created_by",
	"is_shared", "organization_id", "org_name", "permission", "source_tenant_id", "shared_at", "disabled_by_me",
	"created_at", "updated_at",
}

// ListService is the narrow SDK surface this command depends on.
type ListService interface {
	cmdutil.VisibleAgentLister
}

// ListOptions captures `agent list` filter flag state.
type ListOptions struct {
	Owned  bool
	Shared bool
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
		Short: "List agents visible to the active profile",
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
	cmd.Flags().BoolVar(&opts.Owned, "owned", false, "Only show agents in the current tenant")
	cmd.Flags().BoolVar(&opts.Shared, "shared", false, "Only show agents received through shared spaces")
	cmd.Flags().IntVarP(&opts.Limit, "limit", "L", 30, "Maximum results to return — client-side cap; meta.has_more/total_count report the full size (1..10000)")
	cmdutil.AddFormatFlag(cmd, agentListFields...)
	cmdutil.SetAgentHelp(cmd, cmdutil.AgentHelp{
		UsedFor:  "List current-tenant and shared-space agents visible to the active profile. Use --owned or --shared to restrict the source.",
		Examples: []string{"weknora agent list --format json", "weknora agent list --shared --format json"},
		Output:   "envelope.data is an array of Agent objects; is_shared identifies shared rows and shared rows include org_name, permission, and source_tenant_id",
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
	if opts.Owned && opts.Shared {
		return cmdutil.NewError(cmdutil.CodeInputInvalidArgument,
			"--owned and --shared are mutually exclusive")
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
	items, err := cmdutil.ListVisibleAgents(ctx, svc, !opts.Shared, !opts.Owned)
	if err != nil {
		return cmdutil.WrapHTTP(err, "list agents")
	}
	if items == nil {
		items = []cmdutil.VisibleAgent{} // ensure JSON [] not null
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
		if opts.Owned {
			fmt.Fprintln(iostreams.IO.Out, "(no current-tenant agents)")
			return nil
		}
		if opts.Shared {
			fmt.Fprintln(iostreams.IO.Out, "(no shared agents)")
			return nil
		}
		fmt.Fprintln(iostreams.IO.Out, "(no agents)")
		return nil
	}

	tw := tabwriter.NewWriter(iostreams.IO.Out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tSOURCE\tACCESS\tBUILTIN\tUPDATED")
	now := time.Now()
	for _, a := range items {
		name := text.Truncate(40, a.Name)
		source := "owned"
		access := "-"
		if a.IsShared {
			source = "shared"
			if a.OrgName != "" {
				source = "shared:" + text.Truncate(24, a.OrgName)
			}
			access = a.Permission
		}
		builtin := "-"
		if a.IsBuiltin {
			builtin = "yes"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", a.ID, name, source, access, builtin, text.FuzzyAgo(now, a.UpdatedAt))
	}
	return tw.Flush()
}
