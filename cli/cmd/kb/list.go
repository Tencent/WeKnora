package kb

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
}

// ListService is the narrow SDK surface this command depends on.
type ListService interface {
	ListKnowledgeBases(ctx context.Context) ([]sdk.KnowledgeBase, error)
}

// listResult is the typed payload emitted under data.items.
type listResult struct {
	Items []sdk.KnowledgeBase `json:"items"`
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
	cmdutil.AddJSONFlags(cmd, kbListFields)
	agent.SetAgentHelp(cmd, "Lists all knowledge bases. Returns data.items: [{id, name, ...}]; empty array when none. --pinned restricts to pinned KBs (client-side filter). Use `--json` (bare) for the field list, `--json id,name` to project, or `--jq` for arbitrary reshape.")
	return cmd
}

func runList(ctx context.Context, opts *ListOptions, jopts *cmdutil.JSONOptions, svc ListService) error {
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

	if jopts.Enabled() {
		return format.WriteEnvelopeFiltered(
			iostreams.IO.Out,
			format.Success(listResult{Items: items}, nil),
			jopts.Fields, jopts.JQ,
		)
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
