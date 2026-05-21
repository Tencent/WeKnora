// Package chat implements `weknora chat <text>` - the streaming RAG answer
// entry point.
//
// Two output modes share a single SDK call:
//
//   - Stream mode (TTY + --format text): write each StreamResponse.Content
//     fragment directly to iostreams.IO.Out as it arrives, then print a
//     footer with knowledge references. This is the "feels alive" UX a
//     human typing in a terminal expects.
//
//   - NDJSON mode (--format json / --format ndjson / pipe): inject a CLI
//     "init" event at stream head, then pass through every SDK event verbatim
//     as NDJSON lines. Agents and pipes get a live event stream they can
//     parse incrementally. --format json routes here too — buffered JSON
//     envelope makes no sense for a streaming command (§5).
//
// The SDK's KnowledgeQAStream callback contract is invoked sequentially on
// one goroutine, so neither mode needs locking. The runChat core takes a
// ChatService interface so tests inject a fake without standing up a real
// SSE server.
package chat

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/format"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/output"
	"github.com/Tencent/WeKnora/cli/internal/sse"
	sdk "github.com/Tencent/WeKnora/client"
)

// chatFields enumerates the NDJSON init-event fields surfaced for
// `--format json` / `--format ndjson` discovery on `chat`. Reflects the
// InitEvent head line + the raw SDK event vocabulary (§5).
var chatFields = []string{
	"session_id", "kb_id",
	// SDK event fields (pass-through): response_type, content, done,
	// knowledge_references, assistant_message_id, session_id
}

type Options struct {
	Query     string
	KBID      string
	SessionID string
}

// ChatService is the narrow SDK surface this command depends on. *sdk.Client
// satisfies it; tests substitute a fake. Compile-time check is at the bottom
// of this file.
type ChatService interface {
	CreateSession(ctx context.Context, req *sdk.CreateSessionRequest) (*sdk.Session, error)
	KnowledgeQAStream(ctx context.Context, sessionID string, req *sdk.KnowledgeQARequest, cb func(*sdk.StreamResponse) error) error
}

// NewCmd builds `weknora chat <text>`.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{}
	cmd := &cobra.Command{
		Use:   `chat "<text>"`,
		Short: "Ask a streaming RAG question against a knowledge base",
		Long: `Send a query to the WeKnora knowledge-chat endpoint and stream the
answer back. By default a fresh session is created on first invocation; pass
--session to continue an existing conversation.

Modes:
  --format text:                 live token streaming + reference footer
  --format json / --format ndjson / pipe (default): NDJSON event stream —
                                 one init line at head (session_id, kb_id),
                                 then raw SDK events verbatim. Both json
                                 and ndjson flags produce the same NDJSON
                                 stream (§5).`,
		Example: `  weknora chat "What is RRF?" --kb a32a63ff-fb36-4874-bcaa-30f48570a694
  weknora chat "Summarise this design doc" --kb my-kb --format json
  weknora chat "Continue?" --session sess_abc`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			opts.Query = strings.TrimSpace(strings.Join(args, " "))
			if opts.Query == "" {
				return cmdutil.NewError(cmdutil.CodeInputInvalidArgument, "query argument cannot be empty")
			}
			fopts, err := cmdutil.CheckFormatFlag(c)
			if err != nil {
				return err
			}
			fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())
			kbID, err := f.ResolveKB(c)
			if err != nil {
				return err
			}
			opts.KBID = kbID
			cli, err := f.Client()
			if err != nil {
				return err
			}
			return runChat(c.Context(), opts, fopts, cli)
		},
	}
	cmd.Flags().String("kb", "", "Knowledge base UUID or name (overrides project link / env)")
	cmd.Flags().StringVar(&opts.SessionID, "session", "", "Continue an existing chat session (skip auto-create)")
	cmdutil.AddFormatFlag(cmd, chatFields...)
	cmdutil.SetAgentHelp(cmd, cmdutil.AgentHelp{
		UsedFor:       "Ask a streaming RAG question against a knowledge base. Produces an NDJSON event stream: init line (session_id, kb_id) then raw SDK events. Use --format json or --format ndjson.",
		RequiredFlags: []string{"--kb"},
		Examples:      []string{`weknora chat "What is RRF?" --kb kb_abc --format json`},
		Output:        "NDJSON stream: {type:init, session_id, kb_id} then SDK events (response_type, content, done, knowledge_references, ...)",
	})
	return cmd
}

// runChat is the testable core: validate, ensure a session, dispatch the
// stream, and route output. Returns a typed error.
func runChat(ctx context.Context, opts *Options, fopts *cmdutil.FormatOptions, svc ChatService) error {
	if opts.Query == "" {
		return cmdutil.NewError(cmdutil.CodeInputInvalidArgument, "query argument cannot be empty")
	}
	if opts.KBID == "" {
		// Defensive: the cobra layer resolves KB before runChat; this guards
		// the direct-test entry point.
		return cmdutil.NewError(cmdutil.CodeKBIDRequired, "kb id is required")
	}
	if svc == nil {
		return cmdutil.NewError(cmdutil.CodeServerError, "chat: no SDK client available")
	}

	// Streaming commands route --format json AND --format ndjson to the
	// NDJSON event-stream path. A buffered envelope makes no sense for a
	// streaming command (§5). Only --format text uses the live renderer.
	ndjsonMode := fopts != nil && (fopts.Mode == cmdutil.FormatJSON || fopts.Mode == cmdutil.FormatNDJSON)

	sessionID := opts.SessionID
	autoCreated := false
	if sessionID == "" {
		sess, err := svc.CreateSession(ctx, &sdk.CreateSessionRequest{Title: "weknora chat"})
		if err != nil {
			// Ctrl-C during session creation: classify as cancelled so the
			// hint nudges the user toward retry-with-signal-clean, not
			// "pass --session" as session_create_failed would.
			if cmdutil.IsCancelled(ctx, err) {
				return cmdutil.Wrapf(cmdutil.CodeOperationCancelled, err, "chat cancelled")
			}
			// Map HTTP-shaped failures, but tag generic transport / unknown
			// errors as session_create_failed so the dedicated hint fires.
			code := cmdutil.ClassifyHTTPError(err)
			if code == cmdutil.CodeNetworkError || code == cmdutil.CodeServerError {
				code = cmdutil.CodeSessionCreateFailed
			}
			return cmdutil.Wrapf(code, err, "create chat session")
		}
		sessionID = sess.ID
		autoCreated = true
	}

	if ndjsonMode {
		return runChatNDJSON(ctx, opts, sessionID, svc)
	}

	// Surface the auto-created session ID up-front so a user who hits ^C
	// mid-stream still has the pointer to resume - no need to scroll back
	// past tokens. Skipped in NDJSON mode (it appears in the init event).
	if autoCreated {
		fmt.Fprintf(iostreams.IO.Err, "session: %s (use --session to continue)\n", sessionID)
	}

	return runChatText(ctx, opts, sessionID, autoCreated, svc)
}

// runChatNDJSON handles --format json and --format ndjson paths.
// Emits a CLI init event at stream head, then passes every SDK event through
// verbatim as NDJSON lines. No buffering — callers parse the stream
// incrementally (§5).
func runChatNDJSON(ctx context.Context, opts *Options, sessionID string, svc ChatService) error {
	w := iostreams.IO.Out

	// 1. Inject the CLI-managed init event at the head of the stream (§5.3).
	//    Carries the session pointer + retrieval context callers need for
	//    follow-up threading.
	initEv := output.InitEvent{
		SessionID: sessionID,
		KBID:      opts.KBID,
		Profile:   cmdutil.GetProfile(),
	}
	if err := output.EmitInit(w, initEv); err != nil {
		return err
	}

	// 2. Open SDK stream and pass each event through as a bare NDJSON line.
	req := &sdk.KnowledgeQARequest{
		Query:            opts.Query,
		KnowledgeBaseIDs: []string{opts.KBID},
		AgentEnabled:     false,
		WebSearchEnabled: false,
		Channel:          "api",
	}
	cb := func(r *sdk.StreamResponse) error {
		return output.EmitSDKEvent(w, r)
	}
	if err := svc.KnowledgeQAStream(ctx, sessionID, req, cb); err != nil {
		if cmdutil.IsCancelled(ctx, err) {
			return cmdutil.Wrapf(cmdutil.CodeOperationCancelled, err, "chat cancelled")
		}
		return cmdutil.WrapHTTP(err, "knowledge qa stream")
	}
	return nil
}

// runChatText handles the --format text path. Streams content fragments
// live on TTY; accumulates then renders on non-TTY pipes.
func runChatText(ctx context.Context, opts *Options, sessionID string, autoCreated bool, svc ChatService) error {
	// Stream mode requires an interactive stdout.
	streamMode := iostreams.IO.IsStdoutTTY()

	req := &sdk.KnowledgeQARequest{
		Query:            opts.Query,
		KnowledgeBaseIDs: []string{opts.KBID},
		AgentEnabled:     false,
		WebSearchEnabled: false,
		Channel:          "api",
	}

	acc := &sse.Accumulator{}

	cb := func(r *sdk.StreamResponse) error {
		if streamMode && r != nil && r.Content != "" {
			// Best-effort write; if stdout dies the SDK will surface the
			// error on the next iteration. No need to bail early.
			_, _ = iostreams.IO.Out.Write([]byte(r.Content))
		}
		acc.Append(r)
		return nil
	}

	streamErr := svc.KnowledgeQAStream(ctx, sessionID, req, cb)
	if streamErr != nil {
		// Re-surface the auto-created session id on failure so a user who
		// missed the start-of-stream notice (it scrolls past mid-stream
		// tokens, especially on ^C) can still recover with --session.
		if autoCreated {
			fmt.Fprintf(iostreams.IO.Err, "session: %s (resume with --session %s)\n", sessionID, sessionID)
		}
		// Context cancelled (Ctrl-C) → user-aborted, exit 130 lineage.
		if cmdutil.IsCancelled(ctx, streamErr) {
			return cmdutil.Wrapf(cmdutil.CodeOperationCancelled, streamErr, "chat cancelled")
		}
		// Stream began (we observed at least one event) but never reached a
		// terminal Done frame: typed as sse_stream_aborted so the hint
		// nudges the user toward a retry.
		if acc.Result() != "" && !acc.Done() {
			return cmdutil.Wrapf(cmdutil.CodeSSEStreamAborted, streamErr, "stream aborted before completion")
		}
		// Pre-stream HTTP / transport failure: route through the canonical
		// classifier so 401 / 404 / 5xx still surface their specific codes.
		return cmdutil.WrapHTTP(streamErr, "knowledge qa stream")
	}

	// SDK returned nil but we never saw a Done event - server closed the
	// connection cleanly mid-stream. Treat as aborted so the user sees the
	// truncation rather than a silent partial answer. Includes the empty-body
	// case (Done frame never arrived AND no content): better to surface the
	// abort than emit ok=true with answer="" - agents can't distinguish the
	// model genuinely had nothing to say from the stream getting cut.
	if !acc.Done() {
		return cmdutil.NewError(cmdutil.CodeSSEStreamAborted, "stream ended without a terminal event")
	}

	answer := acc.Result()
	references := acc.References

	// Streaming mode already wrote the answer body via the callback, so we
	// only need to render the trailing references (and a closing newline).
	// Non-TTY accumulate path writes the answer here for the first time.
	out := iostreams.IO.Out
	if streamMode {
		// Ensure the answer line ends cleanly before the references footer.
		if !strings.HasSuffix(answer, "\n") {
			fmt.Fprintln(out)
		}
	} else {
		fmt.Fprint(out, answer)
		if !strings.HasSuffix(answer, "\n") {
			fmt.Fprintln(out)
		}
	}
	format.WriteReferences(out, references)
	return nil
}

// compile-time check: the production SDK client implements ChatService.
var _ ChatService = (*sdk.Client)(nil)
