package messagecmd

import (
	"context"
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/output"
	"github.com/Tencent/WeKnora/cli/internal/text"
	sdk "github.com/Tencent/WeKnora/client"
)

// messageListFields enumerates the projectable scalar fields of sdk.Message
// (nested knowledge_references / agent_steps are intentionally excluded).
var messageListFields = []string{
	"id", "session_id", "request_id", "role", "content",
	"is_completed", "channel", "created_at", "updated_at",
}

type ListOptions struct {
	SessionID string
	Limit     int
	// Before filters to messages created before this RFC3339 timestamp.
	// The server pages by time cursor (limit + before_time), not by page
	// number — to walk further back, re-run with --before set to the
	// oldest created_at from the previous batch.
	Before string
}

// ListService is the narrow SDK surface this command depends on.
type ListService interface {
	LoadMessages(ctx context.Context, sessionID string, limit int, beforeTime *time.Time) ([]sdk.Message, error)
}

// NewCmdList builds `weknora message list --session <id>`.
func NewCmdList(f *cmdutil.Factory) *cobra.Command {
	opts := &ListOptions{}
	cmd := &cobra.Command{
		Use:   "list --session <session-id>",
		Short: "List messages in a chat session (newest first, time-cursor paged)",
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
	cmd.Flags().StringVar(&opts.SessionID, "session", "", "Session id to load messages from")
	_ = cmd.MarkFlagRequired("session")
	cmd.Flags().IntVarP(&opts.Limit, "limit", "L", 30, "Maximum messages to return — server-side page size (1..1000)")
	cmd.Flags().StringVar(&opts.Before, "before", "", "Only messages created before this RFC3339 timestamp (time-cursor pagination)")
	cmdutil.AddFormatFlag(cmd, messageListFields...)
	cmdutil.SetAgentHelp(cmd, cmdutil.AgentHelp{
		UsedFor:       "List messages in a session. Use the assistant message id from the latest turn to chain follow-ups (session ask), or --before <oldest created_at> to page further back.",
		RequiredFlags: []string{"--session <session-id>"},
		Examples: []string{
			"weknora message list --session sess_abc",
			"weknora message list --session sess_abc --limit 100 --before 2026-06-01T00:00:00Z",
		},
		Output: "envelope.data is an array of Message objects (id, session_id, role, content, is_completed, created_at); meta.count is the number returned. To page further back, re-run with --before <oldest created_at>; a batch shorter than --limit means the start of history was reached",
	})
	return cmd
}

// validateListOpts checks --limit. Called from RunE before the client is built
// (so a bad value surfaces as exit 5, not an auth error) and at runList's top
// for direct callers; idempotent.
func validateListOpts(opts *ListOptions) error {
	if opts.Limit < 1 || opts.Limit > 1000 {
		return &cmdutil.Error{
			Code:    cmdutil.CodeInputInvalidArgument,
			Message: fmt.Sprintf("--limit must be in 1..1000, got %d", opts.Limit),
		}
	}
	return nil
}

func runList(ctx context.Context, opts *ListOptions, fopts *cmdutil.FormatOptions, svc ListService) error {
	if err := validateListOpts(opts); err != nil {
		return err
	}
	before, err := cmdutil.ParseTimeFlag("--before", opts.Before)
	if err != nil {
		return err
	}
	items, err := svc.LoadMessages(ctx, opts.SessionID, opts.Limit, before)
	if err != nil {
		return cmdutil.WrapHTTP(err, "list messages for session %s", opts.SessionID)
	}
	if items == nil {
		items = []sdk.Message{} // JSON [] not null
	}
	if fopts.WantsJSON() {
		// No has_more here: the server exposes neither a total nor a cursor
		// for message loads, and fabricating one from len==limit would give
		// the same envelope key a second, heuristic meaning (elsewhere
		// has_more strictly means "client-side truncation happened").
		// Consumers walk back with --before until a batch comes up short.
		meta := &output.Meta{Count: output.IntPtr(len(items))}
		return fopts.Emit(iostreams.IO.Out, items, meta)
	}
	if len(items) == 0 {
		fmt.Fprintln(iostreams.IO.Out, "(no messages)")
		return nil
	}
	now := time.Now()
	tw := tabwriter.NewWriter(iostreams.IO.Out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tROLE\tCONTENT\tCREATED")
	for _, m := range items {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", m.ID, m.Role, text.OneLine(60, m.Content), text.FuzzyAgo(now, m.CreatedAt))
	}
	return tw.Flush()
}
