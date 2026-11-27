package sandbox

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// e2bControlPlaneAuthTransport keeps the SDK's X-API-Key authentication
// as the default and retries a gateway's explicit missing-header rejection
// with the same key as a bearer credential.
type e2bControlPlaneAuthTransport struct {
	next     http.RoundTripper
	endpoint *url.URL
}

func (t *e2bControlPlaneAuthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.next.RoundTrip(req)
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		return resp, err
	}
	key := req.Header.Get("X-API-Key")
	if key == "" || req.Header.Get("Authorization") != "" || isEnvdDataPlaneRequest(req) ||
		req.URL.Scheme != t.endpoint.Scheme || !strings.EqualFold(req.URL.Host, t.endpoint.Host) ||
		!strings.HasPrefix(req.URL.EscapedPath(), strings.TrimRight(t.endpoint.EscapedPath(), "/")+"/") ||
		(req.Body != nil && req.Body != http.NoBody && req.GetBody == nil) {
		return resp, nil
	}

	// Peek at a bounded error body and keep it readable for the SDK if there
	// is no challenge. A generic 401 can mean a genuinely invalid API key.
	reader := bufio.NewReaderSize(resp.Body, 4096)
	resp.Body = struct {
		io.Reader
		io.Closer
	}{reader, resp.Body}
	data, _ := reader.Peek(4096)
	var failure struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(data, &failure) != nil || failure.Message != "authorization header is missing" {
		return resp, nil
	}

	retry := req.Clone(req.Context())
	if req.GetBody != nil {
		retry.Body, err = req.GetBody()
		if err != nil {
			return resp, nil
		}
	}
	_ = resp.Body.Close()
	retry.Header.Set("Authorization", "Bearer "+key)
	return t.next.RoundTrip(retry)
}

func (t *e2bControlPlaneAuthTransport) CloseIdleConnections() {
	if closer, ok := t.next.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}
