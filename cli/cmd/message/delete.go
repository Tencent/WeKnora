package messagecmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/prompt"
)

var messageDeleteFields = []string{"id", "deleted"}

// DeleteOptions holds the parsed flag/arg values for `message delete`.
type DeleteOptions struct {
	SessionID string
	MessageID string
	Yes       bool
	DryRun    bool
}

// DeleteService is the narrow SDK surface this command depends on.
type DeleteService interface {
	DeleteMessage(ctx context.Context, sessionID, messageID string) error
}

// deleteResult is the typed payload emitted on success in JSON mode.
type deleteResult struct {
	ID      string `json:"id"`
	Deleted bool   `json:"deleted"`
}

const messageDeleteLong = `Permanently delete one message from a session.

Requires both the message id (positional) and the parent session id
(--session) because the server route encodes both:
DELETE /messages/{session_id}/{id}.

Deleting an assistant message breaks the turn chain for follow-up
references (session ask). Prefer deleting whole sessions
(weknora session delete) unless you specifically need to redact one turn.

Typed exit codes:
  resource.not_found            no message with the given id under that session (exit 4)
  auth.forbidden                caller lacks delete permission on the message (exit 3)
  input.confirmation_required   destructive op without -y on a TTY (exit 10)

AI agents: this is a high-risk write. Without -y/--yes the CLI exits 10
and writes input.confirmation_required to stderr. NEVER auto-pass -y
without the user's explicit go-ahead — the exit-10 protocol exists
exactly to guard against unintended deletes.`

// NewCmdDelete builds `weknora message delete <message-id> --session <session-id>`.
func NewCmdDelete(f *cmdutil.Factory) *cobra.Command {
	opts := &DeleteOptions{}
	cmd := &cobra.Command{
		Use:   "delete <message-id> --session <session-id>",
		Short: "Delete one message from a session (high-risk write)",
		Long:  messageDeleteLong,
		Example: `  weknora message delete msg_abc --session sess_xyz                  # interactive confirm
  weknora message delete msg_abc --session sess_xyz -y               # no prompt
  weknora message delete msg_abc --session sess_xyz -y --format json # agent / JSON form`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			fopts, err := cmdutil.CheckFormatFlag(c)
			if err != nil {
				return err
			}
			fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())
			opts.MessageID = args[0]
			opts.Yes, _ = c.Flags().GetBool("yes")
			if handled, err := cmdutil.HandleDryRun(c, opts.DryRun, cmdutil.DryRunPlan{
				Action: "message.delete",
				Args:   map[string]any{"message_id": opts.MessageID, "session": opts.SessionID},
			}); handled {
				return err
			}
			cli, err := f.Client()
			if err != nil {
				return err
			}
			return runDelete(c.Context(), opts, fopts, cli, f.Prompter())
		},
	}
	cmd.Flags().StringVar(&opts.SessionID, "session", "", "Parent session id the message lives in")
	_ = cmd.MarkFlagRequired("session")
	cmdutil.AddFormatFlag(cmd, messageDeleteFields...)
	cmdutil.AddDryRunFlag(cmd, &opts.DryRun)
	cmdutil.SetRisk(cmd, "message.delete")
	cmdutil.SetAgentHelp(cmd, cmdutil.AgentHelp{
		UsedFor:       "permanently delete one message from a session",
		RequiredFlags: []string{"<message-id> (positional)", "--session <session-id>"},
		Examples:      []string{"weknora message delete msg_abc --session sess_xyz -y"},
		Output:        "envelope.data is {id, deleted:true}",
		Warnings: []string{
			"Requires explicit user approval (exit 10 / input.confirmation_required); never auto-add -y.",
			"message delete is irreversible; breaks the turn chain for follow-up references to that conversation turn.",
		},
	})
	return cmd
}

func runDelete(ctx context.Context, opts *DeleteOptions, fopts *cmdutil.FormatOptions, svc DeleteService, p prompt.Prompter) error {
	if err := cmdutil.ConfirmDestructive(p, opts.Yes, fopts.WantsJSON(), "delete", "message", opts.MessageID, "message.delete", []string{"weknora", "message", "delete", opts.MessageID, "--session", opts.SessionID, "-y"}); err != nil {
		return err
	}
	if err := svc.DeleteMessage(ctx, opts.SessionID, opts.MessageID); err != nil {
		return cmdutil.WrapHTTP(err, "delete message %s", opts.MessageID)
	}
	if fopts.WantsJSON() {
		return fopts.Emit(iostreams.IO.Out, deleteResult{ID: opts.MessageID, Deleted: true}, nil)
	}
	fmt.Fprintf(iostreams.IO.Out, "✓ Deleted message %s\n", opts.MessageID)
	return nil
}
