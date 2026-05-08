package kb

import (
	"context"
	"fmt"
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

// ListOptions captures `weknora kb list` flags.
type ListOptions struct {
	JSONOut bool
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
			cli, err := f.Client()
			if err != nil {
				return err
			}
			return runList(c.Context(), opts, cli)
		},
	}
	cmd.Flags().BoolVar(&opts.JSONOut, "json", false, "Output JSON envelope")
	agent.SetAgentHelp(cmd, "Lists all knowledge bases. Returns data.items: [{id, name, ...}]; empty array when none.")
	return cmd
}

func runList(ctx context.Context, opts *ListOptions, svc ListService) error {
	items, err := svc.ListKnowledgeBases(ctx)
	if err != nil {
		return cmdutil.Wrapf(cmdutil.ClassifyHTTPError(err), err, "list knowledge bases")
	}
	if items == nil {
		items = []sdk.KnowledgeBase{} // ensure JSON [] not null
	}

	if opts.JSONOut {
		return format.WriteEnvelope(iostreams.IO.Out, format.Success(listResult{Items: items}, nil))
	}

	if len(items) == 0 {
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
