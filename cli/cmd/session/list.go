package sessioncmd

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

const (
	defaultPage     = 1
	defaultPageSize = 30
	maxPageSize     = 1000
)

type ListOptions struct {
	Page     int
	PageSize int
	JSONOut  bool
}

// ListService is the narrow SDK surface this command depends on.
type ListService interface {
	GetSessionsByTenant(ctx context.Context, page, pageSize int) ([]sdk.Session, int, error)
}

// listResult is the typed payload emitted under data.
type listResult struct {
	Items []sdk.Session `json:"items"`
}

// NewCmdList builds `weknora session list`. Paginated; defaults to page=1
// page_size=30. No cursor — server is page-based today.
func NewCmdList(f *cmdutil.Factory) *cobra.Command {
	opts := &ListOptions{Page: defaultPage, PageSize: defaultPageSize}
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List chat sessions for the active context",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			cli, err := f.Client()
			if err != nil {
				return err
			}
			return runList(c.Context(), opts, cli)
		},
	}
	cmd.Flags().IntVar(&opts.Page, "page", defaultPage, "Page number (1-indexed)")
	cmd.Flags().IntVar(&opts.PageSize, "page-size", defaultPageSize, "Items per page (1..1000)")
	cmd.Flags().BoolVar(&opts.JSONOut, "json", false, "Output JSON envelope")
	agent.SetAgentHelp(cmd, "Lists chat sessions. _meta.has_more is set when more pages exist; bump --page and retry to walk them.")
	return cmd
}

func runList(ctx context.Context, opts *ListOptions, svc ListService) error {
	if opts.Page < 1 {
		return &cmdutil.Error{
			Code:    cmdutil.CodeInputInvalidArgument,
			Message: fmt.Sprintf("--page must be >= 1, got %d", opts.Page),
		}
	}
	if opts.PageSize < 1 || opts.PageSize > maxPageSize {
		return &cmdutil.Error{
			Code:    cmdutil.CodeInputInvalidArgument,
			Message: fmt.Sprintf("--page-size must be in 1..%d, got %d", maxPageSize, opts.PageSize),
		}
	}

	items, total, err := svc.GetSessionsByTenant(ctx, opts.Page, opts.PageSize)
	if err != nil {
		return cmdutil.WrapHTTP(err, "list sessions")
	}
	if items == nil {
		items = []sdk.Session{} // JSON [] not null
	}

	if opts.JSONOut {
		meta := &format.Meta{HasMore: opts.Page*opts.PageSize < total}
		return format.WriteEnvelope(iostreams.IO.Out, format.Success(listResult{Items: items}, meta))
	}

	if len(items) == 0 {
		fmt.Fprintln(iostreams.IO.Out, "(no sessions)")
		return nil
	}
	tw := tabwriter.NewWriter(iostreams.IO.Out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tTITLE\tUPDATED")
	now := time.Now()
	for _, s := range items {
		title := text.Truncate(50, s.Title)
		if title == "" {
			title = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", s.ID, title, fuzzyTime(now, s.UpdatedAt))
	}
	return tw.Flush()
}

// fuzzyTime renders a server-provided timestamp string in "2d ago" form.
// Returns the raw input if parsing fails — better to surface the unknown
// format than to silently render "-".
func fuzzyTime(now time.Time, ts string) string {
	if ts == "" {
		return "-"
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ts
	}
	return text.FuzzyAgo(now, t)
}
