package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/custom/config"
)

type Client struct {
	cfg  config.LLMConfig
	http *http.Client
}

func (c *Client) Model() string { return c.cfg.Model }

func (c *Client) PromptVersion() string { return c.cfg.PromptVersion }

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type completionRequest struct {
	Model     string    `json:"model"`
	Messages  []Message `json:"messages"`
	MaxTokens int       `json:"max_tokens,omitempty"`
}

type completionResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
}

func NewClient(cfg config.LLMConfig) *Client {
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 3 * time.Minute
	}
	return &Client{cfg: cfg, http: &http.Client{Timeout: timeout}}
}

func (c *Client) Complete(ctx context.Context, prompt string) (string, error) {
	if strings.TrimSpace(c.cfg.BaseURL) == "" {
		return "", fmt.Errorf("custom llm base url 未配置")
	}
	if strings.TrimSpace(c.cfg.APIKey) == "" {
		return "", fmt.Errorf("custom llm api key 未配置")
	}
	if strings.TrimSpace(c.cfg.Model) == "" {
		return "", fmt.Errorf("custom llm model 未配置")
	}
	if strings.TrimSpace(prompt) == "" {
		return "", fmt.Errorf("llm prompt 不能为空")
	}

	body, err := json.Marshal(completionRequest{
		Model: c.cfg.Model,
		Messages: []Message{{
			Role:    "user",
			Content: prompt,
		}},
		MaxTokens: c.cfg.MaxTokens,
	})
	if err != nil {
		return "", fmt.Errorf("encode llm request: %w", err)
	}
	endpoint := strings.TrimRight(c.cfg.BaseURL, "/")
	if !strings.HasSuffix(endpoint, "/chat/completions") {
		endpoint += "/chat/completions"
	}
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		req, requestBuildErr := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if requestBuildErr != nil {
			return "", requestBuildErr
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
		response, requestErr := c.http.Do(req)
		if requestErr != nil {
			lastErr = fmt.Errorf("call llm: %w", requestErr)
		} else {
			responseBody, readErr := io.ReadAll(response.Body)
			response.Body.Close()
			if readErr != nil {
				lastErr = fmt.Errorf("read llm response: %w", readErr)
			} else if response.StatusCode >= 500 {
				lastErr = fmt.Errorf("llm status %d: %s", response.StatusCode, strings.TrimSpace(string(responseBody)))
			} else if response.StatusCode >= 400 {
				return "", fmt.Errorf("llm status %d: %s", response.StatusCode, strings.TrimSpace(string(responseBody)))
			} else {
				var result completionResponse
				if err := json.Unmarshal(responseBody, &result); err != nil {
					return "", fmt.Errorf("decode llm response: %w", err)
				}
				if len(result.Choices) == 0 || strings.TrimSpace(result.Choices[0].Message.Content) == "" {
					return "", fmt.Errorf("llm response contains no content")
				}
				return result.Choices[0].Message.Content, nil
			}
		}
		if attempt < 3 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(time.Duration(attempt) * 200 * time.Millisecond):
			}
		}
	}
	return "", lastErr
}
