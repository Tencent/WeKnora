package agentcmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/format"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/sse"
	sdk "github.com/Tencent/WeKnora/client"
)

// agentInvokeFields enumerates fields surfaced for `--format json` discovery
// on `agent invoke`. Matches invokeData below — the single-shot result
// object with the agent's final answer plus the trace (references, tool
// events).
var agentInvokeFields = []string{
	"answer", "references", "tool_events", "thinking",
	"session_id", "agent_id", "query",
}

// InvokeOptions captures `agent invoke` flag state.
type InvokeOptions struct {
	AgentID   string
	Query     string
	SessionID string // --session: continue an existing session (skip auto-create)
}

// InvokeService is the narrow SDK surface this command depends on.
//
// CreateSession is called when --session is omitted — sessions are
// agent-agnostic at creation (verified against
// internal/handler/session/handler.go CreateSession, which only persists
// {title, description}). The agent ID is supplied per-request via
// AgentQARequest.AgentID, so the same session can be reused across
// agent / KB-chat invocations.
type InvokeService interface {
	CreateSession(ctx context.Context, req *sdk.CreateSessionRequest) (*sdk.Session, error)
	AgentQAStreamWithRequest(ctx context.Context, sessionID string, req *sdk.AgentQARequest, cb sdk.AgentEventCallback) error
}

// invokeData is the JSON payload emitted on the JSON path.
type invokeData struct {
	Answer     string               `json:"answer"`
	References []*sdk.SearchResult  `json:"references"`
	ToolEvents []sse.AgentToolEvent `json:"tool_events,omitempty"`
	Thinking   string               `json:"thinking,omitempty"`
	SessionID  string               `json:"session_id"`
	AgentID    string               `json:"agent_id"`
	Query      string               `json:"query"`
}

// NewCmdInvoke builds `weknora agent invoke <agent-id> "<text>"`.
func NewCmdInvoke(f *cmdutil.Factory) *cobra.Command {
	opts := &InvokeOptions{}
	cmd := &cobra.Command{
		Use:   `invoke <agent-id> "<text>"`,
		Short: "Run a query through a custom agent",
		Long: `Sends a query to the agent's configured workflow (system prompt, allowed
tools, KB scope, retrieval thresholds) over SSE. By default a fresh session
is auto-created; pass --session to continue an existing conversation. The
agent picks the model, retrieval params, and tool surface from its own
config — agent invoke is the thin shim that streams the result.

Modes:
  TTY (text format, default):  live answer streaming + tool-trace footer
  --format json / pipe:        buffered, single JSON object at completion`,
		Example: `  weknora agent invoke ag_abc "Summarise the Q3 plan"
  weknora agent invoke ag_abc "Continue?" --session sess_xyz
  weknora agent invoke ag_abc "What did we ship?" --format json`,
		Args: cobra.ExactArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			fopts, err := cmdutil.CheckFormatFlag(c)
			if err != nil {
				return err
			}
			fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())
			opts.AgentID = args[0]
			opts.Query = strings.TrimSpace(args[1])
			cli, err := f.Client()
			if err != nil {
				return err
			}
			return runInvoke(c.Context(), opts, fopts, cli)
		},
	}
	cmd.Flags().StringVar(&opts.SessionID, "session", "", "Continue an existing chat session (skip auto-create)")
	cmdutil.AddFormatFlag(cmd, agentInvokeFields...)
	return cmd
}

func runInvoke(ctx context.Context, opts *InvokeOptions, fopts *cmdutil.FormatOptions, svc InvokeService) error {
	if opts.Query == "" {
		return cmdutil.NewError(cmdutil.CodeInputInvalidArgument, "query argument cannot be empty")
	}
	if opts.AgentID == "" {
		return cmdutil.NewError(cmdutil.CodeInputInvalidArgument, "agent-id argument cannot be empty")
	}
	if svc == nil {
		return cmdutil.NewError(cmdutil.CodeServerError, "agent invoke: no SDK client available")
	}

	jsonOut := fopts != nil && fopts.Mode == cmdutil.FormatJSON

	sessionID := opts.SessionID
	autoCreated := false
	if sessionID == "" {
		sess, err := svc.CreateSession(ctx, &sdk.CreateSessionRequest{Title: "weknora agent invoke"})
		if err != nil {
			if isCancelled(ctx, err) {
				return cmdutil.Wrapf(cmdutil.CodeOperationCancelled, err, "agent invoke cancelled")
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

	// Streaming requires interactive stdout + no --format json + no
	// --format ndjson (handled by early-return below). Matches chat.go's
	// mode-selection contract so users get the same muscle memory across
	// both commands.
	streamMode := iostreams.IO.IsStdoutTTY() && !jsonOut &&
		(fopts == nil || fopts.Mode != cmdutil.FormatNDJSON)

	// Surface auto-created session id up-front so a ^C mid-stream still
	// leaves a recoverable pointer.
	if autoCreated && !jsonOut {
		fmt.Fprintf(iostreams.IO.Err, "session: %s (use --session to continue)\n", sessionID)
	}

	req := &sdk.AgentQARequest{
		Query:        opts.Query,
		AgentEnabled: true,
		AgentID:      opts.AgentID,
		Channel:      "api",
	}

	// --format ndjson: stream raw SDK events as NDJSON. Encoder hoisted out
	// of the callback to avoid per-event allocation.
	if fopts != nil && fopts.Mode == cmdutil.FormatNDJSON {
		enc := json.NewEncoder(iostreams.IO.Out)
		enc.SetEscapeHTML(false)
		cb := func(r *sdk.AgentStreamResponse) error {
			return enc.Encode(r)
		}
		if err := svc.AgentQAStreamWithRequest(ctx, sessionID, req, cb); err != nil {
			if isCancelled(ctx, err) {
				return cmdutil.Wrapf(cmdutil.CodeOperationCancelled, err, "agent invoke cancelled")
			}
			return cmdutil.WrapHTTP(err, "agent-chat stream")
		}
		return nil
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
		if autoCreated && !jsonOut {
			fmt.Fprintf(iostreams.IO.Err, "session: %s (resume with --session %s)\n", sessionID, sessionID)
		}
		if isCancelled(ctx, streamErr) {
			return cmdutil.Wrapf(cmdutil.CodeOperationCancelled, streamErr, "agent invoke cancelled")
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
	if jsonOut {
		data := invokeData{
			Answer:     answer,
			References: acc.References,
			ToolEvents: acc.ToolEvents,
			Thinking:   acc.Thinking(),
			SessionID:  sessionID,
			AgentID:    opts.AgentID,
			Query:      opts.Query,
		}
		return fopts.Emit(iostreams.IO.Out, data)
	}

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
	renderToolTrace(out, acc.ToolEvents)
	format.WriteReferences(out, acc.References)
	return nil
}

// renderToolTrace prints a compact tool-event footer in human mode.
// Skipped when the agent emitted no tool events — silent beats an empty
// banner.
func renderToolTrace(w io.Writer, events []sse.AgentToolEvent) {
	if len(events) == 0 {
		return
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "──── Tool trace ────")
	for i, e := range events {
		fmt.Fprintf(w, "[%d] %s", i+1, e.Kind)
		if e.Result != "" {
			fmt.Fprintf(w, "  %s", truncateInline(e.Result, 80))
		}
		fmt.Fprintln(w)
	}
}

// truncateInline shrinks a multi-line result to a single line + ellipsis
// for the human tool-trace footer.
func truncateInline(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-1] + "…"
}

// isCancelled reports whether err or ctx represents a context-cancelled
// state — true on Ctrl-C / SIGTERM after main.go's signal.NotifyContext
// fires. Wrapping URL/transport layers may rewrite context.Canceled into
// something errors.Is no longer recognises, so we fall back to ctx.Err().
func isCancelled(ctx context.Context, err error) bool {
	if errors.Is(err, context.Canceled) {
		return true
	}
	if ctx.Err() == context.Canceled {
		return true
	}
	return false
}

// compile-time check: production SDK client satisfies InvokeService.
var _ InvokeService = (*sdk.Client)(nil)
