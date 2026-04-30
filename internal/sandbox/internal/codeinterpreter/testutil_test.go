package codeinterpreter

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// newMockSandbox creates a *Sandbox whose jupyterURL() points at the given
// httptest.Server, so tests can exercise RunCode / context handlers without a
// real e2b backend.
func newMockSandbox(t *testing.T, server *httptest.Server) *Sandbox {
	t.Helper()

	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse mock server url: %v", err)
	}

	// Use the httptest server's own client (which trusts the test TLS root,
	// if any). We inject a RoundTripper that rewrites the host to the test
	// server's address regardless of what the SDK builds.
	client := server.Client()
	origTransport := client.Transport
	if origTransport == nil {
		origTransport = http.DefaultTransport
	}
	client.Transport = &rewriteTransport{
		base:       origTransport,
		targetHost: u.Host,
		scheme:     u.Scheme,
	}

	return &Sandbox{
		id:       "sbx-test",
		clientID: "client-test",
		template: DefaultTemplate,
		connection: &ConnectionConfig{
			APIKey:         "test-api-key",
			Domain:         "e2b.test",
			Debug:          true,
			RequestTimeout: 5 * time.Second,
			HTTPClient:     client,
		},
	}
}

type rewriteTransport struct {
	base       http.RoundTripper
	targetHost string
	scheme     string
}

func (r *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme = r.scheme
	req.URL.Host = r.targetHost
	req.Host = r.targetHost
	return r.base.RoundTrip(req)
}

func ndjson(lines ...string) string {
	return strings.Join(lines, "\n")
}
