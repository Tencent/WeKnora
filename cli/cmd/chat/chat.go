// Package chat implements `weknora chat <text>` — the streaming RAG answer
// entry point.
//
// Two output modes share a single SDK call:
//
//   - Stream mode (TTY + no --no-stream + no --json): write each
//     StreamResponse.Content fragment directly to iostreams.IO.Out as it
//     arrives, then print a footer with knowledge references. This is the
//     "feels alive" UX a human typing in a terminal expects.
//
//   - Accumulate mode (non-TTY, --no-stream, or --json): buffer every
//     fragment via sse.Accumulator and emit a single envelope (or a single
//     plain-text answer + references block) once Done. Agents and pipes
//     get a deterministic single record to parse.
//
// The SDK's KnowledgeQAStream callback contract is invoked sequentially on
// one goroutine, so neither mode needs locking. The runChat core takes a
// chatService interface so tests inject a fake without standing up a real
// SSE server.
package chat

import (
	"context"
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

// Options captures one `weknora chat` invocation.
type Options struct {
	Query     string
	KBID      string
	SessionID string
	NoStream  bool
	JSONOut   bool
}

// chatService is the narrow SDK surface this command depends on. *sdk.Client
// satisfies it; tests substitute a fake. Compile-time check is at the bottom
// of this file.
type chatService interface {
	CreateSession(ctx context.Context, req *sdk.CreateSessionRequest) (*sdk.Session, error)
	KnowledgeQAStream(ctx context.Context, sessionID string, req *sdk.KnowledgeQARequest, cb func(*sdk.StreamResponse) error) error
}

// chatData is the success-envelope payload. Mirrors what an agent needs to
// continue a conversation: the answer text, retrieval references, and the
// session pointer to thread follow-ups.
type chatData struct {
	Answer             string             `json:"answer"`
	References         []*sdk.SearchResult `json:"references"`
	SessionID          string             `json:"session_id"`
	AssistantMessageID string             `json:"assistant_message_id,omitempty"`
	KBID               string             `json:"kb_id"`
	Query              string             `json:"query"`
}

// NewCmd builds `weknora chat <text>`.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{}
	cmd := &cobra.Command{
		Use:   `chat <text>`,
		Short: "Ask a streaming RAG question against a knowledge base",
		Long: `Send a query to the WeKnora knowledge-chat endpoint and stream the
answer back. By default a fresh session is created on first invocation; pass
--session-id to continue an existing conversation.

Modes:
  TTY (default):              live token streaming + reference footer
  Pipe / --no-stream / --json: buffered, emitted once on completion`,
		Example: `  weknora chat "What is RRF?" --kb-id kb_123
  weknora chat "Summarise this design doc" --kb my-kb --json
  weknora chat "Continue?" --session-id sess_abc`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			opts.Query = strings.TrimSpace(strings.Join(args, " "))
			if opts.Query == "" {
				return cmdutil.NewError(cmdutil.CodeInputInvalidArgument, "query argument cannot be empty")
			}
			kbID, err := f.ResolveKB(c)
			if err != nil {
				return err
			}
			opts.KBID = kbID
			cli, err := f.Client()
			if err != nil {
				return err
			}
			return runChat(c.Context(), opts, cli)
		},
	}
	cmd.Flags().String("kb-id", "", "Knowledge base ID (overrides project link / env)")
	cmd.Flags().String("kb", "", "Knowledge base name (resolved via list)")
	cmd.Flags().StringVar(&opts.SessionID, "session-id", "", "Continue an existing chat session (skip auto-create)")
	cmd.Flags().BoolVar(&opts.NoStream, "no-stream", false, "Buffer the full answer before printing (forces accumulate mode)")
	cmd.Flags().BoolVar(&opts.JSONOut, "json", false, "Emit a single JSON envelope (implies --no-stream)")
	cmd.MarkFlagsMutuallyExclusive("kb-id", "kb")
	return cmd
}

// runChat is the testable core: validate, ensure a session, dispatch the
// stream, and route output. Returns a typed error suitable for the envelope.
func runChat(ctx context.Context, opts *Options, svc chatService) error {
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

	sessionID := opts.SessionID
	autoCreated := false
	if sessionID == "" {
		sess, err := svc.CreateSession(ctx, &sdk.CreateSessionRequest{Title: "weknora chat"})
		if err != nil {
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

	// Decide output mode. Stream mode requires:
	//   1. an interactive stdout (tty)
	//   2. no --no-stream
	//   3. no --json (envelope is single-record by definition)
	streamMode := iostreams.IO.IsStdoutTTY() && !opts.NoStream && !opts.JSONOut

	// Surface the auto-created session ID up-front so a user who hits ^C
	// mid-stream still has the pointer to resume — no need to scroll back
	// past tokens. Skipped in JSON mode (it ends up in the envelope) and
	// when the caller already supplied --session-id.
	if autoCreated && !opts.JSONOut {
		fmt.Fprintf(iostreams.IO.Err, "session: %s (use --session-id to continue)\n", sessionID)
	}

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
		// tokens, especially on ^C) can still recover with --session-id.
		// Skipped in JSON mode — the envelope carries it in .data.session_id.
		if autoCreated && !opts.JSONOut {
			fmt.Fprintf(iostreams.IO.Err, "session: %s (resume with --session-id %s)\n", sessionID, sessionID)
		}
		// Context cancelled (Ctrl-C) → user-aborted, exit 130 lineage.
		if errors.Is(streamErr, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			return cmdutil.Wrapf(cmdutil.CodeUserAborted, streamErr, "chat cancelled")
		}
		// Stream began (we observed at least one event) but never reached a
		// terminal Done frame: typed as sse_stream_aborted so the hint
		// nudges the user toward --no-stream / retry.
		if acc.Result() != "" && !acc.Done() {
			return cmdutil.Wrapf(cmdutil.CodeSSEStreamAborted, streamErr, "stream aborted before completion")
		}
		// Pre-stream HTTP / transport failure: route through the canonical
		// classifier so 401 / 404 / 5xx still surface their specific codes.
		return cmdutil.Wrapf(cmdutil.ClassifyHTTPError(streamErr), streamErr, "knowledge qa stream")
	}

	// SDK returned nil but we never saw a Done event — server closed the
	// connection cleanly mid-stream. Treat as aborted so the user sees the
	// truncation rather than a silent partial answer. Includes the empty-body
	// case (Done frame never arrived AND no content): better to surface the
	// abort than emit ok=true with answer="" — agents can't distinguish the
	// model genuinely had nothing to say from the stream getting cut.
	if !acc.Done() {
		return cmdutil.NewError(cmdutil.CodeSSEStreamAborted, "stream ended without a terminal event")
	}

	answer := acc.Result()
	references := acc.References

	if opts.JSONOut {
		// Prefer the SDK-echoed session id (acc.SessionID) but fall back to
		// our local sessionID — agents must always see a usable pointer.
		sid := acc.SessionID
		if sid == "" {
			sid = sessionID
		}
		data := chatData{
			Answer:             answer,
			References:         references,
			SessionID:          sid,
			AssistantMessageID: acc.AssistantMessageID,
			KBID:               opts.KBID,
			Query:              opts.Query,
		}
		return cmdutil.NewJSONExporter().Write(iostreams.IO.Out, format.Success(data, &format.Meta{KBID: opts.KBID}))
	}

	// Human / non-JSON paths: streaming mode already wrote the answer body
	// via the callback, so we only need to render the trailing references
	// (and a closing newline). Accumulate + non-JSON writes the answer here
	// for the first time.
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
	renderReferences(out, references)
	return nil
}

// renderReferences prints a compact human-readable references block.
// Skipped entirely when the server returned no references — agent-friendly
// silence beats an empty banner.
func renderReferences(w io.Writer, refs []*sdk.SearchResult) {
	if len(refs) == 0 {
		return
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "──── References ────")
	for i, r := range refs {
		if r == nil {
			continue
		}
		title := r.KnowledgeTitle
		if title == "" {
			title = r.KnowledgeFilename
		}
		if title == "" {
			title = r.KnowledgeID
		}
		fmt.Fprintf(w, "[%d] %s", i+1, title)
		if r.Score > 0 {
			fmt.Fprintf(w, "  score=%.3f", r.Score)
		}
		fmt.Fprintln(w)
	}
}

// compile-time check: the production SDK client implements chatService.
var _ chatService = (*sdk.Client)(nil)
