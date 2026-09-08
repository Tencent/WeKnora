package sandbox

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
)

// These programs are argv payloads for provider exec, never host processes.
//
//go:embed terminal_runner.py
var terminalRunner string

//go:embed terminal_control.py
var terminalControl string

const terminalCleanupTimeout = 10 * time.Second
const terminalOperationTimeout = 5 * time.Second
const terminalBufferLimit = 256 * 1024

var ErrTerminalOutputLimit = errors.New("sandbox: terminal unread output exceeds 256 KiB")

type terminalProcess struct {
	read   func(io.Writer) (int, error)
	close  func()
	input  func(context.Context, []byte) error
	resize func(context.Context, uint16, uint16) error
	stop   func(context.Context) error
}

type terminalResult struct {
	exit TerminalExit
	err  error
}

type commandTerminal struct {
	process   *terminalProcess
	client    RemoteSandboxClient
	handle    RemoteSandboxHandle
	token     string
	done      chan struct{}
	stop      chan struct{}
	closeOnce sync.Once
	mu        sync.Mutex
	readable  *sync.Cond
	buffer    bytes.Buffer
	result    terminalResult
	finished  bool
	closed    bool
	ops       context.Context
}

func startCommandTerminal(ctx context.Context, req TerminalRequest, client RemoteSandboxClient,
	handle RemoteSandboxHandle, open func(context.Context, []string, string, TerminalRequest) (*terminalProcess, error),
) (Terminal, error) {
	req, err := normalizeTerminalRequest(req)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	token := "weknora-terminal-" + uuid.NewString()
	argv := []string{"python3", "-I", "-u", "-c", terminalRunner, token,
		strconv.FormatFloat(req.Timeout.Seconds(), 'f', 9, 64), strconv.Itoa(req.CPUSeconds),
		strconv.FormatInt(req.MemoryBytes, 10), strconv.Itoa(int(req.Cols)), strconv.Itoa(int(req.Rows)), req.Command}
	streamCtx, cancelStream := context.WithTimeout(context.WithoutCancel(ctx), req.Timeout+45*time.Second)
	stopOpening := context.AfterFunc(ctx, cancelStream)
	openTimer := time.AfterFunc(30*time.Second, cancelStream)
	process, err := open(streamCtx, argv, token, req)
	openTimer.Stop()
	stopOpening()
	if err != nil {
		cancelStream()
		// Start might have reached the server even if its response was lost.
		// Never retry it. The runner also enforces its deadline independently.
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), terminalCleanupTimeout)
		defer cancel()
		return nil, errors.Join(terminalProviderError("start", err), controlTerminal(cleanupCtx, client, handle, token, "stop"))
	}
	ops, cancelOps := context.WithCancel(context.WithoutCancel(ctx))
	t := &commandTerminal{process: process, client: client, handle: handle, token: token,
		done: make(chan struct{}), stop: make(chan struct{}), ops: ops}
	t.readable = sync.NewCond(&t.mu)
	readDone := make(chan terminalResult, 1)
	go func() {
		code, err := process.read(t)
		readDone <- terminalResult{exit: terminalExitFor(code), err: err}
	}()
	go func() {
		defer cancelStream()
		defer cancelOps()
		timer := time.NewTimer(req.Timeout + 250*time.Millisecond)
		defer timer.Stop()
		var result terminalResult
		var readFinished bool
		select {
		case result = <-readDone:
			readFinished = true
		case <-ctx.Done():
			result.exit = TerminalExit{ExitCode: -1, Reason: "canceled"}
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				result.exit.Reason = "timeout"
			}
		case <-timer.C:
			result.exit = TerminalExit{ExitCode: 124, Reason: "timeout"}
		case <-t.stop:
			result.exit = TerminalExit{ExitCode: -1, Reason: "closed"}
		}
		if !readFinished || result.err != nil {
			t.mu.Lock()
			if t.closed && errors.Is(result.err, io.ErrClosedPipe) {
				result = terminalResult{exit: TerminalExit{ExitCode: -1, Reason: "closed"}}
			}
			t.mu.Unlock()
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), terminalCleanupTimeout)
			if process.stop != nil {
				_ = process.stop(cleanupCtx)
			}
			cleanupErr := controlTerminal(cleanupCtx, client, handle, token, "stop")
			cancel()
			result.err = errors.Join(result.err, cleanupErr)
		}
		process.close()
		cancelStream()
		if !readFinished {
			<-readDone
		}
		if errors.Is(result.err, ErrTerminalOutputLimit) {
			result.exit.Reason = "output_limit"
		}
		if result.err != nil && result.exit.Reason == "exited" {
			result.exit.Reason = "error"
		}
		result.err = terminalProviderError("stream", result.err)
		t.mu.Lock()
		t.result, t.finished = result, true
		t.readable.Broadcast()
		t.mu.Unlock()
		close(t.done)
	}()
	return t, nil
}

func terminalExitFor(code int) TerminalExit {
	reason := "exited"
	switch code {
	case 124:
		reason = "timeout"
	case 130:
		reason = "interrupted"
	case 152:
		reason = "cpu_limit"
	}
	return TerminalExit{ExitCode: code, Reason: reason}
}

func controlTerminal(ctx context.Context, client RemoteSandboxClient, handle RemoteSandboxHandle, token, op string) error {
	result, err := client.Exec(ctx, handle, RemoteExecRequest{Command: "python3",
		Args: []string{"-I", "-u", "-c", terminalControl, token, op},
		User: DefaultSandboxExecUser, Timeout: 6 * time.Second})
	if err != nil {
		return terminalProviderError(op, err)
	}
	if result == nil || result.ExitCode != 0 || result.Killed {
		return fmt.Errorf("sandbox: terminal %s was not acknowledged", op)
	}
	return nil
}

// Provider diagnostics can echo argv or credentials. Keep the cause for
// errors.Is/As, but never format it into logs or the public terminal error.
type terminalOperationError struct {
	op    string
	cause error
}

func (e *terminalOperationError) Error() string { return "sandbox: terminal " + e.op + " failed" }
func (e *terminalOperationError) Unwrap() error { return e.cause }

func terminalProviderError(op string, err error) error {
	if err == nil {
		return nil
	}
	return &terminalOperationError{op: op, cause: err}
}

func (t *commandTerminal) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return 0, io.ErrClosedPipe
	}
	if len(p) > terminalBufferLimit-t.buffer.Len() {
		return 0, ErrTerminalOutputLimit
	}
	n, err := t.buffer.Write(p)
	t.readable.Broadcast()
	return n, err
}

func (t *commandTerminal) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for t.buffer.Len() == 0 && !t.finished && !t.closed {
		t.readable.Wait()
	}
	if t.closed {
		return 0, io.ErrClosedPipe
	}
	if t.buffer.Len() > 0 {
		return t.buffer.Read(p)
	}
	if t.result.err != nil {
		return 0, t.result.err
	}
	return 0, io.EOF
}

func (t *commandTerminal) Close() error {
	t.closeOnce.Do(func() {
		t.mu.Lock()
		t.closed = true
		t.buffer.Reset()
		t.readable.Broadcast()
		t.mu.Unlock()
		close(t.stop)
	})
	<-t.done
	return t.result.err
}

func (t *commandTerminal) Wait(ctx context.Context) (TerminalExit, error) {
	select {
	case <-t.done:
		return t.result.exit, t.result.err
	case <-ctx.Done():
		return TerminalExit{}, ctx.Err()
	}
}

func (t *commandTerminal) operation(ctx context.Context, fn func(context.Context) error) error {
	select {
	case <-t.done:
		return io.ErrClosedPipe
	default:
	}
	ctx, cancel := context.WithTimeout(ctx, terminalOperationTimeout)
	defer cancel()
	stop := context.AfterFunc(t.ops, cancel)
	defer stop()
	if err := ctx.Err(); err != nil {
		return err
	}
	return terminalProviderError("control", fn(ctx))
}

func (t *commandTerminal) Input(ctx context.Context, p []byte) error {
	if len(p) > 64*1024 {
		return errors.New("sandbox: terminal input exceeds 64 KiB")
	}
	return t.operation(ctx, func(ctx context.Context) error { return t.process.input(ctx, p) })
}

func (t *commandTerminal) Resize(ctx context.Context, cols, rows uint16) error {
	if cols == 0 || rows == 0 {
		return errors.New("sandbox: terminal dimensions must be positive")
	}
	return t.operation(ctx, func(ctx context.Context) error { return t.process.resize(ctx, cols, rows) })
}

func (t *commandTerminal) Interrupt(ctx context.Context) error {
	return t.operation(ctx, func(ctx context.Context) error {
		return controlTerminal(ctx, t.client, t.handle, t.token, "interrupt")
	})
}
