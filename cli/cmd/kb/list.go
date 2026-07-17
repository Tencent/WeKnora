package kb

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

// kbListFields enumerates the fields surfaced for `--format json` discovery on
// `kb list`. Nested config structs (chunking / image / FAQ / VLM / storage
// / extract) are intentionally omitted - users wanting those can use `--jq`
// against the full object.
var kbListFields = []string{
	"id", "name", "type", "description",
	"is_temporary", "is_pinned",
	"is_shared", "organization_id", "org_name", "permission", "source_tenant_id", "shared_at",
	"embedding_model_id", "summary_model_id",
	"knowledge_count", "chunk_count",
	"is_processing", "processing_count",
	"created_at", "updated_at",
}

// ListOptions captures `kb list` filter flag state.
type ListOptions struct {
	Pinned bool // --pinned: client-side filter to KBs with IsPinned == true
	Owned  bool // --owned: only current-workspace KBs
	Shared bool // --shared: only KBs received through shared spaces
	// Limit caps the returned slice client-side. 0 = no cap, 1..10000 = explicit.
	// The KB list SDK is unpaginated; --all-pages is intentionally not exposed
	// because it would be a no-op.
	Limit int
}

// ListService is the narrow SDK surface this command depends on.
type ListService interface {
	cmdutil.VisibleKBLister
}

// NewCmdList builds `weknora kb list`.
func NewCmdList(f *cmdutil.Factory) *cobra.Command {
	opts := &ListOptions{}
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List knowledge bases visible to the active profile",
		Long: `List current-workspace and shared-space knowledge bases visible to the active
profile, sorted by most recently updated. Pass --owned or --shared to restrict
the source, and --pinned to restrict to pinned KBs.`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			fopts, err := cmdutil.CheckFormatFlag(c)
			if err != nil {
				return err
			}
			fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())
			// Validate static input before building the client so a bad --limit
			// returns input.invalid_argument (exit 5) rather than an auth error
			// (exit 3) when no profile is configured.
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
	cmd.Flags().BoolVar(&opts.Pinned, "pinned", false, "Only show pinned knowledge bases")
	cmd.Flags().BoolVar(&opts.Owned, "owned", false, "Only show knowledge bases in the current workspace")
	cmd.Flags().BoolVar(&opts.Shared, "shared", false, "Only show knowledge bases received through shared spaces")
	cmd.Flags().IntVarP(&opts.Limit, "limit", "L", 30, "Maximum results to return — client-side cap; meta.has_more/total_count report the full size (1..10000)")
	cmdutil.AddFormatFlag(cmd, kbListFields...)
	cmdutil.SetAgentHelp(cmd, cmdutil.AgentHelp{
		UsedFor:  "List current-workspace and shared-space knowledge bases visible to the active profile. Use --owned or --shared to restrict the source.",
		Examples: []string{"weknora kb list --format json", "weknora kb list --shared --format json"},
		Output:   "envelope.data is an array of KnowledgeBase objects; is_shared identifies shared rows and shared rows include organization_id, org_name, permission, and source_tenant_id; meta.total_count is the full selected set",
	})
	return cmd
}

// validateListOpts checks --limit. Called from RunE before the client is built
// (so a bad value surfaces as exit 5, not an auth error) and at runList's top
// for direct callers; idempotent.
func validateListOpts(opts *ListOptions) error {
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
	if err := validateListOpts(opts); err != nil {
		return err
	}
	items, err := cmdutil.ListVisibleKnowledgeBases(ctx, svc, !opts.Shared, !opts.Owned)
	if err != nil {
		return cmdutil.WrapHTTP(err, "list knowledge bases")
	}
	if items == nil {
		items = []cmdutil.VisibleKnowledgeBase{} // ensure JSON [] not null
	}
	if opts.Pinned {
		filtered := items[:0]
		for _, kb := range items {
			if kb.IsPinned {
				filtered = append(filtered, kb)
			}
		}
		items = filtered
	}
	// Default sort by updated_at desc. Server return order is not
	// guaranteed, so client-side sort makes output deterministic regardless
	// of backend storage choices.
	sort.Slice(items, func(i, j int) bool {
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	// The KB list SDK is unpaginated — it returns every KB in one call —
	// so the CLI holds the true total and can tell the caller whether the
	// client-side --limit dropped any. total_count is the full count;
	// has_more flags that --limit truncated it (raise --limit to get the
	// rest — there is no server cursor to continue with).
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
		if opts.Pinned {
			fmt.Fprintln(iostreams.IO.Out, "(no pinned knowledge bases)")
			return nil
		}
		if opts.Owned {
			fmt.Fprintln(iostreams.IO.Out, "(no current-workspace knowledge bases)")
			return nil
		}
		if opts.Shared {
			fmt.Fprintln(iostreams.IO.Out, "(no shared knowledge bases)")
			return nil
		}
		fmt.Fprintln(iostreams.IO.Out, "(no knowledge bases)")
		return nil
	}

	tw := tabwriter.NewWriter(iostreams.IO.Out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tSOURCE\tACCESS\tDOCS\tUPDATED")
	now := time.Now()
	for _, kb := range items {
		name := text.Truncate(40, kb.Name)
		source := "owned"
		access := "-"
		if kb.IsShared {
			source = "shared"
			if kb.OrgName != "" {
				source = "shared:" + text.Truncate(24, kb.OrgName)
			}
			access = kb.Permission
		}
		docs := text.Pluralize(int(kb.KnowledgeCount), "doc")
		updated := text.FuzzyAgo(now, kb.UpdatedAt)
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", kb.ID, name, source, access, docs, updated)
	}
	return tw.Flush()
}
