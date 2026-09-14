// Package sandbox provides graphical desktop (VNC over WebSocket) capability.
//
// The desktop is an optional, provider-neutral capability alongside
// RemoteTerminalManager. It differs from the terminal in one structural way:
// the terminal rides envd's PTY service, while the desktop is a raw WebSocket
// to a non-envd port (websockify on 6080) routed through the same gateway.
// That is why it needs its own capability bit and its own dialer.
package sandbox

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"

	"github.com/gorilla/websocket"
)

// ErrDesktopUnsupported is returned when the session's backend cannot relay a
// desktop, or the config's template is not a desktop image.
var ErrDesktopUnsupported = errors.New("sandbox: desktop is not supported by this backend")

// RemoteDesktopOptions carries the neutral parameters for dialling a desktop.
type RemoteDesktopOptions struct {
	// BasicAuthUser / BasicAuthPassword authenticate against websockify's
	// BasicHTTPAuth plugin. The password is read out of the sandbox at
	// DesktopSecretPath and MUST NOT be sent to the browser: it is a
	// backend-to-sandbox credential only.
	// An empty user falls back to DesktopBasicAuthUser.
	BasicAuthUser     string
	BasicAuthPassword string
}

// dialSandboxDesktop is the provider-neutral half of DialDesktop. Both
// adapters call it so the credential shape and the error mapping cannot drift.
func dialSandboxDesktop(
	ctx context.Context,
	provider RemoteProvider,
	dialer *WebsocketDialer,
	handle RemoteSandboxHandle,
	opts RemoteDesktopOptions,
) (*websocket.Conn, error) {
	if handle == nil || strings.TrimSpace(handle.ID()) == "" {
		return nil, &RemoteError{
			Kind: RemoteErrorKindInvalidRequest, Provider: provider,
			Op: "DialDesktop", Message: "handle is required",
		}
	}
	header := http.Header{}
	// Cube exec/PTY attach this from the SDK sandbox object, so a terminal
	// still works when the pool registry is empty. DialDesktop used to read
	// only the registry, which Cube Create/Connect never wrote, and CubeProxy
	// answered 401 on 6080 while envd on 49983 was fine.
	if token := InboundTokenOf(handle); token != "" {
		header.Set(InboundTokenHeader, token)
	}
	if opts.BasicAuthPassword != "" {
		user := opts.BasicAuthUser
		if user == "" {
			user = DesktopBasicAuthUser
		}
		header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString(
			[]byte(user+":"+opts.BasicAuthPassword)))
	}
	conn, resp, err := dialer.Dial(
		ctx, handle.ID(), DesktopWebsockifyPort, DesktopWebsockifyPath, header)
	if err != nil {
		kind := RemoteErrorKindUnavailable
		status := 0
		if resp != nil {
			status = resp.StatusCode
			if status == http.StatusUnauthorized || status == http.StatusForbidden {
				kind = RemoteErrorKindAuthentication
			}
		}
		return nil, &RemoteError{
			Kind: kind, Provider: provider, Op: "DialDesktop",
			Message: "dial websockify", StatusCode: status, Cause: err,
		}
	}
	return conn, nil
}

// RemoteDesktopManager is the optional capability interface for backends that
// can relay a WebSocket to the desktop port of a running sandbox. Mirrors
// RemoteTerminalManager: advertised via RemoteSandboxCapabilities
// (SupportsDesktop) and narrowed with DesktopManagerFrom.
type RemoteDesktopManager interface {
	// DialDesktop opens a WebSocket to websockify inside the sandbox behind
	// handle. The returned connection carries raw RFB bytes in binary frames.
	DialDesktop(ctx context.Context, handle RemoteSandboxHandle, opts RemoteDesktopOptions) (*websocket.Conn, error)
}

// DesktopManagerFrom narrows a client to its desktop capability.
//
// Both signals must agree, exactly as in TerminalManagerFrom: the type
// assertion finds the method, and SupportsDesktop is the advertised
// capability. Either one alone is not enough — a decorator can carry the
// method while the inner backend cannot serve it.
func DesktopManagerFrom(client RemoteSandboxClient) (RemoteDesktopManager, bool) {
	if client == nil {
		return nil, false
	}
	mgr, ok := client.(RemoteDesktopManager)
	if !ok {
		return nil, false
	}
	if !client.Capabilities().SupportsDesktop {
		return nil, false
	}
	return mgr, true
}

// RemoteDesktopTTLRefresher extends a desktop backend with provider idle-TTL
// refresh. DialDesktop must not start that loop: its ctx is the HTTP request,
// which is cancelled on handshake abort. The WebSocket handler starts refresh
// on the relay ctx (WithoutCancel) after upgrade, matching OpenTerminal.
type RemoteDesktopTTLRefresher interface {
	StartDesktopTTLRefresh(ctx context.Context, handle RemoteSandboxHandle)
}

// DesktopTTLRefresherFrom narrows a client to desktop TTL refresh.
//
// SupportsTimeoutRefresh is the advertised capability (Docker is false: it
// has no SetTimeout). The type assertion finds the method through wrappers
// such as Langfuse, the same two-signal pattern as DesktopManagerFrom.
func DesktopTTLRefresherFrom(client RemoteSandboxClient) (RemoteDesktopTTLRefresher, bool) {
	if client == nil {
		return nil, false
	}
	refresher, ok := client.(RemoteDesktopTTLRefresher)
	if !ok {
		return nil, false
	}
	if !client.Capabilities().SupportsTimeoutRefresh {
		return nil, false
	}
	return refresher, true
}
