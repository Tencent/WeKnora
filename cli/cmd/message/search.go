package messagecmd

import (
	"context"
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/output"
	"github.com/Tencent/WeKnora/cli/internal/text"
	sdk "github.com/Tencent/WeKnora/client"
)

// messageSearchFields enumerates the projectable fields of MessageSearchGroupItem.
var messageSearchFields = []string{
	"request_id", "session_id", "session_title", "query_content",
	"answer_content", "score", "match_type", "created_at",
}

// messageSearchModes is the closed set the server accepts for --mode, sourced
// from the SDK enumerator so it can't drift from the server vocabulary.
var messageSearchModes = cmdutil.EnumStrings(sdk.AllMessageSearchModes())

// SearchOptions holds the parsed flag values for `message search`.
type SearchOptions struct {
	Query      string
	Mode       string // server-defined search mode, passed through verbatim
	Limit      int
	SessionIDs []string
}

// SearchService is the narrow SDK surface this command depends on.
type SearchService interface {
	SearchMessages(ctx context.Context, req *sdk.SearchMessagesRequest) (*sdk.MessageSearchResult, error)
}

// NewCmdSearch builds `weknora message search "<query>"`.
func NewCmdSearch(f *cmdutil.Factory) *cobra.Command {
	opts := &SearchOptions{}
	cmd := &cobra.Command{
		Use:   `search "<query>"`,
		Short: "Search chat history across sessions (question/answer pairs)",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			fopts, err := cmdutil.CheckFormatFlag(c)
			if err != nil {
				return err
			}
			fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())
			opts.Query = args[0]
			// Validate static input (--limit / --mode) before building the
			// client so a bad value returns input.invalid_argument (exit 5)
			// instead of an auth error (exit 3) when the profile is
			// unconfigured — an agent must see the real, fixable problem.
			if err := validateSearchOpts(opts); err != nil {
				return err
			}
			cli, err := f.Client()
			if err != nil {
				return err
			}
			return runSearch(c.Context(), opts, fopts, cli)
		},
	}
	cmd.Flags().IntVarP(&opts.Limit, "limit", "L", 20, "Maximum results to return — server-side limit (1..1000; default 20 mirrors the server default)")
	cmd.Flags().StringVar(&opts.Mode, "mode", "", "Search mode: keyword | vector | hybrid (omit for server default: hybrid)")
	cmd.Flags().StringArrayVar(&opts.SessionIDs, "session", nil, "Restrict search to these session ids (repeatable)")
	cmdutil.AddFormatFlag(cmd, messageSearchFields...)
	cmdutil.SetAgentHelp(cmd, cmdutil.AgentHelp{
		UsedFor:       "Search past Q&A pairs across chat history. This searches conversation messages — for RAG retrieval over documents use `weknora search chunks` instead.",
		RequiredFlags: []string{"<query> (positional)"},
		Examples: []string{
			`weknora message search "deploy steps"`,
			`weknora message search "deploy steps" --session sess_abc --limit 5`,
		},
		Output: "envelope.data is an array of grouped results (request_id, session_id, query_content, answer_content, score); meta.total_count is the server-side total. --mode accepts keyword | vector | hybrid (server default: hybrid)",
	})
	return cmd
}

// validateSearchOpts checks --limit / --mode and normalizes --mode to the
// canonical form. Called from RunE before the client is built (so bad input
// surfaces as exit 5, not an auth error) and again at the top of runSearch
// for direct callers; it is idempotent.
func validateSearchOpts(opts *SearchOptions) error {
	// 1..1000 mirrors `search chunks` (the sibling search command); the
	// server itself does not cap limit (handler passes it through).
	if opts.Limit < 1 || opts.Limit > 1000 {
		return &cmdutil.Error{
			Code:    cmdutil.CodeInputInvalidArgument,
			Message: fmt.Sprintf("--limit must be in 1..1000, got %d", opts.Limit),
		}
	}
	// Validate --mode against the closed server enum and normalize to the
	// canonical (lowercase) form the server matches. An unrecognised mode
	// matches no channel server-side and returns an empty set with no error —
	// which an agent cannot tell apart from a genuine no-match — so reject it.
	mode, err := cmdutil.ValidateEnum("mode", opts.Mode, messageSearchModes)
	if err != nil {
		return err
	}
	opts.Mode = mode
	return nil
}

func runSearch(ctx context.Context, opts *SearchOptions, fopts *cmdutil.FormatOptions, svc SearchService) error {
	if err := validateSearchOpts(opts); err != nil {
		return err
	}
	res, err := svc.SearchMessages(ctx, &sdk.SearchMessagesRequest{
		Query:      opts.Query,
		Mode:       opts.Mode,
		Limit:      opts.Limit,
		SessionIDs: opts.SessionIDs,
	})
	if err != nil {
		return cmdutil.WrapHTTP(err, "search messages")
	}
	if res == nil {
		res = &sdk.MessageSearchResult{}
	}
	items := res.Items
	if items == nil {
		items = []*sdk.MessageSearchGroupItem{}
	}
	if fopts.WantsJSON() {
		meta := &output.Meta{Count: output.IntPtr(len(items)), TotalCount: output.IntPtr(res.Total)}
		return fopts.Emit(iostreams.IO.Out, items, meta)
	}
	if len(items) == 0 {
		fmt.Fprintln(iostreams.IO.Out, "(no matches)")
		return nil
	}
	tw := tabwriter.NewWriter(iostreams.IO.Out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SESSION ID\tQUERY\tANSWER\tSCORE")
	for _, it := range items {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%.2f\n", it.SessionID, text.OneLine(40, it.QueryContent), text.OneLine(40, it.AnswerContent), it.Score)
	}
	return tw.Flush()
}
