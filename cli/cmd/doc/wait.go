package doc

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"sync"
	"time"

	sdk "github.com/Tencent/WeKnora/client"
	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
)

// WaitOptions captures `doc wait` flag state.
type WaitOptions struct {
	IDs      []string
	Timeout  time.Duration
	Interval time.Duration
}

// NewCmdWait builds `weknora doc wait <id> [<id>...]`.
//
// Multi-id behaviour is always wait-all: blocks until every id reaches a
// terminal state. Use shell composition
// (`weknora doc wait id1 && weknora doc wait id2`) when fail-fast is
// desired.
func NewCmdWait(f *cmdutil.Factory) *cobra.Command {
	opts := &WaitOptions{}
	cmd := &cobra.Command{
		Use:   "wait <doc-id> [<doc-id>...]",
		Short: "Wait for one or more documents to finish parsing",
		Long: `Block until every given document reaches a terminal parse_status
(completed or failed), the timeout expires, or the user interrupts (Ctrl-C).
Always wait-all: every id must reach a terminal state before returning.

Exit codes:
  0    all completed
  1    any failed
  124  --timeout reached (matches GNU 'timeout' command)
  130  Ctrl-C / SIGINT

Multi-id is polled concurrently (max 5 parallel; use 'xargs -P' for more).
For fail-fast semantics, use shell composition:
  weknora doc wait id1 && weknora doc wait id2 && weknora doc wait id3`,
		Example: `  weknora doc wait doc_abc
  weknora doc wait id1 id2 id3 --timeout 20m
  weknora doc wait id1 id2 --format ndjson`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			// Validate flags FIRST so an invalid --format doesn't cost the
			// user a multi-minute poll before erroring out.
			fopts, err := cmdutil.CheckFormatFlag(c)
			if err != nil {
				return err
			}
			fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())

			opts.IDs = args
			cli, err := f.Client()
			if err != nil {
				return err
			}
			res, err := waitForDocs(c.Context(), opts.IDs, cli, *opts)
			if err != nil {
				return err
			}

			if err := emitWaitResult(res, fopts, iostreams.IO.Out); err != nil {
				return err
			}

			switch res.ExitCode() {
			case 0:
				return nil
			case 1:
				return cmdutil.NewError(cmdutil.CodeOperationFailed, fmt.Sprintf("%d doc(s) failed", len(res.Failed)))
			case 124:
				return cmdutil.NewError(cmdutil.CodeOperationTimeout, fmt.Sprintf("wait timed out (%d doc(s) still pending)", len(res.Timeout)))
			}
			return nil
		},
	}
	cmd.Flags().DurationVar(&opts.Timeout, "timeout", 10*time.Minute, "Max wait time before exiting 124")
	cmd.Flags().DurationVar(&opts.Interval, "interval", 2*time.Second, "Initial poll interval; exponential backoff capped at 15s + jitter")
	cmdutil.AddFormatFlag(cmd)
	return cmd
}

// ---------------------------------------------------------------------------
// Core poll loop (B2)
// ---------------------------------------------------------------------------

// WaitService is the narrow SDK surface needed for polling.
type WaitService interface {
	GetKnowledge(ctx context.Context, id string) (*sdk.Knowledge, error)
}

// WaitResult is the terminal-state partition returned by waitForDocs.
type WaitResult struct {
	Completed []string    `json:"completed"`
	Failed    []FailedDoc `json:"failed,omitempty"`
	Timeout   []string    `json:"timeout,omitempty"`
}

// FailedDoc carries the id + reason for a doc that reached parse_status=failed
// or that GetKnowledge returned an error for.
type FailedDoc struct {
	ID      string `json:"id"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

const (
	maxConcurrentPolls = 5
	maxBackoffInterval = 15 * time.Second
	jitterMax          = 500 * time.Millisecond
)

// waitForDocs polls each id until terminal state or timeout. Concurrency is
// bounded by maxConcurrentPolls. Returns the partitioned terminal state.
// Always waits for every id (wait-all semantics).
//
// Exponential backoff starts at opts.Interval, doubles each tick, caps at
// maxBackoffInterval, with up to jitterMax random jitter added per sleep.
func waitForDocs(ctx context.Context, ids []string, svc WaitService, opts WaitOptions) (*WaitResult, error) {
	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	result := &WaitResult{}
	var mu sync.Mutex
	addCompleted := func(id string) {
		mu.Lock()
		defer mu.Unlock()
		result.Completed = append(result.Completed, id)
	}
	addFailed := func(fd FailedDoc) {
		mu.Lock()
		defer mu.Unlock()
		result.Failed = append(result.Failed, fd)
	}
	addTimeout := func(id string) {
		mu.Lock()
		defer mu.Unlock()
		result.Timeout = append(result.Timeout, id)
	}

	sem := make(chan struct{}, maxConcurrentPolls)
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			interval := opts.Interval
			for {
				select {
				case <-ctx.Done():
					addTimeout(id)
					return
				default:
				}

				doc, err := svc.GetKnowledge(ctx, id)
				if err != nil {
					if ctx.Err() == context.DeadlineExceeded {
						addTimeout(id)
						return
					}
					addFailed(FailedDoc{ID: id, Message: err.Error()})
					return
				}

				switch doc.ParseStatus {
				case "completed":
					addCompleted(id)
					return
				case "failed":
					addFailed(FailedDoc{ID: id, Message: doc.ErrorMessage})
					return
				}

				// Not yet terminal — sleep with jitter, then exp-backoff.
				jitter := time.Duration(rand.Int63n(int64(jitterMax)))
				timer := time.NewTimer(interval + jitter)
				select {
				case <-ctx.Done():
					timer.Stop()
					addTimeout(id)
					return
				case <-timer.C:
				}
				interval *= 2
				if interval > maxBackoffInterval {
					interval = maxBackoffInterval
				}
			}
		}(id)
	}
	wg.Wait()
	return result, nil
}

// ExitCode resolves the compound terminal state to a Unix exit code per
// spec §3.2: priority 1 > 124 > 0 (failed > timeout > completed). SIGINT
// (exit 130) is handled by the Go runtime / context cancellation, not here.
func (r *WaitResult) ExitCode() int {
	if len(r.Failed) > 0 {
		return 1
	}
	if len(r.Timeout) > 0 {
		return 124
	}
	return 0
}

// Compile-time assertion: *sdk.Client satisfies WaitService.
var _ WaitService = (*sdk.Client)(nil)

// ---------------------------------------------------------------------------
// B5: output rendering
// ---------------------------------------------------------------------------

// emitWaitResult renders r according to --format. Output writer is
// parametrized for tests; production callers pass iostreams.IO.Out.
func emitWaitResult(r *WaitResult, fopts *cmdutil.FormatOptions, w io.Writer) error {
	switch fopts.Mode {
	case cmdutil.FormatJSON, cmdutil.FormatNDJSON:
		return fopts.Emit(w, r)
	case cmdutil.FormatText, "":
		return writeWaitText(w, r)
	default:
		return fmt.Errorf("unsupported --format %q for doc wait", fopts.Mode)
	}
}

// writeWaitText renders r as human-readable lines:
//
//	✓ <id> completed
//	✗ <id> failed: <message>
//	⏱ <id> timeout
func writeWaitText(w io.Writer, r *WaitResult) error {
	for _, id := range r.Completed {
		if _, err := fmt.Fprintf(w, "✓ %s completed\n", id); err != nil {
			return err
		}
	}
	for _, fd := range r.Failed {
		msg := fd.Message
		if msg == "" {
			msg = "(no message)"
		}
		if _, err := fmt.Fprintf(w, "✗ %s failed: %s\n", fd.ID, msg); err != nil {
			return err
		}
	}
	for _, id := range r.Timeout {
		if _, err := fmt.Fprintf(w, "⏱ %s timeout\n", id); err != nil {
			return err
		}
	}
	return nil
}
