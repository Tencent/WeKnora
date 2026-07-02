package sessioncmd

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

// sessionAskFields enumerates the fields surfaced for `--jq` projection
// discovery on `session ask`: the json object's data fields + the raw SDK
// agent event vocabulary used by --format ndjson.
var sessionAskFields = []string{
	"events", "session_id", "agent_id", "query",
	// NDJSON init + SDK event fields.
	"type", "profile", "response_type", "content", "done", "knowledge_references", "data",
}

// AskOptions captures `session ask` flag state.
type AskOptions struct {
	AgentID   string
	Query     string
	SessionID string // --session: continue an existing session (skip auto-create)
	// Reference adds bounded kb_id/chunk_id reference events to JSON/text.
	Reference bool
	// Verbose surfaces the projected execution trace in JSON/text,
	// including reasoning, tools, and lifecycle events.
	// NDJSON is always raw and is unaffected by presentation flags.
	Verbose bool
}

// AskService is the narrow SDK surface this command depends on.
//
// CreateSession is called when --session is omitted — sessions are
// agent-agnostic at creation (verified against
// internal/handler/session/handler.go CreateSession, which only persists
// {title, description}). The agent ID is supplied per-request via
// AgentQARequest.AgentID, so the same session can be reused across
// agent / KB-chat invocations.
type AskService interface {
	CreateSession(ctx context.Context, req *sdk.CreateSessionRequest) (*sdk.Session, error)
	AgentQAStreamWithRequest(ctx context.Context, sessionID string, req *sdk.AgentQARequest, cb sdk.AgentEventCallback) error
}

// NewCmdAsk builds `weknora session ask --agent <agent-id> "<text>"`.
func NewCmdAsk(f *cmdutil.Factory) *cobra.Command {
	opts := &AskOptions{}
	cmd := &cobra.Command{
		Use:   `ask "<text>"`,
		Short: "Ask a server-side agent in a session context",
		Long: `Invoke a server-side agent within a session. If --session is omitted,
a new session is auto-created and its id is reported in the output for
the caller to thread follow-ups.

AI agents: this is the primary entrypoint for invoking custom agents.
The 'weknora agent' subtree handles CRUD only (list / view / create /
edit / delete / status / check).

Modes:
  --format json (default):       one JSON envelope with answer events
  --format text:                 live human-readable answer stream
  --format ndjson:               raw NDJSON event stream — init line (session_id,
                                 agent_id) then SDK agent events verbatim. Debug.

Pass --reference to include bounded kb_id/chunk_id reference indexes. Pass
--verbose to include reasoning, tool activity, and lifecycle frames. Combine
both flags for the complete projected stream.`,
		Example: `  weknora session ask --agent ag_x "Summarize Q3 sales"
  weknora session ask --session sess_x --agent ag_x "Follow-up question"
  weknora session ask --agent ag_x "Multi-step task" --format ndjson`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			fopts, err := cmdutil.CheckFormatFlag(c)
			if err != nil {
				return err
			}
			fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())
			opts.Query = strings.TrimSpace(args[0])
			cli, err := f.Client()
			if err != nil {
				return err
			}
			return runAsk(c.Context(), opts, fopts, cli)
		},
	}
	cmd.Flags().StringVarP(&opts.AgentID, "agent", "a", "", "Agent ID to invoke (required)")
	_ = cmd.MarkFlagRequired("agent")
	cmd.Flags().StringVar(&opts.SessionID, "session", "", "Continue an existing chat session (skip auto-create)")
	cmd.Flags().BoolVar(&opts.Reference, "reference", false, "Include indexed references in JSON/text output")
	cmd.Flags().BoolVar(&opts.Verbose, "verbose", false, "Include reasoning, tools, and lifecycle events in JSON/text output")
	cmdutil.AddFormatFlag(cmd, sessionAskFields...)
	cmdutil.SetAgentHelp(cmd, cmdutil.AgentHelp{
		UsedFor:       "Invoke a custom agent in a session context. Default JSON returns a bounded answer-event projection. --reference adds indexed citations; --verbose adds reasoning, tools, and lifecycle events. --format ndjson streams raw SDK agent events; --format text renders the selected events live.",
		RequiredFlags: []string{"--agent"},
		Examples: []string{
			`weknora session ask --agent ag_x "Summarize Q3 sales"`,
			`weknora session ask --agent ag_x "Summarize Q3 sales" --jq '[.data.events[].content] | join("")'`,
			`weknora session ask --session sess_x --agent ag_x "Follow-up question"`,
		},
		Output: "Default --format json: {ok,data:{events:[answer...],session_id,agent_id,query}}. --reference adds kb_id/chunk_id reference events; --verbose adds execution events. --format ndjson remains raw.",
	})
	return cmd
}

func runAsk(ctx context.Context, opts *AskOptions, fopts *cmdutil.FormatOptions, svc AskService) error {
	if opts.Query == "" {
		return cmdutil.NewError(cmdutil.CodeInputInvalidArgument, "query argument cannot be empty")
	}
	if opts.AgentID == "" {
		return cmdutil.NewError(cmdutil.CodeInputInvalidArgument, "agent-id argument cannot be empty")
	}
	if svc == nil {
		return cmdutil.NewError(cmdutil.CodeServerError, "session ask: no SDK client available")
	}

	// --format selects the output shape: json (default) accumulates and
	// emits one object, ndjson streams raw SDK agent events, text renders.
	sessionID := opts.SessionID
	autoCreated := false
	if sessionID == "" {
		sess, err := svc.CreateSession(ctx, &sdk.CreateSessionRequest{Title: "weknora session ask"})
		if err != nil {
			if cmdutil.IsCancelled(ctx, err) {
				return cmdutil.Wrapf(cmdutil.CodeOperationCancelled, err, "session ask cancelled")
			}
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
		return runAskNDJSON(ctx, opts, sessionID, svc)
	}
	if fopts != nil && fopts.Mode == cmdutil.FormatJSON {
		return runAskJSON(ctx, opts, fopts, sessionID, svc)
	}

	// Surface auto-created session id up-front so a ^C mid-stream still
	// leaves a recoverable pointer. Skipped in json/ndjson mode (session_id
	// is in the output).
	if autoCreated {
		fmt.Fprintf(iostreams.IO.Err, "session: %s (use --session to continue)\n", sessionID)
	}

	return runAskText(ctx, opts, sessionID, autoCreated, svc)
}

// runAskNDJSON handles the --format ndjson path: emits a CLI init event at
// stream head, then passes every SDK agent event through verbatim as NDJSON
// lines. No buffering.
func runAskNDJSON(ctx context.Context, opts *AskOptions, sessionID string, svc AskService) error {
	w := iostreams.IO.Out

	// 1. Inject the CLI-managed init event at the head of the stream.
	//    Carries session pointer + agent id callers need for follow-up threading.
	initEv := output.InitEvent{
		SessionID: sessionID,
		AgentID:   opts.AgentID,
		Profile:   cmdutil.GetProfile(),
	}
	if err := output.EmitInit(w, initEv); err != nil {
		return err
	}

	// 2. Open SDK stream and pass each agent event through as a bare NDJSON line.
	req := &sdk.AgentQARequest{
		Query:        opts.Query,
		AgentEnabled: true,
		AgentID:      opts.AgentID,
		Channel:      "api",
	}
	cb := func(r *sdk.AgentStreamResponse) error {
		// NDJSON is the raw protocol/debug surface: do not filter events or
		// mutate their payloads. JSON/text modes own presentation filtering.
		return output.EmitSDKEvent(w, r)
	}
	if err := svc.AgentQAStreamWithRequest(ctx, sessionID, req, cb); err != nil {
		if cmdutil.IsCancelled(ctx, err) {
			return cmdutil.Wrapf(cmdutil.CodeOperationCancelled, err, "session ask cancelled")
		}
		return cmdutil.WrapHTTP(err, "agent-chat stream")
	}
	return nil
}

// runAskText handles the --format text path. It renders the same
// projection as JSON immediately for both terminals and pipes.
func runAskText(ctx context.Context, opts *AskOptions, sessionID string, autoCreated bool, svc AskService) error {
	req := &sdk.AgentQARequest{
		Query:        opts.Query,
		AgentEnabled: true,
		AgentID:      opts.AgentID,
		Channel:      "api",
	}

	projector := sse.NewProjector(opts.Verbose, opts.Reference, "")
	renderer := sse.NewTextRenderer(iostreams.IO.Out, opts.Verbose)
	cb := func(r *sdk.AgentStreamResponse) error {
		event, include := projector.Agent(r)
		if !include {
			return nil
		}
		return renderer.Write(event)
	}

	streamErr := svc.AgentQAStreamWithRequest(ctx, sessionID, req, cb)
	if streamErr != nil {
		if autoCreated {
			fmt.Fprintf(iostreams.IO.Err, "session: %s (resume with --session %s)\n", sessionID, sessionID)
		}
		if cmdutil.IsCancelled(ctx, streamErr) {
			return cmdutil.Wrapf(cmdutil.CodeOperationCancelled, streamErr, "session ask cancelled")
		}
		if projector.Seen() && !projector.Done() {
			return cmdutil.Wrapf(cmdutil.CodeSSEStreamAborted, streamErr, "stream aborted before completion")
		}
		return cmdutil.WrapHTTP(streamErr, "agent-chat stream")
	}

	// Server closed cleanly but never sent a complete event — treat as aborted
	// so agents don't silently emit a truncated answer as ok=true.
	if !projector.Done() {
		return cmdutil.NewError(cmdutil.CodeSSEStreamAborted, "stream ended without a terminal event")
	}
	return renderer.Close()
}

// runAskJSON handles the --format json path (the default): collect the
// projection and emit it in one normal success envelope.
func runAskJSON(ctx context.Context, opts *AskOptions, fopts *cmdutil.FormatOptions, sessionID string, svc AskService) error {
	req := &sdk.AgentQARequest{
		Query:        opts.Query,
		AgentEnabled: true,
		AgentID:      opts.AgentID,
		Channel:      "api",
	}

	projector := sse.NewProjector(opts.Verbose, opts.Reference, "")
	events := make([]sse.ProjectedEvent, 0)
	cb := func(r *sdk.AgentStreamResponse) error {
		if event, include := projector.Agent(r); include {
			events = append(events, event)
		}
		return nil
	}

	if err := svc.AgentQAStreamWithRequest(ctx, sessionID, req, cb); err != nil {
		if cmdutil.IsCancelled(ctx, err) {
			return askStreamError(cmdutil.Wrapf(cmdutil.CodeOperationCancelled, err, "session ask cancelled"), sessionID)
		}
		return askStreamError(cmdutil.WrapHTTP(err, "agent-chat stream"), sessionID)
	}
	if !projector.Done() {
		return askStreamError(
			cmdutil.NewError(cmdutil.CodeSSEStreamAborted, "stream ended without a terminal event"),
			sessionID,
		)
	}

	data := askResult{
		Events:    events,
		SessionID: sessionID,
		AgentID:   opts.AgentID,
		Query:     opts.Query,
	}

	// Reaching here means fopts.Mode is FormatJSON (the only caller). Route
	// through FormatOptions.Emit so --jq projection and the success-envelope
	// contract apply. A nil fopts (direct-test entry) defaults to JSON.
	if fopts == nil {
		fopts = &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}
	}
	return fopts.Emit(iostreams.IO.Out, data, nil)
}

func askStreamError(err *cmdutil.Error, sessionID string) *cmdutil.Error {
	return err.WithDetail(map[string]any{"session_id": sessionID})
}

// askResult is the --format json data payload. The projector controls default
// versus verbose coverage.
type askResult struct {
	Events    []sse.ProjectedEvent `json:"events"`
	SessionID string               `json:"session_id"`
	AgentID   string               `json:"agent_id"`
	Query     string               `json:"query"`
}

// compile-time check: production SDK client satisfies AskService.
var _ AskService = (*sdk.Client)(nil)
