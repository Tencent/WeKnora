package llm

import (
	"bufio"
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
	Model          string          `json:"model"`
	Messages       []Message       `json:"messages"`
	MaxTokens      int             `json:"max_tokens,omitempty"`
	Temperature    float64         `json:"temperature"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
	Stream         bool            `json:"stream,omitempty"`
}

type responseFormat struct {
	Type string `json:"type"`
}

type completionResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
}

type streamingCompletionResponse struct {
	Choices []struct {
		Delta Message `json:"delta"`
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
		Model:          c.cfg.Model,
		Temperature:    0,
		ResponseFormat: &responseFormat{Type: "json_object"},
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

// Stream emits OpenAI-compatible response deltas and returns their complete
// concatenation. Callers must only publish application data after validating a
// self-contained prefix of the accumulated response.
func (c *Client) Stream(ctx context.Context, prompt string, onDelta func(string) error) (string, error) {
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
		Model: c.cfg.Model, Temperature: 0, ResponseFormat: &responseFormat{Type: "json_object"}, Stream: true,
		Messages: []Message{{Role: "user", Content: prompt}}, MaxTokens: c.cfg.MaxTokens,
	})
	if err != nil {
		return "", fmt.Errorf("encode llm stream request: %w", err)
	}
	endpoint := strings.TrimRight(c.cfg.BaseURL, "/")
	if !strings.HasSuffix(endpoint, "/chat/completions") {
		endpoint += "/chat/completions"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	response, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("call llm stream: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode >= 400 {
		body, _ := io.ReadAll(response.Body)
		return "", fmt.Errorf("llm stream status %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	var output strings.Builder
	reader := bufio.NewScanner(response.Body)
	reader.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
	for reader.Scan() {
		line := strings.TrimSpace(reader.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			break
		}
		var event streamingCompletionResponse
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			return "", fmt.Errorf("decode llm stream event: %w", err)
		}
		for _, choice := range event.Choices {
			if choice.Delta.Content == "" {
				continue
			}
			output.WriteString(choice.Delta.Content)
			if onDelta != nil {
				if err := onDelta(choice.Delta.Content); err != nil {
					return "", err
				}
			}
		}
	}
	if err := reader.Err(); err != nil {
		return "", fmt.Errorf("read llm stream: %w", err)
	}
	if output.Len() == 0 {
		return "", fmt.Errorf("llm stream contains no content")
	}
	return output.String(), nil
}
