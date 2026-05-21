package sessioncmd

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/format"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/output"
	"github.com/Tencent/WeKnora/cli/internal/sse"
	sdk "github.com/Tencent/WeKnora/client"
)

// sessionAskFields enumerates the NDJSON init-event fields surfaced for
// `--format json` / `--format ndjson` discovery on `session ask`. Reflects
// the InitEvent head line + the raw SDK agent event vocabulary (§5).
var sessionAskFields = []string{
	"session_id", "agent_id",
	// SDK event fields (pass-through): response_type, content, done,
	// knowledge_references, tool_call_id, tool_result
}

// AskOptions captures `session ask` flag state.
type AskOptions struct {
	AgentID   string
	Query     string
	SessionID string // --session: continue an existing session (skip auto-create)
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
  TTY (human format, default):   live answer streaming + tool-trace footer
  --format json / --format ndjson / pipe: NDJSON event stream — one init line
                                 at head (session_id, agent_id), then raw SDK
                                 agent events verbatim. Both json and ndjson
                                 flags produce the same NDJSON stream (§5).`,
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
	cmdutil.AddFormatFlag(cmd, sessionAskFields...)
	cmdutil.SetAgentHelp(cmd, cmdutil.AgentHelp{
		UsedFor:       "Invoke a custom agent in a session context. Produces an NDJSON event stream: init line (session_id, agent_id) then raw SDK agent events. Use --format json or --format ndjson.",
		RequiredFlags: []string{"--agent"},
		Examples: []string{
			`weknora session ask --agent ag_x "Summarize Q3 sales" --format json`,
			`weknora session ask --session sess_x --agent ag_x "Follow-up question" --format json`,
		},
		Output: "NDJSON stream: {type:init, session_id, agent_id} then SDK agent events (response_type, content, done, knowledge_references, ...)",
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

	// Streaming commands route --format json AND --format ndjson to the
	// NDJSON event-stream path. A buffered envelope makes no sense for a
	// streaming command (§5). Only --format human (or TTY default) uses the
	// live renderer.
	ndjsonMode := fopts != nil && (fopts.Mode == cmdutil.FormatJSON || fopts.Mode == cmdutil.FormatNDJSON)

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

	if ndjsonMode {
		return runAskNDJSON(ctx, opts, sessionID, svc)
	}

	// Surface auto-created session id up-front so a ^C mid-stream still
	// leaves a recoverable pointer. Skipped in NDJSON mode (it appears in
	// the init event).
	if autoCreated {
		fmt.Fprintf(iostreams.IO.Err, "session: %s (use --session to continue)\n", sessionID)
	}

	return runAskHuman(ctx, opts, sessionID, autoCreated, svc)
}

// runAskNDJSON handles --format json and --format ndjson paths.
// Emits a CLI init event at stream head, then passes every SDK agent event
// through verbatim as NDJSON lines. No buffering (§5).
func runAskNDJSON(ctx context.Context, opts *AskOptions, sessionID string, svc AskService) error {
	w := iostreams.IO.Out

	// 1. Inject the CLI-managed init event at the head of the stream (§5.3).
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

// runAskHuman handles the TTY / human-text path. Streams answer fragments
// live on TTY; accumulates then renders on non-TTY pipes.
func runAskHuman(ctx context.Context, opts *AskOptions, sessionID string, autoCreated bool, svc AskService) error {
	// Stream mode requires an interactive stdout.
	streamMode := iostreams.IO.IsStdoutTTY()

	req := &sdk.AgentQARequest{
		Query:        opts.Query,
		AgentEnabled: true,
		AgentID:      opts.AgentID,
		Channel:      "api",
	}

	acc := &sse.AgentAccumulator{}
	cb := func(r *sdk.AgentStreamResponse) error {
		if streamMode && r != nil && r.ResponseType == sdk.AgentResponseTypeAnswer && r.Content != "" {
			_, _ = iostreams.IO.Out.Write([]byte(r.Content))
		}
		acc.Append(r)
		return nil
	}

	streamErr := svc.AgentQAStreamWithRequest(ctx, sessionID, req, cb)
	if streamErr != nil {
		if autoCreated {
			fmt.Fprintf(iostreams.IO.Err, "session: %s (resume with --session %s)\n", sessionID, sessionID)
		}
		if cmdutil.IsCancelled(ctx, streamErr) {
			return cmdutil.Wrapf(cmdutil.CodeOperationCancelled, streamErr, "session ask cancelled")
		}
		if acc.Answer() != "" && !acc.Done() {
			return cmdutil.Wrapf(cmdutil.CodeSSEStreamAborted, streamErr, "stream aborted before completion")
		}
		return cmdutil.WrapHTTP(streamErr, "agent-chat stream")
	}

	// Server closed cleanly but never sent a Done event — treat as aborted
	// so agents don't silently emit a truncated answer as ok=true.
	if !acc.Done() {
		return cmdutil.NewError(cmdutil.CodeSSEStreamAborted, "stream ended without a terminal event")
	}

	answer := acc.Answer()
	out := iostreams.IO.Out
	if streamMode {
		if !strings.HasSuffix(answer, "\n") {
			fmt.Fprintln(out)
		}
	} else {
		fmt.Fprint(out, answer)
		if !strings.HasSuffix(answer, "\n") {
			fmt.Fprintln(out)
		}
	}
	renderAskToolTrace(out, acc.ToolEvents)
	format.WriteReferences(out, acc.References)
	return nil
}

// renderAskToolTrace prints a compact tool-event footer in human mode.
// Skipped when the agent emitted no tool events — silent beats an empty
// banner.
func renderAskToolTrace(w io.Writer, events []sse.AgentToolEvent) {
	if len(events) == 0 {
		return
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "──── Tool trace ────")
	for i, e := range events {
		fmt.Fprintf(w, "[%d] %s", i+1, e.Kind)
		if e.Result != "" {
			fmt.Fprintf(w, "  %s", truncateAskInline(e.Result, 80))
		}
		fmt.Fprintln(w)
	}
}

// truncateAskInline shrinks a multi-line result to a single line + ellipsis
// for the human tool-trace footer.
func truncateAskInline(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-1] + "…"
}

// compile-time check: production SDK client satisfies AskService.
var _ AskService = (*sdk.Client)(nil)
