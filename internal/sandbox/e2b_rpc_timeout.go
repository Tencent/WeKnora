package sandbox

import (
	"context"
	"net/http"
	"time"
)

// Keep the HTTP budget on ordinary requests (including their response bodies).
// Start and Connect stream for the command's lifetime and are bounded by the
// SDK's execution context instead. Parent cancellation is preserved throughout.
type e2bRPCTimeoutTransport struct {
	next    http.RoundTripper
	timeout time.Duration
}

func (t *e2bRPCTimeoutTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.timeout <= 0 || req.URL.Path == "/process.Process/Start" || req.URL.Path == "/process.Process/Connect" || isFilesystemContentRPC(req.URL.Path) {
		return t.next.RoundTrip(req)
	}
	ctx, cancel := context.WithTimeout(req.Context(), t.timeout)
	response, err := t.next.RoundTrip(req.Clone(ctx))
	if err != nil {
		cancel()
		return nil, err
	}
	response.Body = &cancelOnCloseBody{ReadCloser: response.Body, cancel: cancel}
	return response, nil
}

func (t *e2bRPCTimeoutTransport) CloseIdleConnections() {
	if closer, ok := t.next.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}
