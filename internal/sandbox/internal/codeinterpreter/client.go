package codeinterpreter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// ConnectionConfig holds the configuration needed to talk to the E2B API and
// to an individual sandbox.
type ConnectionConfig struct {
	// APIKey is the E2B API key. If empty the E2B_API_KEY environment variable
	// is used.
	APIKey string
	// AccessToken is an optional envd access token used to authenticate direct
	// requests to the sandbox.
	AccessToken string
	// TrafficAccessToken is an optional token used to bypass traffic controls.
	TrafficAccessToken string
	// Domain is the e2b domain (default: e2b.app). The E2B_DOMAIN env var is
	// used when empty.
	Domain string
	// Debug turns on debug-mode. When true, the SDK talks over http:// and
	// uses the sandbox host unchanged (useful for local development).
	Debug bool
	// RequestTimeout is the default HTTP request timeout.
	RequestTimeout time.Duration
	// HTTPClient is the underlying client used for all HTTP traffic. If nil a
	// sensible default is created.
	HTTPClient *http.Client
	// Headers lets callers inject additional headers on every request.
	Headers map[string]string
}

func (c *ConnectionConfig) init() {
	if c.APIKey == "" {
		c.APIKey = os.Getenv("E2B_API_KEY")
	}
	if c.AccessToken == "" {
		c.AccessToken = os.Getenv("E2B_ACCESS_TOKEN")
	}
	if c.Domain == "" {
		if d := os.Getenv("E2B_DOMAIN"); d != "" {
			c.Domain = d
		} else {
			c.Domain = DefaultDomain
		}
	}
	if !c.Debug {
		if v := os.Getenv("E2B_DEBUG"); v == "1" || strings.EqualFold(v, "true") {
			c.Debug = true
		}
	}
	if c.RequestTimeout == 0 {
		c.RequestTimeout = DefaultRequestTimeout * time.Second
	}
	if c.HTTPClient == nil {
		c.HTTPClient = &http.Client{}
	}
}

// APIBase returns the base URL for the E2B management API.
func (c *ConnectionConfig) APIBase() string {
	scheme := "https"
	if c.Debug {
		scheme = "http"
	}
	return fmt.Sprintf("%s://api.%s", scheme, c.Domain)
}

// do is a low level helper that performs an HTTP request against the E2B API
// and decodes the JSON response into `out` (if non-nil). It returns a typed
// error on non-2xx responses.
func (c *ConnectionConfig) do(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request body: %w", err)
		}
		reader = bytes.NewReader(b)
	}

	u := c.APIBase() + path
	req, err := http.NewRequestWithContext(ctx, method, u, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if c.APIKey != "" {
		req.Header.Set("X-API-Key", c.APIKey)
	}
	if c.AccessToken != "" {
		req.Header.Set("X-Access-Token", c.AccessToken)
	}
	for k, v := range c.Headers {
		req.Header.Set(k, v)
	}

	client := c.HTTPClient
	if client.Timeout == 0 && c.RequestTimeout > 0 {
		client = &http.Client{
			Timeout:   c.RequestTimeout,
			Transport: client.Transport,
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		var netErr interface{ Timeout() bool }
		if errors.As(err, &netErr) && netErr.Timeout() {
			return formatRequestTimeoutError()
		}
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return mapHTTPError(resp.StatusCode, string(body))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// mapHTTPError translates an HTTP status code into the proper SDK error type.
func mapHTTPError(status int, body string) error {
	msg := strings.TrimSpace(body)
	switch status {
	case http.StatusNotFound:
		return &NotFoundError{Message: msg}
	case http.StatusUnauthorized, http.StatusForbidden:
		return &AuthenticationError{Message: msg}
	case http.StatusTooManyRequests:
		return &RateLimitError{Message: msg}
	case http.StatusBadGateway, http.StatusGatewayTimeout:
		return &TimeoutError{Message: msg + ": This error is likely due to sandbox timeout. You can modify the sandbox timeout by passing 'Timeout' when starting the sandbox or by calling 'SetTimeout' on the sandbox with the desired timeout."}
	default:
		return &SandboxError{StatusCode: status, Message: msg}
	}
}
