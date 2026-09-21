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
	if err := secutils.ValidateURLForSSRF(url); err != nil {
		return nil, nil, fmt.Errorf("endpoint SSRF check failed: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	if e.Auth != nil {
		e.Auth(req, data)
	}
	// User headers are applied last but the helper skips reserved names so
	// they cannot clobber auth or content negotiation.
	secutils.ApplyCustomHeaders(req, e.Headers)
	return req, data, nil
}

// Do sends the request and returns the response. Non-2xx responses are
// turned into an error carrying the body so callers surface vendor messages.
func (e Endpoint) Do(req *http.Request) (*http.Response, error) {
	resp, err := e.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		_ = resp.Body.Close()
		return nil, &HTTPError{StatusCode: resp.StatusCode, Body: string(body)}
	}
	return resp, nil
}

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
