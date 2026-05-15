package sessioncmd

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

const (
	defaultFullLimit = 50
	maxFullLimit     = 1000
)

// sessionViewFields enumerates the fields surfaced for `--json` discovery on
// `session view`. Mirrors sdk.Session json tags; adds the synthesized
// `messages` projection surfaced by `--full`.
var sessionViewFields = []string{
	"id", "tenant_id", "title", "description", "created_at", "updated_at",
	"messages",
}

type ViewOptions struct {
	// Full instructs runView to fetch chat history via LoadMessages and
	// render it after the session metadata.
	Full bool
	// Limit caps the number of messages loaded when Full is true.
	// Must be 1..maxFullLimit.
	Limit int
	// LimitSet records whether the caller explicitly set --limit, so we
	// can reject `--limit` without `--full` (vs. silently ignoring the
	// default).
	LimitSet bool
}

// ViewService is the narrow SDK surface this command depends on. LoadMessages
// is only invoked under --full but lives on the same interface so the runView
// dependency surface stays minimal.
type ViewService interface {
	GetSession(ctx context.Context, id string) (*sdk.Session, error)
	LoadMessages(ctx context.Context, sessionID string, limit int, beforeTime *time.Time) ([]sdk.Message, error)
}

// NewCmdView builds `weknora session view <id>`. Renders session metadata
// only by default. With `--full`, also loads the chat history via
// `LoadMessages` and renders messages (or projects them into the JSON
// payload under `messages`).
func NewCmdView(f *cmdutil.Factory) *cobra.Command {
	opts := &ViewOptions{Limit: defaultFullLimit}
	cmd := &cobra.Command{
		Use:   "view <id>",
		Short: "Show a chat session by ID",
		Long: `Show a chat session.

By default renders the session metadata (id, title, description, timestamps).

Pass --full to also load the chat history (LoadMessages SDK call). Use
--limit to cap the number of messages loaded (1..1000, default 50).
--limit without --full is rejected as input.invalid_argument.`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			opts.LimitSet = c.Flags().Changed("limit")
			jopts, err := cmdutil.CheckJSONFlags(c)
			if err != nil {
				return err
			}
			cli, err := f.Client()
			if err != nil {
				return err
			}
			return runView(c.Context(), opts, jopts, cli, args[0])
		},
	}
	cmd.Flags().BoolVar(&opts.Full, "full", false, "Also load chat history via LoadMessages")
	cmd.Flags().IntVar(&opts.Limit, "limit", defaultFullLimit, "Max messages to load when --full is set (1..1000)")
	cmdutil.AddJSONFlags(cmd, sessionViewFields)
	return cmd
}

func runView(ctx context.Context, opts *ViewOptions, jopts *cmdutil.JSONOptions, svc ViewService, id string) error {
	if !opts.Full && opts.LimitSet {
		return &cmdutil.Error{
			Code:    cmdutil.CodeInputInvalidArgument,
			Message: "--limit requires --full",
		}
	}
	if opts.Full {
		if opts.Limit < 1 || opts.Limit > maxFullLimit {
			return &cmdutil.Error{
				Code:    cmdutil.CodeInputInvalidArgument,
				Message: fmt.Sprintf("--limit must be in 1..%d, got %d", maxFullLimit, opts.Limit),
			}
		}
	}

	s, err := svc.GetSession(ctx, id)
	if err != nil {
		return cmdutil.WrapHTTP(err, "get session %q", id)
	}

	var msgs []sdk.Message
	if opts.Full {
		msgs, err = svc.LoadMessages(ctx, id, opts.Limit, nil)
		if err != nil {
			return cmdutil.WrapHTTP(err, "load messages for session %q", id)
		}
		if msgs == nil {
			msgs = []sdk.Message{}
		}
	}

	if jopts.Enabled() {
		if !opts.Full {
			return jopts.Emit(iostreams.IO.Out, s)
		}
		// Project session + messages into a single bare object. Use the
		// SDK json tags via an embedded *Session so existing keys stay
		// stable.
		payload := struct {
			*sdk.Session
			Messages []sdk.Message `json:"messages"`
		}{Session: s, Messages: msgs}
		return jopts.Emit(iostreams.IO.Out, payload)
	}

	w := iostreams.IO.Out
	fmt.Fprintf(w, "ID:        %s\n", s.ID)
	if s.Title != "" {
		fmt.Fprintf(w, "TITLE:     %s\n", s.Title)
	}
	if s.Description != "" {
		fmt.Fprintf(w, "DESC:      %s\n", s.Description)
	}
	if t, ok := parseTS(s.CreatedAt); ok {
		fmt.Fprintf(w, "CREATED:   %s\n", t.Format("2006-01-02 15:04:05"))
	} else if s.CreatedAt != "" {
		fmt.Fprintf(w, "CREATED:   %s\n", s.CreatedAt)
	}
	if t, ok := parseTS(s.UpdatedAt); ok {
		fmt.Fprintf(w, "UPDATED:   %s\n", t.Format("2006-01-02 15:04:05"))
	} else if s.UpdatedAt != "" {
		fmt.Fprintf(w, "UPDATED:   %s\n", s.UpdatedAt)
	}

	if opts.Full {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "Messages (%d):\n", len(msgs))
		for _, m := range msgs {
			fmt.Fprintln(w)
			ts := ""
			if !m.CreatedAt.IsZero() {
				ts = " " + m.CreatedAt.Format("2006-01-02 15:04:05")
			}
			fmt.Fprintf(w, "[%s]%s\n", m.Role, ts)
			if m.Content != "" {
				fmt.Fprintln(w, m.Content)
			}
		}
	}
	return nil
}

func parseTS(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}
