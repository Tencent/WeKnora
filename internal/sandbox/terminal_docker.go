package sandbox

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/moby/moby/client"
)

type dockerTerminalResizeAPI interface {
	ExecResize(context.Context, string, client.ExecResizeOptions) (client.ExecResizeResult, error)
}

func (a *dockerRPCTimeoutAPI) ExecResize(
	ctx context.Context, id string, opts client.ExecResizeOptions,
) (client.ExecResizeResult, error) {
	api, ok := a.inner.(dockerTerminalResizeAPI)
	if !ok {
		return client.ExecResizeResult{}, errors.New("sandbox: Docker exec resize unavailable")
	}
	ctx, cancel := a.rpcCtx(ctx)
	defer cancel()
	return api.ExecResize(ctx, id, opts)
}

// OpenCommandTerminal uses Engine TTY exec for a bounded command process tree.
func (c *DockerRemoteClient) OpenCommandTerminal(
	ctx context.Context, handle RemoteSandboxHandle, req CommandTerminalRequest,
) (CommandTerminal, error) {
	id, err := dockerHandleID("OpenCommandTerminal", handle)
	if err != nil {
		return nil, err
	}
	resize, ok := c.api.(dockerTerminalResizeAPI)
	if !ok {
		return nil, errors.New("sandbox: Docker native PTY unavailable")
	}
	return startCommandTerminal(ctx, req, c, handle, func(
		ctx context.Context, argv []string, token string, req CommandTerminalRequest,
	) (*terminalProcess, error) {
		size := client.ConsoleSize{Width: uint(req.Cols), Height: uint(req.Rows)}
		created, err := c.api.ExecCreate(ctx, id, client.ExecCreateOptions{
			Cmd: argv, TTY: true, AttachStdin: true, AttachStdout: true, AttachStderr: true,
			ConsoleSize: size,
			// Engine 20.10 cannot disable TTY detach keys. Reserve a fresh,
			// NUL-prefixed sequence instead of interpreting ordinary Ctrl-P/Q
			// input as a detach. Closing this API always terminates the runner.
			DetachKeys: "ctrl-@," + strings.Join(strings.Split(token, ""), ","),
			User:       DefaultSandboxExecUser, WorkingDir: SessionWorkspaceRoot,
			Env: []string{"TERM=xterm-256color", "LANG=C.UTF-8"},
		})
		if err != nil {
			return nil, dockerError("OpenCommandTerminal", err)
		}
		// ExecAttach starts the process. An ambiguous response is never retried.
		attached, err := c.api.ExecAttach(ctx, created.ID, client.ExecAttachOptions{TTY: true, ConsoleSize: size})
		if err != nil {
			return nil, dockerError("OpenCommandTerminal", err)
		}
		writes := make(chan struct{}, 1)
		return &terminalProcess{
			close: attached.Close,
			read: func(dst io.Writer) (int, error) {
				// TTY output has no Docker stdcopy headers, even for binary bytes.
				if _, err := io.CopyBuffer(dst, attached.Reader, make([]byte, 32*1024)); err != nil {
					return -1, err
				}
				for {
					state, err := c.api.ExecInspect(ctx, created.ID, client.ExecInspectOptions{})
					if err != nil {
						return -1, dockerError("TerminalWait", err)
					}
					if !state.Running {
						return state.ExitCode, nil
					}
					select {
					case <-ctx.Done():
						return -1, ctx.Err()
					case <-time.After(20 * time.Millisecond):
					}
				}
			},
			input: func(ctx context.Context, p []byte) error {
				select {
				case writes <- struct{}{}:
				case <-ctx.Done():
					return ctx.Err()
				}
				defer func() { <-writes }()
				deadline, _ := ctx.Deadline()
				if err := attached.Conn.SetWriteDeadline(deadline); err != nil {
					return err
				}
				canceled := make(chan struct{})
				stop := context.AfterFunc(ctx, func() {
					_ = attached.Conn.SetWriteDeadline(time.Now())
					close(canceled)
				})
				defer func() {
					if !stop() {
						<-canceled
					}
					_ = attached.Conn.SetWriteDeadline(time.Time{})
				}()
				for len(p) > 0 {
					n, err := attached.Conn.Write(p)
					if err != nil {
						return err
					}
					if n == 0 {
						return io.ErrShortWrite
					}
					p = p[n:]
				}
				return nil
			},
			resize: func(ctx context.Context, cols, rows uint16) error {
				_, err := resize.ExecResize(ctx, created.ID, client.ExecResizeOptions{
					Width: uint(cols), Height: uint(rows),
				})
				return dockerError("TerminalResize", err)
			},
		}, nil
	})
}

var _ remoteCommandTerminalProvider = (*DockerRemoteClient)(nil)
