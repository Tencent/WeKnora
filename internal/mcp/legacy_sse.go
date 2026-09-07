package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/mark3labs/mcp-go/client/transport"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
)

// legacySSETransport implements the pre-streamable HTTP MCP dialect used by
// servers that accept every JSON-RPC request as a POST to the configured URL
// and return the response as an SSE event. Unlike mcp-go's regular SSE
// transport, this dialect does not require a preliminary GET /sse endpoint.
type legacySSETransport struct {
	endpoint   string
	httpClient *http.Client
	headers    map[string]string

	oauthHandler *transport.OAuthHandler

	started atomic.Bool
	closed  atomic.Bool

	protocolVersion atomic.Value

	notificationMu      sync.RWMutex
	notificationHandler func(mcpgo.JSONRPCNotification)

	connectionLostMu sync.RWMutex
	connectionLost   func(error)
}

func newLegacySSETransport(endpoint string, httpClient *http.Client, headers map[string]string, oauthConfig transport.OAuthConfig, useOAuth bool) (*legacySSETransport, error) {
	if strings.TrimSpace(endpoint) == "" {
		return nil, errors.New("legacy SSE endpoint is required")
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	transportClient := &legacySSETransport{
		endpoint:   endpoint,
		httpClient: httpClient,
		headers:    make(map[string]string, len(headers)),
	}
	for key, value := range headers {
		transportClient.headers[key] = value
	}
	if useOAuth {
		oauthHandler := transport.NewOAuthHandler(oauthConfig)
		oauthHandler.SetBaseURL(endpoint)
		transportClient.oauthHandler = oauthHandler
	}
	return transportClient, nil
}

func (t *legacySSETransport) Start(context.Context) error {
	if t.closed.Load() {
		return errors.New("legacy SSE transport has been closed")
	}
	t.started.Store(true)
	return nil
}

func (t *legacySSETransport) SendRequest(ctx context.Context, request transport.JSONRPCRequest) (*transport.JSONRPCResponse, error) {
	if err := t.ensureStarted(); err != nil {
		return nil, err
	}

	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal legacy SSE request: %w", err)
	}
	response, err := t.doPost(ctx, payload, request.Header)
	if err != nil {
		return nil, err
	}

	return t.decodeResponse(response, request.ID)
}

func (t *legacySSETransport) SendNotification(ctx context.Context, notification mcpgo.JSONRPCNotification) error {
	if err := t.ensureStarted(); err != nil {
		return err
	}

	payload, err := json.Marshal(notification)
	if err != nil {
		return fmt.Errorf("failed to marshal legacy SSE notification: %w", err)
	}
	response, err := t.doPost(ctx, payload, nil)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	return nil
}

func (t *legacySSETransport) SetNotificationHandler(handler func(mcpgo.JSONRPCNotification)) {
	t.notificationMu.Lock()
	defer t.notificationMu.Unlock()
	t.notificationHandler = handler
}

func (t *legacySSETransport) SetProtocolVersion(version string) {
	t.protocolVersion.Store(version)
}

func (t *legacySSETransport) SetConnectionLostHandler(handler func(error)) {
	t.connectionLostMu.Lock()
	defer t.connectionLostMu.Unlock()
	t.connectionLost = handler
}

func (t *legacySSETransport) Close() error {
	if t.closed.Swap(true) {
		return nil
	}
	t.started.Store(false)
	return nil
}

func (t *legacySSETransport) GetSessionId() string { return "" }

func (t *legacySSETransport) ensureStarted() error {
	switch {
	case t.closed.Load():
		return errors.New("legacy SSE transport has been closed")
	case !t.started.Load():
		return errors.New("legacy SSE transport has not been started")
	default:
		return nil
	}
}

func (t *legacySSETransport) doPost(ctx context.Context, payload []byte, requestHeaders http.Header) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, t.endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("failed to create legacy SSE request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	if version := t.protocolVersion.Load(); version != nil {
		if value, ok := version.(string); ok && value != "" {
			request.Header.Set(transport.HeaderKeyProtocolVersion, value)
		}
	}
	for key, value := range t.headers {
		request.Header.Set(key, value)
	}
	for key, values := range requestHeaders {
		if _, exists := request.Header[key]; exists {
			continue
		}
		for _, value := range values {
			request.Header.Add(key, value)
		}
	}
	if t.oauthHandler != nil {
		authorization, err := t.oauthHandler.GetAuthorizationHeader(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get OAuth authorization header: %w", err)
		}
		request.Header.Set("Authorization", authorization)
	}

	response, err := t.httpClient.Do(request)
	if err != nil {
		t.notifyConnectionLost(err)
		return nil, fmt.Errorf("failed to send legacy SSE request: %w", err)
	}
	if response.StatusCode == http.StatusUnauthorized {
		defer response.Body.Close()
		if t.oauthHandler != nil {
			t.oauthHandler.HandleUnauthorizedResponse(response)
			return nil, &transport.OAuthAuthorizationRequiredError{
				Handler: t.oauthHandler,
			}
		}
		return nil, &transport.AuthorizationRequiredError{}
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		defer response.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		return nil, fmt.Errorf("legacy SSE request failed with status %d: %s", response.StatusCode, body)
	}
	return response, nil
}

func (t *legacySSETransport) decodeResponse(response *http.Response, expectedID mcpgo.RequestId) (*transport.JSONRPCResponse, error) {
	defer response.Body.Close()

	contentType, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if contentType != "text/event-stream" {
		body, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
		if err != nil {
			return nil, fmt.Errorf("failed to read legacy SSE response: %w", err)
		}
		return unmarshalLegacySSEResponse(body)
	}

	return t.readSSEResponse(response.Body, expectedID)
}

func (t *legacySSETransport) readSSEResponse(reader io.Reader, expectedID mcpgo.RequestId) (*transport.JSONRPCResponse, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 4<<20)

	var dataLines []string
	var rawLines []string
	flushEvent := func() (*transport.JSONRPCResponse, error) {
		if len(dataLines) == 0 {
			return nil, nil
		}
		payload := []byte(strings.Join(dataLines, "\n"))
		response, err := unmarshalLegacySSEResponse(payload)
		if err != nil {
			return nil, err
		}
		if response.ID.IsNil() {
			var notification mcpgo.JSONRPCNotification
			if err := json.Unmarshal(payload, &notification); err == nil {
				t.notifyNotification(notification)
			}
			return nil, nil
		}
		if response.ID.String() != expectedID.String() {
			return nil, nil
		}
		return response, nil
	}

	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		rawLines = append(rawLines, line)
		if line == "" {
			response, err := flushEvent()
			if err != nil {
				return nil, err
			}
			if response != nil {
				return response, nil
			}
			dataLines = nil
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if data, ok := strings.CutPrefix(line, "data:"); ok {
			dataLines = append(dataLines, strings.TrimPrefix(data, " "))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read legacy SSE response: %w", err)
	}
	if response, err := flushEvent(); err != nil || response != nil {
		return response, err
	}
	if len(dataLines) == 0 && len(rawLines) > 0 {
		if response, err := unmarshalLegacySSEResponse([]byte(strings.Join(rawLines, "\n"))); err == nil {
			return response, nil
		}
	}
	return nil, errors.New("legacy SSE response did not contain a JSON-RPC response")
}

func (t *legacySSETransport) notifyNotification(notification mcpgo.JSONRPCNotification) {
	t.notificationMu.RLock()
	handler := t.notificationHandler
	t.notificationMu.RUnlock()
	if handler != nil {
		handler(notification)
	}
}

func (t *legacySSETransport) notifyConnectionLost(err error) {
	t.connectionLostMu.RLock()
	handler := t.connectionLost
	t.connectionLostMu.RUnlock()
	if handler != nil {
		handler(err)
	}
}

func unmarshalLegacySSEResponse(body []byte) (*transport.JSONRPCResponse, error) {
	var response transport.JSONRPCResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("failed to decode legacy SSE JSON-RPC response: %w", err)
	}
	return &response, nil
}
