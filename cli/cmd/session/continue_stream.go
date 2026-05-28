// continue_stream.go implements `weknora session continue-stream` —
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

// continueStreamFields enumerates the NDJSON init-event + raw SDK event
// vocabulary surfaced for `--format json` / `--format ndjson` discovery.
var continueStreamFields = []string{
	"session_id", "message_id",
	// SDK StreamResponse fields (pass-through): id, response_type, content,
	// done, knowledge_references, assistant_message_id, session_id,
	// tool_calls, data
}

// ContinueStreamOptions captures `session continue-stream` flag/arg state.
type ContinueStreamOptions struct {
	SessionID string
	MessageID string
}

// ContinueStreamService is the narrow SDK surface this command depends on.
// *sdk.Client satisfies it; tests substitute a fake. Compile-time check
// at the bottom of this file.
type ContinueStreamService interface {
	ContinueStream(ctx context.Context, sessionID, messageID string, cb func(*sdk.StreamResponse) error) error
}

// NewCmdContinueStream builds `weknora session continue-stream <session-id> --message <id>`.
func NewCmdContinueStream(f *cmdutil.Factory) *cobra.Command {
	opts := &ContinueStreamOptions{}
	cmd := &cobra.Command{
		Use:   "continue-stream <session-id>",
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
		Example: `  weknora session continue-stream sess_xyz --message msg_abc
  weknora session continue-stream sess_xyz -m msg_abc --format ndjson`,
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
			return runContinueStream(c.Context(), opts, fopts, cli)
		},
	}
	cmd.Flags().StringVarP(&opts.MessageID, "message", "m", "",
		"Assistant message ID to resume (from the init or agent_query event of the original stream)")
	_ = cmd.MarkFlagRequired("message")
	cmdutil.AddFormatFlag(cmd, continueStreamFields...)
	cmdutil.SetAgentHelp(cmd, cmdutil.AgentHelp{
		UsedFor:       "Resume an SSE event stream for an in-progress or completed assistant message. Produces an NDJSON event stream: init line (session_id, message_id) then raw SDK StreamResponse events.",
		RequiredFlags: []string{"--message (message_id from prior init / agent_query event)"},
		Examples: []string{
			"weknora session continue-stream sess_xyz --message msg_abc --format json",
			"# Network-blip recovery: replay with same session_id + message_id from the original 'session ask' init event",
		},
		Output: "NDJSON stream: {type:init, session_id, message_id, profile} then SDK StreamResponse events (response_type, content, done, knowledge_references, assistant_message_id, ...)",
		Warnings: []string{
			"Server replays from event 0 (NOT cursor-from-disconnect). Agents that already consumed events on the original stream MUST dedupe by message_id + event hash to avoid double-processing.",
			"Buffer TTL: redis mode 1h hardcoded; memory mode = process lifetime. After expiry the CLI returns local.sse_stream_aborted.",
		},
	})
	return cmd
}

// runContinueStream is the testable core: validate, dispatch the resume, and
// route the NDJSON stream. Returns a typed error.
//
// Always emits NDJSON: a buffered envelope makes no sense for a streaming
// command, and continue-stream has no human-text use case (operators reach
// for it during incident response / debugging, which always wants the raw
// event log). --format text is therefore treated identically to --format
// json/ndjson here.
func runContinueStream(ctx context.Context, opts *ContinueStreamOptions, _ *cmdutil.FormatOptions, svc ContinueStreamService) error {
	if opts.SessionID == "" {
		return cmdutil.NewError(cmdutil.CodeInputInvalidArgument, "session-id argument cannot be empty")
	}
	if opts.MessageID == "" {
		return cmdutil.NewError(cmdutil.CodeInputInvalidArgument, "--message cannot be empty")
	}
	if svc == nil {
		return cmdutil.NewError(cmdutil.CodeServerError, "session continue-stream: no SDK client available")
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
	cb := func(r *sdk.StreamResponse) error {
		return output.EmitSDKEvent(w, r)
	}
	if err := svc.ContinueStream(ctx, opts.SessionID, opts.MessageID, cb); err != nil {
		// Ctrl-C / SIGTERM lineage (operator gave up on the resume).
		if cmdutil.IsCancelled(ctx, err) {
			return cmdutil.Wrapf(cmdutil.CodeOperationCancelled, err, "session continue-stream cancelled")
		}
		// Pre-stream HTTP / transport failure (e.g. 404 if message_id is
		// unknown, or buffer-expired body from the server). Route through
		// the canonical classifier so codes survive — 404 still surfaces
		// as resource.not_found etc.
		return cmdutil.WrapHTTP(err, "continue stream")
	}
	return nil
}

// compile-time check: production SDK client satisfies ContinueStreamService.
var _ ContinueStreamService = (*sdk.Client)(nil)
