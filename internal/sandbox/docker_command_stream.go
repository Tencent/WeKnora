package sandbox

import (
	"context"
	"io"
	"sync"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/client"
)

// OpenCommandStream uses a non-PTY exec, keeping JSON framing independent of terminal modes.
func (c *DockerRemoteClient) OpenCommandStream(
	ctx context.Context, handle RemoteSandboxHandle, command string,
) (RemoteTerminalSession, error) {
	id, err := dockerHandleID("OpenCommandStream", handle)
	if err != nil {
		return nil, err
	}
	created, err := c.api.ExecCreate(ctx, id, client.ExecCreateOptions{
		Cmd: []string{"/bin/sh", "-c", command}, User: DefaultSandboxExecUser,
		AttachStdin: true, AttachStdout: true, AttachStderr: true,
	})
	if err != nil {
		return nil, err
	}
	attached, err := c.api.ExecAttach(ctx, created.ID, client.ExecAttachOptions{})
	if err != nil {
		return nil, err
	}
	stream := &dockerCommandStream{
		out: make(chan RemoteTerminalEvent, 16), closed: make(chan struct{}), attached: attached,
	}
	go func() {
		defer close(stream.out)
		_, err := stdcopy.StdCopy(streamWriter{stream}, io.Discard, attached.Reader)
		if err != nil {
			emitTerminalEvent(stream.out, stream.closed, RemoteTerminalEvent{Err: err})
		}
		_ = stream.Close()
	}()
	go func() {
		select {
		case <-ctx.Done():
			_ = stream.Close()
		case <-stream.closed:
		}
	}()
	return stream, nil
}

type dockerCommandStream struct {
	out      chan RemoteTerminalEvent
	closed   chan struct{}
	once     sync.Once
	writeMu  sync.Mutex
	attached client.ExecAttachResult
}

func (s *dockerCommandStream) Output() <-chan RemoteTerminalEvent           { return s.out }
func (s *dockerCommandStream) PID() uint32                                  { return 0 }
func (s *dockerCommandStream) Resize(context.Context, uint32, uint32) error { return nil }
func (s *dockerCommandStream) Close() error {
	s.once.Do(func() { close(s.closed); s.attached.Close() })
	return nil
}

func (s *dockerCommandStream) Write(ctx context.Context, data []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	deadline := time.Now().Add(5 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := s.attached.Conn.SetWriteDeadline(deadline); err != nil {
		return err
	}
	_, err := s.attached.Conn.Write(data)
	return err
}

type streamWriter struct{ s *dockerCommandStream }

func (w streamWriter) Write(p []byte) (int, error) {
	data := append([]byte(nil), p...)
	select {
	case w.s.out <- RemoteTerminalEvent{Data: data}:
		return len(p), nil
	case <-w.s.closed:
		return 0, io.ErrClosedPipe
	}
}
