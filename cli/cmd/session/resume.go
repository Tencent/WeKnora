// resume.go implements `weknora session resume` —
// re-attach to an SSE event buffer for an in-progress or already-completed
// assistant message under a known session_id.
//
// Server semantics:
//   - Replay-from-0 + tail: every connection replays the entire stored event
//     log from index 0, then tails any new events. NOT a cursor-from-disconnect
//     model. Agents that already consumed events on the original stream MUST
//     dedupe (by message_id + event hash, or by AssistantMessageID + content
//     fingerprint) to avoid double-processing.
//   - Buffer TTL:
//     redis mode  - 1h hardcoded (not configurable from the CLI)
//     memory mode - process lifetime (server restart = data loss)
//     After TTL the server returns an error which the CLI maps to
//     local.sse_stream_aborted.
//
// Output shape matches `weknora chat` and `weknora session ask` NDJSON mode:
// one CLI-injected init line carrying {session_id, message_id, profile} at
// stream head, then SDK StreamResponse events verbatim. The init line lets
// agents thread the resume to the original message in their dedupe table
// without parsing the first SDK event.
package sessioncmd

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/output"
	sdk "github.com/Tencent/WeKnora/client"
)

// resumeFields enumerates the NDJSON init-event + raw SDK event
// vocabulary surfaced for `--format json` / `--format ndjson` discovery.
var resumeFields = []string{
	"session_id", "message_id",
	// SDK StreamResponse fields (pass-through): id, response_type, content,
	// done, knowledge_references, assistant_message_id, session_id,
	// tool_calls, data
}

// ResumeOptions captures `session resume` flag/arg state.
type ResumeOptions struct {
	SessionID string
	MessageID string
}

// ResumeService is the narrow SDK surface this command depends on.
// *sdk.Client satisfies it; tests substitute a fake. Compile-time check
// at the bottom of this file.
type ResumeService interface {
	ContinueStream(ctx context.Context, sessionID, messageID string, cb func(*sdk.StreamResponse) error) error
}

// NewCmdResume builds `weknora session resume <session-id> --message <id>`.
func NewCmdResume(f *cmdutil.Factory) *cobra.Command {
	opts := &ResumeOptions{}
	cmd := &cobra.Command{
		Use:   "resume <session-id>",
		Short: "Resume an SSE event stream for an in-progress or completed session message",
		Long: `Re-attach to the SSE event buffer for an assistant message under a known session.

The server replays the entire stored event log for the given (session_id, message_id)
from index 0, then tails any new events. This is NOT a cursor-from-disconnect
model: agents that already consumed events on the original stream MUST dedupe
(by message_id or event content hash) to avoid double-processing.

Buffer TTL:
  - redis mode:  1h hardcoded (not configurable from the CLI)
  - memory mode: process lifetime (server restart = data loss)

After TTL expiry the CLI surfaces local.sse_stream_aborted.

Typical use cases:
  - Network blip mid-stream: re-attach with the same session_id + message_id
    from the original 'session ask' / 'chat' init event.
  - Long-running agent invocation: poll progress without blocking the original
    stream.
  - Post-mortem inspection: replay the full event history of a completed
    message for debugging.

Output is NDJSON (matches 'chat' / 'session ask' --format json|ndjson):
one init line at head ({session_id, message_id, profile}), then raw SDK
StreamResponse events verbatim.

Note: unlike 'chat' / 'session ask', this command always emits NDJSON
regardless of --format value. The operator use case (incident response,
debugging) always wants the raw event log; there is no human-text rendering.
--format json and --format ndjson behave identically here; --format text is
silently treated as NDJSON.`,
		Example: `  weknora session resume sess_xyz --message msg_abc
  weknora session resume sess_xyz -m msg_abc --format ndjson`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			opts.SessionID = args[0]
			fopts, err := cmdutil.CheckFormatFlag(c)
			if err != nil {
				return err
			}
			fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())
			cli, err := f.Client()
			if err != nil {
				return err
			}
			return runResume(c.Context(), opts, fopts, cli)
		},
	}
	cmd.Flags().StringVarP(&opts.MessageID, "message", "m", "",
		"Assistant message ID to resume (from the init or agent_query event of the original stream)")
	_ = cmd.MarkFlagRequired("message")
	cmdutil.AddFormatFlag(cmd, resumeFields...)
	cmdutil.SetAgentHelp(cmd, cmdutil.AgentHelp{
		UsedFor:       "Resume an SSE event stream for an in-progress or completed assistant message. Produces an NDJSON event stream: init line (session_id, message_id) then raw SDK StreamResponse events.",
		RequiredFlags: []string{"<session-id> (positional)", "--message (persisted assistant message id — get it from `weknora message list --session <id>`; a live stream's assistant_message_id is not resumable once the message persists)"},
		Examples: []string{
			"weknora session resume sess_xyz --message msg_abc --format json",
			"# Get the message id from: weknora message list --session <session-id> (the persisted assistant message)",
		},
		Output: "NDJSON stream: {type:init, session_id, message_id, profile} then SDK StreamResponse events (response_type, content, done, knowledge_references, assistant_message_id, ...)",
		Warnings: []string{
			"Server replays from event 0 (NOT cursor-from-disconnect). Agents that already consumed events on the original stream MUST dedupe by message_id + event hash to avoid double-processing.",
			"Buffer TTL: redis mode 1h hardcoded; memory mode = process lifetime. After expiry the CLI returns local.sse_stream_aborted.",
			"Output is always NDJSON (an event stream, not an envelope): --jq does not apply and --format text/json/ndjson behave identically here — parse the event lines yourself.",
		},
	})
	return cmd
}

// runResume is the testable core: validate, dispatch the resume, and
// route the NDJSON stream. Returns a typed error.
//
// Always emits NDJSON: a buffered envelope makes no sense for a streaming
// command, and resume has no human-text use case (operators reach
// for it during incident response / debugging, which always wants the raw
// event log). --format text is therefore treated identically to --format
// json/ndjson here.
func runResume(ctx context.Context, opts *ResumeOptions, _ *cmdutil.FormatOptions, svc ResumeService) error {
	if opts.SessionID == "" {
		return cmdutil.NewError(cmdutil.CodeInputInvalidArgument, "session-id argument cannot be empty")
	}
	if opts.MessageID == "" {
		return cmdutil.NewError(cmdutil.CodeInputInvalidArgument, "--message cannot be empty")
	}
	if svc == nil {
		return cmdutil.NewError(cmdutil.CodeServerError, "session resume: no SDK client available")
	}

	w := iostreams.IO.Out

	// 1. Inject the CLI-managed init event at stream head. Both session_id
	//    and message_id are populated so agents can key their dedupe table
	//    on the resumed message before the first SDK frame arrives.
	initEv := output.InitEvent{
		SessionID: opts.SessionID,
		MessageID: opts.MessageID,
		Profile:   cmdutil.GetProfile(),
	}
	if err := output.EmitInit(w, initEv); err != nil {
		return err
	}

	// 2. Open the SDK replay stream and pass each event through as a bare
	//    NDJSON line. The SDK's StreamResponse is the source of truth for
	//    the event vocabulary; the CLI does not reshape it.
	// The SDK invokes the callback for each event (including a terminal
	// response_type=error frame) BEFORE returning, so raw passthrough still
	// emits every event; on a terminal error frame the SDK then returns an
	// *SSEStreamError. No CLI-level early-terminate is needed.
	cb := func(r *sdk.StreamResponse) error {
		return output.EmitSDKEvent(w, r)
	}
	err := svc.ContinueStream(ctx, opts.SessionID, opts.MessageID, cb)
	if err != nil {
		// Ctrl-C / SIGTERM lineage (operator gave up on the resume).
		if cmdutil.IsCancelled(ctx, err) {
			return cmdutil.Wrapf(cmdutil.CodeOperationCancelled, err, "session resume cancelled")
		}
		// WrapStream routes through ClassifySDKError: a terminal SSE error
		// frame classifies as server.error (matching chat / session ask); a
		// pre-stream HTTP failure (e.g. 404 for an unknown message_id) still
		// surfaces via ClassifyHTTPError as resource.not_found etc.
		return cmdutil.WrapStream(err, "resume stream")
	}
	return nil
}

// compile-time check: production SDK client satisfies ResumeService.
var _ ResumeService = (*sdk.Client)(nil)
