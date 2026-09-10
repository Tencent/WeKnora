package sandbox

import (
	"context"
	"fmt"
)

// SessionCommandStreamManager opens a server-selected command in a live sandbox.
// It never creates, resumes, or replaces a session sandbox.
type SessionCommandStreamManager interface {
	OpenSessionCommandStream(context.Context, string, string) (RemoteTerminalSession, error)
}

// RemoteCommandStreamManager opens duplex command streams on a provider.
type RemoteCommandStreamManager interface {
	OpenCommandStream(context.Context, RemoteSandboxHandle, string) (RemoteTerminalSession, error)
}

// OpenSessionCommandStream opens a fixed command on an existing live sandbox.
func (m *SessionBoundManager) OpenSessionCommandStream(
	ctx context.Context, sessionID, command string,
) (RemoteTerminalSession, error) {
	state, bound, err := m.peekBoundSandboxState(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if !bound {
		return nil, ErrNoLiveSessionSandbox
	}
	if state != RemoteStateRunning {
		return nil, ErrSandboxPaused
	}
	handle, found, err := m.lookupSessionHandle(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrNoLiveSessionSandbox
	}
	return openRemoteCommandStream(ctx, m.client, handle, command)
}

func openRemoteCommandStream(
	ctx context.Context, client RemoteSandboxClient, handle RemoteSandboxHandle, command string,
) (RemoteTerminalSession, error) {
	if remote, ok := client.(RemoteCommandStreamManager); ok {
		return remote.OpenCommandStream(ctx, handle, command)
	}
	terminal, ok := TerminalManagerFrom(client)
	if !ok {
		return nil, fmt.Errorf("sandbox does not support command streaming")
	}
	stream, err := terminal.OpenTerminal(ctx, handle, RemoteTerminalOptions{})
	if err != nil {
		return nil, err
	}
	// PTY adapters expose an interactive shell. Disable echo/canonical processing
	// before replacing it with our fixed bridge; user input is never shell text.
	if err := stream.Write(ctx, []byte("stty raw -echo; exec /bin/sh -c "+ShellQuote(command)+"\n")); err != nil {
		_ = stream.Close()
		return nil, err
	}
	return stream, nil
}

func (c *langfuseRemoteClient) OpenCommandStream(
	ctx context.Context, handle RemoteSandboxHandle, command string,
) (RemoteTerminalSession, error) {
	return openRemoteCommandStream(ctx, c.inner, handle, command)
}
