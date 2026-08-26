package xquik

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
)

const (
	requestTimeout  = 30 * time.Second
	maxResponseSize = 16 * 1024 * 1024
	maxErrorSize    = 32 * 1024
	userAgent       = "WeKnora-Xquik/1.0"
)

type api interface {
	validate(context.Context) error
	search(context.Context, searchRequest) (searchPage, error)
}

type client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

func newClient(apiKey string) *client {
	return &client{
		baseURL:    defaultBaseURL,
		apiKey:     apiKey,
		httpClient: datasource.NewConnectorHTTPClient(requestTimeout),
	}
}

func (c *client) validate(ctx context.Context) error {
	var response struct {
		Balance json.RawMessage `json:"balance"`
	}
	if err := c.getJSON(ctx, "/credits", nil, &response); err != nil {
		return fmt.Errorf("validate Xquik credentials: %w", err)
	}
	if len(response.Balance) == 0 {
		return errors.New("validate Xquik credentials: balance missing from response")
	}
	return nil
}

func (c *client) search(ctx context.Context, input searchRequest) (searchPage, error) {
	query := url.Values{}
	query.Set("q", input.Query)
	query.Set("queryType", "Latest")
	query.Set("limit", strconv.Itoa(input.Limit))
	if input.Cursor != "" {
		query.Set("cursor", input.Cursor)
	}
	if !input.SinceTime.IsZero() {
		query.Set("sinceTime", input.SinceTime.UTC().Format(time.RFC3339Nano))
	}
	if !input.UntilTime.IsZero() {
		query.Set("untilTime", input.UntilTime.UTC().Format(time.RFC3339Nano))
	}

	var page searchPage
	if err := c.getJSON(ctx, "/x/tweets/search", query, &page); err != nil {
		return searchPage{}, fmt.Errorf("search X posts: %w", err)
	}
	return page, nil
}

func (c *client) getJSON(ctx context.Context, path string, query url.Values, target interface{}) error {
	endpoint := strings.TrimRight(c.baseURL, "/") + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("x-api-key", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	limit := int64(maxResponseSize)
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		limit = maxErrorSize
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if int64(len(body)) > limit {
		return fmt.Errorf("response exceeds %d bytes", limit)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		message := decodeAPIError(resp.StatusCode, body).Error()
		if c.apiKey != "" {
			message = strings.ReplaceAll(message, c.apiKey, "[redacted]")
		}
		return errors.New(message)
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

type apiErrorBody struct {
	Error   json.RawMessage `json:"error"`
	Message string          `json:"message"`
}

func decodeAPIError(status int, body []byte) error {
	var payload apiErrorBody
	_ = json.Unmarshal(body, &payload)
	code := truncateRunes(sanitizeAPIMessage(decodeErrorCode(payload.Error)), 64)
	message := sanitizeAPIMessage(payload.Message)
	if code == "" {
		code = http.StatusText(status)
	}
	if message == "" {
		return fmt.Errorf("Xquik API returned HTTP %d (%s)", status, code)
	}
	return fmt.Errorf("Xquik API returned HTTP %d (%s): %s", status, code, message)
}

func sanitizeAPIMessage(message string) string {
	message = strings.Map(func(char rune) rune {
		if char < 0x20 || char == 0x7f {
			return ' '
		}
		return char
	}, message)
	return truncateRunes(strings.Join(strings.Fields(message), " "), 512)
}

func decodeErrorCode(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.TrimSpace(text)
	}
	var object struct {
		Code string `json:"code"`
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &object); err != nil {
		return ""
	}
	if strings.TrimSpace(object.Code) != "" {
		return strings.TrimSpace(object.Code)
	}
	return strings.TrimSpace(object.Type)
}
