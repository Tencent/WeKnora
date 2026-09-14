package sandbox

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"
)

// Only the server's skill installer may select the maintenance identity.
// Ordinary filesystem operations use the same account as shell/script exec.
type maintenanceFilesystemKey struct{}

func withMaintenanceFilesystem(ctx context.Context) context.Context {
	return context.WithValue(ctx, maintenanceFilesystemKey{}, true)
}

func remoteFileUser(ctx context.Context) string {
	if maintenance, _ := ctx.Value(maintenanceFilesystemKey{}).(bool); maintenance {
		return "root"
	}
	return DefaultSandboxExecUser
}

// Cube's SDK hardcodes root for file calls and offers no file-user option.
// Scope this compatibility adapter to filesystem routes; process requests
// already carry their explicitly selected user, and control-plane auth must
// be preserved. Request-local context avoids shared-client identity races.
type cubeFilesystemTransport struct {
	next    http.RoundTripper
	timeout time.Duration
}

func (t *cubeFilesystemTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r := req.Clone(req.Context())
	if r.URL.Path == envdFilesRoute || strings.HasPrefix(r.URL.Path, "/filesystem.Filesystem/") {
		r.Header.Set("Authorization", basicAuthorizationFor(remoteFileUser(req.Context())))
	}
	// A client-wide HTTP timeout also aborts long-running command streams
	// and large file bodies. Bound ordinary RPCs here; process calls and
	// filesystem content transfers use the caller's execution context.
	if t.timeout <= 0 || rpcUsesCallerDeadline(r.URL.Path) {
		return t.next.RoundTrip(r)
	}
	ctx, cancel := context.WithTimeout(r.Context(), t.timeout)
	response, err := t.next.RoundTrip(r.WithContext(ctx))
	if err != nil {
		cancel()
		return nil, err
	}
	response.Body = &cancelOnCloseBody{ReadCloser: response.Body, cancel: cancel}
	return response, nil
}

type cancelOnCloseBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelOnCloseBody) Close() error {
	defer b.cancel()
	return b.ReadCloser.Close()
}

func rpcUsesCallerDeadline(path string) bool {
	if strings.HasPrefix(path, "/process.Process/") {
		return true
	}
	return isFilesystemContentRPC(path)
}

func isFilesystemContentRPC(path string) bool {
	if path == envdFilesRoute {
		return true
	}
	name, ok := strings.CutPrefix(path, "/filesystem.Filesystem/")
	if !ok {
		return false
	}
	return strings.HasPrefix(name, "Read") || strings.HasPrefix(name, "Write")
}
