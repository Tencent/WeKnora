package kb

import (
	"context"
	"fmt"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/aiclient"
	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/format"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/text"
	sdk "github.com/Tencent/WeKnora/client"
)

// kbListFields enumerates the fields surfaced for `--json` discovery on
// `kb list`. Nested config structs (chunking / image / FAQ / VLM / storage
// / extract) are intentionally omitted — users wanting those can use `--jq`
// against the full envelope.
var kbListFields = []string{
	"id", "name", "type", "description",
	"is_temporary", "is_pinned",
	"embedding_model_id", "summary_model_id",
	"knowledge_count", "chunk_count",
	"is_processing", "processing_count",
	"created_at", "updated_at",
}

// ListOptions captures `kb list` filter flag state.
type ListOptions struct {
	Pinned bool // --pinned: client-side filter to KBs with IsPinned == true
	// Limit caps the returned slice client-side. 0 = no cap, 1..10000 = explicit.
	// The KB list SDK is unpaginated; --all-pages is intentionally not exposed
	// because it would be a no-op.
	Limit int
}

// ListService is the narrow SDK surface this command depends on.
type ListService interface {
	ListKnowledgeBases(ctx context.Context) ([]sdk.KnowledgeBase, error)
}

// NewCmdList builds `weknora kb list`.
func NewCmdList(f *cmdutil.Factory) *cobra.Command {
	opts := &ListOptions{}
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List knowledge bases visible to the active context",
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
	cmd.Flags().BoolVar(&opts.Pinned, "pinned", false, "Only show pinned knowledge bases")
	cmd.Flags().IntVarP(&opts.Limit, "limit", "L", 30, "Maximum results to return (0 = no cap, 1..10000 = explicit)")
	cmdutil.AddJSONFlags(cmd, kbListFields)
	aiclient.SetAgentHelp(cmd, "Lists all knowledge bases as a bare JSON array of {id, name, ...} objects (empty `[]` when none). --pinned restricts to pinned KBs (client-side filter). --limit caps the returned slice. Use `--json` (bare) for full objects, `--json id,name` to project fields, or `--jq` for arbitrary reshape.")
	return cmd
}

func runList(ctx context.Context, opts *ListOptions, jopts *cmdutil.JSONOptions, svc ListService) error {
	if opts.Limit < 0 || opts.Limit > 10000 {
		return &cmdutil.Error{
			Code:    cmdutil.CodeInputInvalidArgument,
			Message: fmt.Sprintf("--limit must be in 0..10000 (0 = no cap), got %d", opts.Limit),
		}
	}
	items, err := svc.ListKnowledgeBases(ctx)
	if err != nil {
		return cmdutil.WrapHTTP(err, "list knowledge bases")
	}
	if items == nil {
		items = []sdk.KnowledgeBase{} // ensure JSON [] not null
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
	// Spec §1.2: default sort by updated_at desc. Server return order is not
	// guaranteed, so client-side sort makes output deterministic regardless
	// of backend storage choices.
	sort.Slice(items, func(i, j int) bool {
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	// --limit applies after sort so the cap returns the top-N most-recent.
	if opts.Limit > 0 && len(items) > opts.Limit {
		items = items[:opts.Limit]
	}

	if jopts.Enabled() {
		return format.WriteJSONFiltered(iostreams.IO.Out, items, jopts.Fields, jopts.JQ)
	}

	if len(items) == 0 {
		if opts.Pinned {
			fmt.Fprintln(iostreams.IO.Out, "(no pinned knowledge bases)")
			return nil
		}
		fmt.Fprintln(iostreams.IO.Out, "(no knowledge bases)")
		return nil
	}

	tw := tabwriter.NewWriter(iostreams.IO.Out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tDOCS\tUPDATED")
	now := time.Now()
	for _, kb := range items {
		name := text.Truncate(40, kb.Name)
		docs := text.Pluralize(int(kb.KnowledgeCount), "doc")
		updated := text.FuzzyAgo(now, kb.UpdatedAt)
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", kb.ID, name, docs, updated)
	}
	return tw.Flush()
}
