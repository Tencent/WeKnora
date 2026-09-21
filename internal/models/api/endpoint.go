package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// AuthFunc stamps credentials onto an outbound request. body is the exact
// bytes being sent so signing vendors can hash them.
type AuthFunc func(req *http.Request, body []byte)

// BearerAuth is the default: Authorization: Bearer <key>.
func BearerAuth(apiKey string) AuthFunc {
	return func(req *http.Request, _ []byte) {
		if apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+apiKey)
		}
	}
}

// HeaderAuth writes the key under an arbitrary header (api-key for Azure,
// x-api-key for Anthropic, x-goog-api-key for Gemini).
func HeaderAuth(header, apiKey string) AuthFunc {
	return func(req *http.Request, _ []byte) {
		if apiKey != "" {
			req.Header.Set(header, apiKey)
		}
	}
}

// NoAuth sends no credentials at all (local deployments).
func NoAuth() AuthFunc { return func(*http.Request, []byte) {} }

// Endpoint is everything a protocol client needs to reach one model.
type Endpoint struct {
	// BaseURL is the vendor root without a trailing slash.
	BaseURL string
	// URL, when set, is the complete request URL and overrides the protocol
	// default of BaseURL + protocol path. Vendors with non-standard paths
	// (Azure deployments, WeKnora Cloud) set it.
	URL string
	// Query is appended to the final URL (Azure api-version).
	Query map[string]string
	// Model is the name sent on the wire; ModelID is WeKnora's own identifier.
	Model   string
	ModelID string
	// Auth stamps credentials; Headers are static extra headers (vendor
	// betas, user-configured custom headers). User headers never override
	// the protocol's own reserved headers.
	Auth    AuthFunc
	Headers map[string]string
	// Client is the HTTP client; nil uses the shared SSRF-safe client.
	Client *http.Client
}

// Resolve builds the request URL from the endpoint description.
func (e Endpoint) Resolve(defaultPath string) string {
	target := e.URL
	if target == "" {
		target = strings.TrimRight(e.BaseURL, "/") + defaultPath
	}
	if len(e.Query) == 0 {
		return target
	}
	var b strings.Builder
	b.WriteString(target)
	sep := "?"
	if strings.Contains(target, "?") {
		sep = "&"
	}
	for k, v := range e.Query {
		b.WriteString(sep)
		b.WriteString(k)
		b.WriteString("=")
		b.WriteString(v)
		sep = "&"
	}
	return b.String()
}

func (e Endpoint) httpClient() *http.Client {
	if e.Client != nil {
		return e.Client
	}
	return HTTPClient
}

// NewRequest prepares an authenticated JSON POST. The body is marshalled
// once so signing vendors see exactly what is sent.
func (e Endpoint) NewRequest(ctx context.Context, url string, body any, stream bool) (*http.Request, []byte, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal request: %w", err)
	}
	req, err := e.newPost(ctx, url, data, "application/json")
	if err != nil {
		return nil, nil, err
	}
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	return req, data, nil
}

// newPost prepares an authenticated POST of an already-encoded body.
func (e Endpoint) newPost(ctx context.Context, url string, data []byte, contentType string) (*http.Request, error) {
	if err := secutils.ValidateURLForSSRF(url); err != nil {
		return nil, fmt.Errorf("endpoint SSRF check failed: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)
	if e.Auth != nil {
		e.Auth(req, data)
	}
	// User headers are applied last but the helper skips reserved names so
	// they cannot clobber auth or content negotiation.
	secutils.ApplyCustomHeaders(req, e.Headers)
	return req, nil
}

// Do sends the request and returns the response. Non-2xx responses are
// turned into an error carrying the body so callers surface vendor messages.
func (e Endpoint) Do(req *http.Request) (*http.Response, error) {
	resp, err := e.httpClient().Do(req)
	if err != nil {
		return nil, &TransportError{Op: "send request", Err: err}
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		_ = resp.Body.Close()
		return nil, &HTTPError{StatusCode: resp.StatusCode, Body: string(body)}
	}
	return resp, nil
}

// PostJSON sends one authenticated JSON request and decodes the reply into
// out. It is the whole round-trip for the non-streaming protocols (rerank,
// embeddings), which have no SSE to assemble.
//
// It never returns a nil error with nothing decoded: every failure path —
// marshalling, SSRF, transport, non-2xx, decoding — returns an error, so a
// caller cannot mistake an empty result for a successful empty answer.
func (e Endpoint) PostJSON(ctx context.Context, url string, body, out any) error {
	req, _, err := e.NewRequest(ctx, url, body, false)
	if err != nil {
		return err
	}
	// No LogRequest here: the chat protocols log their own bodies because a
	// prompt is what an operator debugs, while a rerank body is a query plus
	// every candidate chunk. The rerank layer logs a truncated line at Debug
	// instead, so this would have duplicated it at Info.
	return e.roundTrip(req, out)
}

// roundTrip sends a prepared request and decodes a 2xx reply into out.
func (e Endpoint) roundTrip(req *http.Request, out any) error {
	resp, err := e.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		// The connection broke before the reply arrived whole: nothing was
		// received, so this is the network failing, not the vendor answering.
		return &TransportError{Op: "read response", Err: err}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// TransportError is a request that never got a whole answer: DNS, connect,
// TLS, a reset or a timeout, while sending or while reading the reply. It is
// the only failure worth sending again.
type TransportError struct {
	// Op is the phase that failed: "send request" or "read response".
	Op  string
	Err error
}

func (e *TransportError) Error() string { return e.Op + ": " + e.Err.Error() }
func (e *TransportError) Unwrap() error { return e.Err }

// HTTPError is a non-2xx vendor reply.
type HTTPError struct {
	StatusCode int
	Body       string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("API request failed with status %d: %s", e.StatusCode, e.Body)
}

// LogRequest emits the standard request log line with image payloads
// compacted so data URIs do not flood the log.
func LogRequest(ctx context.Context, url, model string, body []byte, stream bool) {
	pretty := body
	var buf bytes.Buffer
	if err := json.Indent(&buf, body, "", "  "); err == nil {
		pretty = buf.Bytes()
	}
	logger.Infof(ctx, "[LLM Request] endpoint=%s, model=%s, stream=%v, request:\n%s",
		url, model, stream, secutils.CompactImageDataURLForLog(string(pretty)))
}
