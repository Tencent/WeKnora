package doc

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

// ListOptions captures `weknora doc list` flags.
type ListOptions struct {
	Page     int
	PageSize int
	JSONOut  bool
}

// ListService is the narrow SDK surface this command depends on.
// *sdk.Client satisfies it.
type ListService interface {
	ListKnowledge(ctx context.Context, kbID string, page, pageSize int, tagID string) ([]sdk.Knowledge, int64, error)
}

// listResult is the typed payload emitted under data on success.
//
// Items is non-nil even when empty (json:"[]" not "null") so agents can iterate
// without nil-checks. Page metadata is duplicated here (and not in _meta) to
// keep the payload self-describing for downstream consumers that strip _meta.
type listResult struct {
	Items    []sdk.Knowledge `json:"items"`
	Page     int             `json:"page"`
	PageSize int             `json:"page_size"`
	Total    int64           `json:"total"`
	KBID     string          `json:"kb_id"`
}

// NewCmdList builds `weknora doc list`.
func NewCmdList(f *cmdutil.Factory) *cobra.Command {
	opts := &ListOptions{}
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List documents in a knowledge base",
		Long: `Lists documents (uploaded files / web pages / inline text) in the
resolved knowledge base. KB resolution follows the standard 4-level chain:
--kb flag > WEKNORA_KB_ID env > .weknora/project.yaml > error. The --kb
flag accepts either a KB UUID (passed through) or a name (resolved via list).

Default sort is updated_at desc so the most recent uploads surface first;
backend storage order is not guaranteed and varies between deployments.`,
		Example: `  weknora doc list                                                  # uses project link / env
  weknora doc list --kb a32a63ff-fb36-4874-bcaa-30f48570a694        # explicit UUID
  weknora doc list --kb my-kb                                       # resolved by name
  weknora doc list --page 2 --json                                  # paginated envelope output`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			kbID, err := f.ResolveKB(c)
			if err != nil {
				return err
			}
			cli, err := f.Client()
			if err != nil {
				return err
			}
			return runList(c.Context(), opts, cli, kbID)
		},
	}
	// --kb is read by Factory.ResolveKB; declare it here so cobra parses the
	// value into the command's flag set.
	cmd.Flags().String("kb", "", "Knowledge base UUID or name (overrides env / project link)")
	cmd.Flags().IntVar(&opts.Page, "page", 1, "Page number (1-based)")
	cmd.Flags().IntVar(&opts.PageSize, "page-size", 20, "Items per page")
	cmd.Flags().BoolVar(&opts.JSONOut, "json", false, "Output JSON envelope")
	agent.SetAgentHelp(cmd, "Lists docs in the resolved KB. Returns data: {items, page, page_size, total, kb_id}; pass --kb when not running inside a project.")
	return cmd
}

func runList(ctx context.Context, opts *ListOptions, svc ListService, kbID string) error {
	items, total, err := svc.ListKnowledge(ctx, kbID, opts.Page, opts.PageSize, "")
	if err != nil {
		return cmdutil.Wrapf(cmdutil.ClassifyHTTPError(err), err, "list documents")
	}
	if items == nil {
		items = []sdk.Knowledge{} // ensure JSON [] not null
	}
	// Default sort: updated_at desc. Server return order is not guaranteed,
	// so client-side sort makes output deterministic regardless of backend
	// storage choices. Mirrors `weknora kb list`.
	sort.Slice(items, func(i, j int) bool {
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})

	r := listResult{
		Items:    items,
		Page:     opts.Page,
		PageSize: opts.PageSize,
		Total:    total,
		KBID:     kbID,
	}
	if opts.JSONOut {
		return format.WriteEnvelope(iostreams.IO.Out, format.Success(r, &format.Meta{KBID: kbID}))
	}

	if len(items) == 0 {
		fmt.Fprintln(iostreams.IO.Out, "(no documents)")
		return nil
	}

	tw := tabwriter.NewWriter(iostreams.IO.Out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tSTATUS\tSIZE\tUPDATED")
	now := time.Now()
	for _, k := range items {
		name := text.Truncate(40, displayName(k))
		updated := text.FuzzyAgo(now, k.UpdatedAt)
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", k.ID, name, k.ParseStatus, formatSize(k.FileSize), updated)
	}
	return tw.Flush()
}

// displayName picks the most informative human label for a knowledge entry.
// File-uploads use FileName; URL / text entries fall back to Title.
func displayName(k sdk.Knowledge) string {
	if k.FileName != "" {
		return k.FileName
	}
	if k.Title != "" {
		return k.Title
	}
	return k.ID
}

// formatSize renders a byte count as a short human string (KB / MB).
// Kept tiny on purpose — go-humanize would pull a transitive dep just for one
// column. A "-" placeholder hides zero-size entries (URL / text).
func formatSize(bytes int64) string {
	if bytes <= 0 {
		return "-"
	}
	const (
		kb = 1 << 10
		mb = 1 << 20
		gb = 1 << 30
	)
	switch {
	case bytes >= gb:
		return fmt.Sprintf("%.1fGB", float64(bytes)/float64(gb))
	case bytes >= mb:
		return fmt.Sprintf("%.1fMB", float64(bytes)/float64(mb))
	case bytes >= kb:
		return fmt.Sprintf("%.1fKB", float64(bytes)/float64(kb))
	}
	return fmt.Sprintf("%dB", bytes)
}
