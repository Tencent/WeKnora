// stop.go implements `weknora session stop` — cancel server-side generation
// for a specific assistant message under a known session.
//
// Unlike Ctrl-C (which only drops the local connection while the server keeps
// generating and billing tokens), this tells the server to stop.
//
// This is the symmetric counterpart to `session continue-stream`: both key on
// (session_id, message_id). The message_id comes from the init event of the
// original chat / session ask / continue-stream stream.
package sessioncmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

var stopFields = []string{"session_id", "message_id", "stopped"}

// StopOptions captures `session stop` flag/arg state.
type StopOptions struct {
	SessionID string
	MessageID string
	DryRun    bool
}

// StopService is the narrow SDK surface this command depends on.
// *sdk.Client satisfies it; tests substitute a fake. Compile-time check
// at the bottom of this file.
type StopService interface {
	StopSession(ctx context.Context, sessionID, messageID string) error
}

type stopResult struct {
	SessionID string `json:"session_id"`
	MessageID string `json:"message_id"`
	Stopped   bool   `json:"stopped"`
}

// NewCmdStop builds `weknora session stop <session-id> --message <id>`.
func NewCmdStop(f *cmdutil.Factory) *cobra.Command {
	opts := &StopOptions{}
	cmd := &cobra.Command{
		Use:   "stop <session-id>",
		Short: "Stop in-flight generation for an assistant message in a session",
		Long: `Cancel server-side generation for a specific assistant message under a
session. Unlike Ctrl-C (which only drops the local connection while the server
keeps generating and billing tokens), this tells the server to stop.

Symmetric with 'session continue-stream': both key on (session_id, message_id).`,
		Example: `  weknora session stop sess_xyz --message msg_abc`,
		Args:    cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			opts.SessionID = args[0]
			fopts, err := cmdutil.CheckFormatFlag(c)
			if err != nil {
				return err
			}
			fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())
			if handled, err := cmdutil.HandleDryRun(c, opts.DryRun, cmdutil.DryRunPlan{
				Action: "session.stop",
				Args:   map[string]any{"session": opts.SessionID, "message": opts.MessageID},
			}); handled {
				return err
			}
			cli, err := f.Client()
			if err != nil {
				return err
			}
			return runStop(c.Context(), opts, fopts, cli)
		},
	}
	cmd.Flags().StringVarP(&opts.MessageID, "message", "m", "",
		"Assistant message id to stop (from the init event of the stream you're stopping)")
	_ = cmd.MarkFlagRequired("message")
	cmdutil.AddFormatFlag(cmd, stopFields...)
	cmdutil.AddDryRunFlag(cmd, &opts.DryRun)
	cmdutil.SetAgentHelp(cmd, cmdutil.AgentHelp{
		UsedFor:       "Stop server-side generation for an in-flight assistant message (counterpart to continue-stream). The message_id comes from the init event of the chat / session ask / continue-stream stream you're stopping.",
		RequiredFlags: []string{"--message (message_id from the init event of the stream you're stopping)"},
		Examples:      []string{"weknora session stop sess_xyz --message msg_abc"},
		Output:        "envelope {session_id, message_id, stopped:true}",
	})
	return cmd
}

// runStop is the testable core: validate, dispatch the stop, and emit the
// result envelope. Returns a typed error.
func runStop(ctx context.Context, opts *StopOptions, fopts *cmdutil.FormatOptions, svc StopService) error {
	if opts.SessionID == "" {
		return cmdutil.NewError(cmdutil.CodeInputInvalidArgument, "session-id argument cannot be empty")
	}
	if opts.MessageID == "" {
		return cmdutil.NewError(cmdutil.CodeInputInvalidArgument, "--message cannot be empty")
	}
	if svc == nil {
		return cmdutil.NewError(cmdutil.CodeServerError, "session stop: no SDK client available")
	}
	if err := svc.StopSession(ctx, opts.SessionID, opts.MessageID); err != nil {
		if cmdutil.IsCancelled(ctx, err) {
			return cmdutil.Wrapf(cmdutil.CodeOperationCancelled, err, "session stop cancelled")
		}
		return cmdutil.WrapHTTP(err, "stop session")
	}
	if fopts.WantsJSON() {
		return fopts.Emit(iostreams.IO.Out, stopResult{SessionID: opts.SessionID, MessageID: opts.MessageID, Stopped: true}, nil)
	}
	fmt.Fprintf(iostreams.IO.Out, "✓ Stopped generation for message %s\n", opts.MessageID)
	return nil
}

// compile-time check: production SDK client satisfies StopService.
var _ StopService = (*sdk.Client)(nil)
