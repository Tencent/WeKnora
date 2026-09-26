// Package client calls a WeKnora code plugin over the extension protocol. It
// is what WeKnora itself uses, and what the conformance suite tests plugins
// with.
package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

// Auth adds credentials to a request.
type Auth interface {
	Apply(req *http.Request, body []byte) error
}

// Bearer authenticates as the plugin host.
type Bearer string

// Apply implements Auth.
func (b Bearer) Apply(req *http.Request, _ []byte) error {
	req.Header.Set("Authorization", "Bearer "+string(b))
	return nil
}

// Signed authenticates to a remote plugin with its shared secret.
type Signed []byte

// Apply implements Auth.
func (s Signed) Apply(req *http.Request, body []byte) error {
	ts := time.Now().Unix()
	req.Header.Set(pluginapi.TimestampHeader, strconv.FormatInt(ts, 10))
	req.Header.Set(pluginapi.SignatureHeader, pluginapi.Sign(s, ts, body))
	return nil
}

// TransportError is a failure without a protocol answer: the plugin was
// unreachable, answered something that is not the protocol, or broke off a
// stream. It unwraps to a *pluginapi.Error (unavailable or internal) so
// callers can treat every failure alike.
type TransportError struct {
	Err *pluginapi.Error
}

func (e *TransportError) Error() string { return e.Err.Error() }
func (e *TransportError) Unwrap() error { return e.Err }

func transport(code pluginapi.ErrorCode, format string, args ...any) error {
	return &TransportError{Err: &pluginapi.Error{
		Code: code, Message: fmt.Sprintf(format, args...), Retryable: code == pluginapi.CodeUnavailable,
	}}
}

// Client calls one plugin process or service.
type Client struct {
	base string
	http *http.Client
	auth Auth
}

// ForHandshake reaches a host plugin at the address its handshake names.
// The returned client has no overall timeout: calls are bounded by their
// context.
func ForHandshake(h pluginapi.Handshake, auth Auth) *Client {
	if h.Network == "unix" {
		tr := &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", h.Address)
			},
			MaxIdleConnsPerHost: 16,
			IdleConnTimeout:     90 * time.Second,
		}
		return &Client{base: "http://plugin", http: &http.Client{Transport: tr}, auth: auth}
	}
	return New("http://"+h.Address, nil, auth)
}

// New reaches a plugin at an HTTP(S) base URL. A nil httpClient uses a
// client without an overall timeout.
func New(baseURL string, httpClient *http.Client, auth Auth) *Client {
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	return &Client{base: strings.TrimSuffix(baseURL, "/"), http: httpClient, auth: auth}
}

// Close releases idle connections.
func (c *Client) Close() { c.http.CloseIdleConnections() }

func (c *Client) do(ctx context.Context, method, path string, body []byte, requestID string) (*http.Response, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set(pluginapi.ProtocolHeader, pluginapi.ProtocolVersion)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if requestID != "" {
		req.Header.Set(pluginapi.RequestIDHeader, requestID)
	}
	if c.auth != nil {
		if err := c.auth.Apply(req, body); err != nil {
			return nil, err
		}
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, transport(pluginapi.CodeUnavailable, "%v", err)
	}
	return resp, nil
}

// Raw sends one request as the client would and returns the answer
// unread, whatever its status: for gateways that relay a plugin's answers,
// streams included. The caller closes the body.
func (c *Client) Raw(ctx context.Context, method, path string, body []byte, requestID string) (*http.Response, error) {
	return c.do(ctx, method, path, body, requestID)
}

// decodeError turns a non-2xx answer into a protocol error.
func decodeError(resp *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var body pluginapi.ErrorBody
	if json.Unmarshal(b, &body) == nil && body.Error.Code != "" {
		return &body.Error
	}
	code := pluginapi.CodeInternal
	if resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode == http.StatusBadGateway {
		code = pluginapi.CodeUnavailable
	}
	return transport(code, "plugin answered HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
}

// Get reads a GET endpoint (manifest, health) into out.
func (c *Client) Get(ctx context.Context, path string, out any) error {
	resp, err := c.do(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return decodeError(resp)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Manifest reads GET /v1/manifest.
func (c *Client) Manifest(ctx context.Context) (*pluginapi.Manifest, error) {
	var m pluginapi.Manifest
	return &m, c.Get(ctx, "/v1/manifest", &m)
}

// Health reads GET /v1/health.
func (c *Client) Health(ctx context.Context) error {
	var h pluginapi.Health
	if err := c.Get(ctx, "/v1/health", &h); err != nil {
		return err
	}
	if h.Status != "ok" {
		return &pluginapi.Error{Code: pluginapi.CodeUnavailable, Message: "plugin reports " + h.Status, Retryable: true}
	}
	return nil
}

func envelopeBody(env pluginapi.Envelope, input any) ([]byte, error) {
	if input != nil {
		raw, err := json.Marshal(input)
		if err != nil {
			return nil, err
		}
		env.Input = raw
	}
	return json.Marshal(env)
}

// Call posts an envelope and decodes the output into out (nil discards it).
func (c *Client) Call(ctx context.Context, path string, env pluginapi.Envelope, input, out any) error {
	body, err := envelopeBody(env, input)
	if err != nil {
		return err
	}
	resp, err := c.do(ctx, http.MethodPost, path, body, env.Context.RequestID)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return decodeError(resp)
	}
	var wrapped pluginapi.Output
	if err := json.NewDecoder(resp.Body).Decode(&wrapped); err != nil {
		return transport(pluginapi.CodeInternal, "decode plugin output: %v", err)
	}
	if out == nil || len(wrapped.Output) == 0 {
		return nil
	}
	return json.Unmarshal(wrapped.Output, out)
}

// MaxEventBytes bounds one NDJSON line (one fetched item).
const MaxEventBytes = 64 << 20

// Stream posts an envelope and hands each event to fn until the stream ends.
// It returns the "end" event's data, the "error" event as an error, or an
// error when the stream stops without either.
func (c *Client) Stream(
	ctx context.Context, path string, env pluginapi.Envelope, input any,
	fn func(pluginapi.Event) error,
) (json.RawMessage, error) {
	body, err := envelopeBody(env, input)
	if err != nil {
		return nil, err
	}
	resp, err := c.do(ctx, http.MethodPost, path, body, env.Context.RequestID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return nil, decodeError(resp)
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64<<10), MaxEventBytes)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var ev pluginapi.Event
		if err := json.Unmarshal(line, &ev); err != nil {
			return nil, transport(pluginapi.CodeInternal, "decode stream event: %v", err)
		}
		switch ev.Type {
		case pluginapi.EventEnd:
			return ev.Data, nil
		case pluginapi.EventError:
			if ev.Error == nil {
				return nil, &pluginapi.Error{Code: pluginapi.CodeInternal, Message: "plugin stream failed"}
			}
			return nil, ev.Error
		}
		if err := fn(ev); err != nil {
			return nil, err
		}
	}
	if err := sc.Err(); err != nil {
		return nil, transport(pluginapi.CodeUnavailable, "stream broken: %v", err)
	}
	return nil, transport(pluginapi.CodeUnavailable, "stream ended without an end event")
}
