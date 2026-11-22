package mcp

import (
	"io"
	"net/http"
	"net/url"
	"strings"
)

// oauthResourceClient supplies the RFC 8707 fallback for explicit authorization
// metadata: mcp-go v1.1.1 does not retain the MCP resource in that discovery path.
// Preserve discovered resources, the caller's HTTP policy and request ownership.
func oauthResourceClient(client *http.Client, resource string) *http.Client {
	if client == nil {
		client = newOAuthHTTPClient()
	}
	copy := *client
	base := copy.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	copy.Transport = &oauthResourceTransport{base: base, resource: resource}
	return &copy
}

type oauthResourceTransport struct {
	base     http.RoundTripper
	resource string
}

func (t *oauthResourceTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodPost || !strings.HasPrefix(req.Header.Get("Content-Type"), "application/x-www-form-urlencoded") || req.GetBody == nil {
		return t.base.RoundTrip(req)
	}
	body, err := req.GetBody()
	if err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(body)
	body.Close()
	if err != nil {
		return nil, err
	}
	form, err := url.ParseQuery(string(raw))
	if err != nil {
		return nil, err
	}
	grant := form.Get("grant_type")
	if form.Has("resource") || (grant != "authorization_code" && grant != "refresh_token") {
		return t.base.RoundTrip(req)
	}
	form.Set("resource", t.resource)
	encoded := form.Encode()
	clone := req.Clone(req.Context())
	clone.Body = io.NopCloser(strings.NewReader(encoded))
	clone.ContentLength = int64(len(encoded))
	clone.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(encoded)), nil }
	// The original body is owned by this transport once RoundTrip is called.
	req.Body.Close()
	return t.base.RoundTrip(clone)
}
