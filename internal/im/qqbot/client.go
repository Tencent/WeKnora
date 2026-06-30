package qqbot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

type Client struct {
	appID        string
	clientSecret string
	apiBaseURL   string
	gatewayURL   string
	httpClient   *http.Client

	mu          sync.Mutex
	accessToken string
	expiresAt   time.Time
}

func NewClient(appID, clientSecret, apiBaseURL, gatewayURL string) (*Client, error) {
	appID = strings.TrimSpace(appID)
	clientSecret = strings.TrimSpace(clientSecret)
	if appID == "" {
		return nil, fmt.Errorf("qqbot app_id is required")
	}
	if clientSecret == "" {
		return nil, fmt.Errorf("qqbot client_secret is required")
	}
	if apiBaseURL == "" {
		apiBaseURL = defaultAPIBaseURL
	}
	return &Client{
		appID:        appID,
		clientSecret: clientSecret,
		apiBaseURL:   strings.TrimRight(apiBaseURL, "/"),
		gatewayURL:   strings.TrimSpace(gatewayURL),
		httpClient:   &http.Client{Timeout: 15 * time.Second},
	}, nil
}

func (c *Client) GatewayURL(ctx context.Context) (string, error) {
	if c.gatewayURL != "" {
		return c.gatewayURL, nil
	}
	var result gatewayResponse
	if err := c.doJSON(ctx, http.MethodGet, defaultGatewayURL, nil, &result); err != nil {
		return "", err
	}
	if result.URL == "" {
		return "", fmt.Errorf("empty qqbot gateway url")
	}
	return result.URL, nil
}

func (c *Client) SendC2CMessage(ctx context.Context, openID, content, msgID string) error {
	path := fmt.Sprintf("/v2/users/%s/messages", openID)
	return c.sendText(ctx, path, content, msgID)
}

func (c *Client) SendGroupMessage(ctx context.Context, groupOpenID, content, msgID string) error {
	path := fmt.Sprintf("/v2/groups/%s/messages", groupOpenID)
	return c.sendText(ctx, path, content, msgID)
}

func (c *Client) sendText(ctx context.Context, path, content, msgID string) error {
	body := sendMessageRequest{
		Content: content,
		MsgType: 0,
		MsgID:   msgID,
		MsgSeq:  1,
	}
	return c.doJSON(ctx, http.MethodPost, c.apiBaseURL+path, body, nil)
}

func (c *Client) doJSON(ctx context.Context, method, url string, body any, out any) error {
	var reader *bytes.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(payload)
	} else {
		reader = bytes.NewReader(nil)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if !strings.Contains(url, "getAppAccessToken") {
		token, err := c.AccessToken(ctx)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "QQBot "+token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("qqbot api %s %s failed: %s", method, url, resp.Status)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode qqbot response: %w", err)
	}
	return nil
}

func (c *Client) AccessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	if c.accessToken != "" && time.Until(c.expiresAt) > time.Minute {
		token := c.accessToken
		c.mu.Unlock()
		return token, nil
	}
	c.mu.Unlock()

	body := map[string]string{
		"appId":        c.appID,
		"clientSecret": c.clientSecret,
	}
	var result tokenResponse
	if err := c.doJSON(ctx, http.MethodPost, appTokenURL, body, &result); err != nil {
		return "", err
	}
	if result.AccessToken == "" {
		return "", fmt.Errorf("empty qqbot access token: code=%d message=%s", result.Code, result.Message)
	}
	expiresIn := parseExpiresIn(result.ExpiresIn)

	c.mu.Lock()
	c.accessToken = result.AccessToken
	c.expiresAt = time.Now().Add(time.Duration(expiresIn) * time.Second)
	c.mu.Unlock()
	return result.AccessToken, nil
}

func parseExpiresIn(raw json.RawMessage) int {
	if len(raw) == 0 {
		return 7200
	}
	var number int
	if err := json.Unmarshal(raw, &number); err == nil && number > 0 {
		return number
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		var parsed int
		if _, err := fmt.Sscanf(text, "%d", &parsed); err == nil && parsed > 0 {
			return parsed
		}
	}
	return 7200
}
