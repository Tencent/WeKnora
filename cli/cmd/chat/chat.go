// Package chat implements `weknora chat <text>` - the streaming RAG answer
// entry point.
//
// Three output modes share a single SDK call:
//
//   - JSON mode (--format json, the default): buffer a bounded projection of
//     the stream into one success envelope. The default projection contains
//     answer events only; --reference adds indexed citations and --verbose
//     adds reasoning, tools, and lifecycle frames.
//
//   - Text mode (--format text): render the same projection as JSON
//     directly to iostreams.IO.Out as it arrives.
//
//   - NDJSON mode (--format ndjson): inject a CLI "init" event at stream
//     head, then pass through every SDK event verbatim as NDJSON lines —
//     the raw protocol trace, for debugging / advanced consumers.
//
// The SDK's KnowledgeQAStream callback contract is invoked sequentially on
// one goroutine, so no mode needs locking. The runChat core takes a
// ChatService interface so tests inject a fake without standing up a real
// SSE server.
package chat

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/output"
	"github.com/Tencent/WeKnora/cli/internal/sse"
	sdk "github.com/Tencent/WeKnora/client"
)

// chatFields enumerates the fields surfaced for `--jq` projection discovery
// on `chat`: the json object's data fields + the raw SDK event vocabulary
// used by --format ndjson.
var chatFields = []string{
	"events", "session_id", "assistant_message_id", "kb_id", "query",
	// NDJSON init + SDK event fields.
	"type", "kb_id", "profile", "response_type", "content", "done", "knowledge_references", "data",
}

type Options struct {
	Query     string
	KBID      string
	SessionID string
	// Reference adds bounded kb_id/chunk_id reference events to JSON/text.
	Reference bool
	// Verbose surfaces the projected execution trace in JSON/text,
	// including reasoning, tools, and lifecycle events.
	// NDJSON is always raw and is unaffected by presentation flags.
	Verbose bool
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
  --format json (default):       one JSON envelope with answer events
  --format text:                 live human-readable answer stream
  --format ndjson:               raw NDJSON event stream — init line (session_id,
                                 kb_id) then SDK events verbatim. Debug / advanced.

Pass --reference to include bounded kb_id/chunk_id reference indexes. Pass
--verbose to include reasoning, tool activity, and lifecycle frames. Combine
both flags for the complete projected stream.`,
		Example: `  weknora chat "What is RRF?" --kb a32a63ff-fb36-4874-bcaa-30f48570a694
  weknora chat "Summarise this design doc" --kb my-kb --format json
  weknora chat "Continue?" --session sess_abc`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			opts.Query = strings.TrimSpace(args[0])
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
	cmdutil.AddKBFlag(cmd)
	cmd.Flags().StringVar(&opts.SessionID, "session", "", "Continue an existing chat session (skip auto-create)")
	cmd.Flags().BoolVar(&opts.Reference, "reference", false, "Include indexed references in JSON/text output")
	cmd.Flags().BoolVar(&opts.Verbose, "verbose", false, "Include reasoning, tools, and lifecycle events in JSON/text output")
	cmdutil.AddFormatFlag(cmd, chatFields...)
	cmdutil.SetAgentHelp(cmd, cmdutil.AgentHelp{
		UsedFor:       "Ask a RAG question against a knowledge base. Default JSON returns a bounded answer-event projection. --reference adds indexed citations; --verbose adds reasoning, tools, and lifecycle events. --format ndjson streams raw SDK events; --format text renders the selected events live.",
		RequiredFlags: []string{"--kb"},
		Examples: []string{
			`weknora chat "What is RRF?" --kb kb_abc`,
			`weknora chat "What is RRF?" --kb kb_abc --jq '[.data.events[].content] | join("")'`,
		},
		Output: "Default --format json: {ok,data:{events:[answer...],session_id,kb_id,query}}. --reference adds kb_id/chunk_id reference events; --verbose adds execution events. --format ndjson remains raw.",
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

	// --format selects the output shape: json (default) accumulates and
	// emits one object, ndjson streams raw SDK events, text renders live.
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

	if fopts != nil && fopts.Mode == cmdutil.FormatNDJSON {
		return runChatNDJSON(ctx, opts, sessionID, svc)
	}
	if fopts != nil && fopts.Mode == cmdutil.FormatJSON {
		return runChatJSON(ctx, opts, fopts, sessionID, svc)
	}

	// Surface the auto-created session ID up-front so a user who hits ^C
	// mid-stream still has the pointer to resume - no need to scroll back
	// past tokens. Skipped in json/ndjson mode (session_id is in the output).
	if autoCreated {
		fmt.Fprintf(iostreams.IO.Err, "session: %s (use --session to continue)\n", sessionID)
	}

	return runChatText(ctx, opts, sessionID, autoCreated, svc)
}

// runChatNDJSON handles the --format ndjson path: emits a CLI init event at
// stream head, then passes every SDK event through verbatim as NDJSON lines.
// No buffering — callers parse the stream incrementally.
func runChatNDJSON(ctx context.Context, opts *Options, sessionID string, svc ChatService) error {
	w := iostreams.IO.Out

	// 1. Inject the CLI-managed init event at the head of the stream.
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
		// NDJSON is the raw protocol/debug surface: do not filter events or
		// mutate their payloads. JSON/text modes own presentation filtering.
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

// runChatText handles the --format text path. It renders the same
// projection as JSON immediately for both terminals and pipes.
func runChatText(ctx context.Context, opts *Options, sessionID string, autoCreated bool, svc ChatService) error {
	req := &sdk.KnowledgeQARequest{
		Query:            opts.Query,
		KnowledgeBaseIDs: []string{opts.KBID},
		AgentEnabled:     false,
		WebSearchEnabled: false,
		Channel:          "api",
	}

	projector := sse.NewProjector(opts.Verbose, opts.Reference, opts.KBID)
	renderer := sse.NewTextRenderer(iostreams.IO.Out, opts.Verbose)

	cb := func(r *sdk.StreamResponse) error {
		event, include := projector.Chat(r)
		if !include {
			return nil
		}
		return renderer.Write(event)
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
		if projector.Seen() && !projector.Done() {
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
	if !projector.Done() {
		return cmdutil.NewError(cmdutil.CodeSSEStreamAborted, "stream ended without a terminal event")
	}
	return renderer.Close()
}

// runChatJSON handles the --format json path (the default): collect the
// projection and emit it in one normal success envelope.
func runChatJSON(ctx context.Context, opts *Options, fopts *cmdutil.FormatOptions, sessionID string, svc ChatService) error {
	req := &sdk.KnowledgeQARequest{
		Query:            opts.Query,
		KnowledgeBaseIDs: []string{opts.KBID},
		AgentEnabled:     false,
		WebSearchEnabled: false,
		Channel:          "api",
	}

	projector := sse.NewProjector(opts.Verbose, opts.Reference, opts.KBID)
	events := make([]sse.ProjectedEvent, 0)
	cb := func(r *sdk.StreamResponse) error {
		if event, include := projector.Chat(r); include {
			events = append(events, event)
		}
		return nil
	}

	if err := svc.KnowledgeQAStream(ctx, sessionID, req, cb); err != nil {
		if cmdutil.IsCancelled(ctx, err) {
			return chatStreamError(cmdutil.Wrapf(cmdutil.CodeOperationCancelled, err, "chat cancelled"), sessionID, projector.AssistantMessageID())
		}
		return chatStreamError(cmdutil.WrapHTTP(err, "knowledge qa stream"), sessionID, projector.AssistantMessageID())
	}
	if !projector.Done() {
		return chatStreamError(
			cmdutil.NewError(cmdutil.CodeSSEStreamAborted, "stream ended without a terminal event"),
			sessionID,
			projector.AssistantMessageID(),
		)
	}

	sid := projector.SessionID()
	if sid == "" {
		sid = sessionID
	}
	data := chatResult{
		Events:             events,
		SessionID:          sid,
		AssistantMessageID: projector.AssistantMessageID(),
		KBID:               opts.KBID,
		Query:              opts.Query,
	}

	// Reaching here means fopts.Mode is FormatJSON (the only caller). Route
	// through FormatOptions.Emit so --jq projection and the success-envelope
	// contract apply, matching every other non-streaming command. A nil fopts
	// (direct-test entry) defaults to JSON so the object still emits.
	if fopts == nil {
		fopts = &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}
	}
	return fopts.Emit(iostreams.IO.Out, data, nil)
}

func chatStreamError(err *cmdutil.Error, sessionID, assistantMessageID string) *cmdutil.Error {
	detail := map[string]any{"session_id": sessionID}
	if assistantMessageID != "" {
		detail["assistant_message_id"] = assistantMessageID
	}
	return err.WithDetail(detail)
}

// chatResult is the --format json data payload. The projector controls default
// versus verbose coverage.
type chatResult struct {
	Events             []sse.ProjectedEvent `json:"events"`
	SessionID          string               `json:"session_id"`
	AssistantMessageID string               `json:"assistant_message_id,omitempty"`
	KBID               string               `json:"kb_id"`
	Query              string               `json:"query"`
}

// compile-time check: the production SDK client implements ChatService.
var _ ChatService = (*sdk.Client)(nil)
