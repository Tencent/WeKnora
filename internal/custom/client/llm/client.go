package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

const (
	streamFirstByteTimeout = 90 * time.Second
	streamIdleTimeout      = 120 * time.Second
	streamTotalTimeout     = 10 * time.Minute
)

// CompleteWithSystem sends an explicit system instruction separately from the
// user payload. Some reasoning models treat a concatenated user prompt as a
// normal conversation and ignore its output contract.
func (c *Client) CompleteWithSystem(ctx context.Context, systemPrompt, prompt string) (string, error) {
	return c.complete(ctx, []Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: prompt},
	})
}

func (c *Client) Model() string { return c.cfg.Model }

func (c *Client) PromptVersion() string { return c.cfg.PromptVersion }

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type completionRequest struct {
	Model               string          `json:"model"`
	Messages            []Message       `json:"messages"`
	MaxTokens           int             `json:"max_tokens,omitempty"`
	MaxCompletionTokens int             `json:"max_completion_tokens,omitempty"`
	Temperature         float64         `json:"temperature"`
	ResponseFormat      *responseFormat `json:"response_format,omitempty"`
	Thinking            *thinkingConfig `json:"thinking,omitempty"`
	Stream              bool            `json:"stream,omitempty"`
}

type responseFormat struct {
	Type string `json:"type"`
}

type thinkingConfig struct {
	Type string `json:"type"`
}

type completionResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
}

type streamingCompletionResponse struct {
	Choices []struct {
		Delta        Message `json:"delta"`
		FinishReason string  `json:"finish_reason"`
	} `json:"choices"`
}

type IncompleteOutputError struct {
	Reason       string
	FinishReason string
	OutputBytes  int
	Attempts     int
}

func (e *IncompleteOutputError) Error() string {
	attempts := e.Attempts
	if attempts <= 0 {
		attempts = 1
	}
	if e.FinishReason != "" {
		return fmt.Sprintf("llm output incomplete: %s (finish_reason=%s, output_bytes=%d, attempts=%d)", e.Reason, e.FinishReason, e.OutputBytes, attempts)
	}
	return fmt.Sprintf("llm output incomplete: %s (output_bytes=%d, attempts=%d)", e.Reason, e.OutputBytes, attempts)
}

func (e *IncompleteOutputError) IncompleteOutput() bool { return true }

type TemporaryError struct {
	Err      error
	Attempts int
}

func (e *TemporaryError) Error() string {
	if e.Attempts > 0 {
		return fmt.Sprintf("%v (attempts=%d)", e.Err, e.Attempts)
	}
	return e.Err.Error()
}
func (e *TemporaryError) Unwrap() error   { return e.Err }
func (e *TemporaryError) Temporary() bool { return true }

type ConnectionClosedError struct {
	Err error
}

func (e *ConnectionClosedError) Error() string {
	return fmt.Sprintf("llm stream connection closed: %v", e.Err)
}

func (e *ConnectionClosedError) Unwrap() error          { return e.Err }
func (e *ConnectionClosedError) Temporary() bool        { return true }
func (e *ConnectionClosedError) ConnectionClosed() bool { return true }

type StreamTimeoutError struct {
	Phase string
}

func (e *StreamTimeoutError) Error() string {
	return fmt.Sprintf("llm stream %s timeout", e.Phase)
}

func (e *StreamTimeoutError) StreamTimeoutPhase() string { return e.Phase }

type InvalidOutputError struct {
	Reason      string
	OutputBytes int
}

func (e *InvalidOutputError) Error() string {
	return fmt.Sprintf("llm output invalid: %s (output_bytes=%d)", e.Reason, e.OutputBytes)
}

func (e *InvalidOutputError) InvalidOutput() bool { return true }

func NewClient(cfg config.LLMConfig) *Client {
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 3 * time.Minute
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = streamFirstByteTimeout
	return &Client{cfg: cfg, http: &http.Client{Timeout: timeout, Transport: transport}}
}

func (c *Client) Complete(ctx context.Context, prompt string) (string, error) {
	return c.complete(ctx, []Message{{Role: "user", Content: prompt}})
}

// CompleteJSON requests a complete JSON response without model reasoning.
// Training orchestration uses this path because partial reasoning is not an
// application result and must never reach its strict JSON validator.
func (c *Client) CompleteJSON(ctx context.Context, prompt string) (string, error) {
	content, err := c.stream(ctx, prompt, nil, streamOptions{disableThinking: true, requireCompleted: true})
	if err != nil {
		var incomplete *IncompleteOutputError
		if errors.As(err, &incomplete) {
			incomplete.Attempts = 1
		}
		return "", err
	}
	content, reason, err := normalizeCompletedJSON(content)
	if err != nil {
		return "", &InvalidOutputError{Reason: err.Error(), OutputBytes: len(content)}
	}
	if reason != "" {
		return "", &IncompleteOutputError{Reason: reason, OutputBytes: len(content), Attempts: 1}
	}
	return content, nil
}

func normalizeCompletedJSON(raw string) (content, incompleteReason string, err error) {
	content = strings.TrimSpace(raw)
	for {
		before := content
		for _, tag := range []string{"think", "analysis"} {
			opening, closing := "<"+tag+">", "</"+tag+">"
			lower := strings.ToLower(content)
			if !strings.HasPrefix(lower, opening) {
				continue
			}
			end := strings.Index(lower, closing)
			if end < 0 {
				return content, "stream completed before closing <" + tag + ">", nil
			}
			content = strings.TrimSpace(content[end+len(closing):])
			break
		}
		for _, tag := range []string{"final", "answer"} {
			opening, closing := "<"+tag+">", "</"+tag+">"
			lower := strings.ToLower(content)
			if !strings.HasPrefix(lower, opening) {
				continue
			}
			if !strings.HasSuffix(lower, closing) {
				return content, "stream completed before closing <" + tag + ">", nil
			}
			content = strings.TrimSpace(content[len(opening) : len(content)-len(closing)])
			break
		}
		if strings.HasPrefix(content, "```") {
			lines := strings.Split(content, "\n")
			if len(lines) < 3 || strings.TrimSpace(lines[len(lines)-1]) != "```" {
				return content, "stream completed before closing JSON code fence", nil
			}
			content = strings.TrimSpace(strings.Join(lines[1:len(lines)-1], "\n"))
		}
		if content == before {
			break
		}
	}
	if json.Valid([]byte(content)) {
		return content, "", nil
	}
	var value any
	decodeErr := json.NewDecoder(strings.NewReader(content)).Decode(&value)
	if errors.Is(decodeErr, io.ErrUnexpectedEOF) || strings.Contains(fmt.Sprint(decodeErr), "unexpected end of JSON input") {
		return content, "stream completed before one full JSON value", nil
	}
	if decodeErr == nil {
		return content, "", fmt.Errorf("stream completed with trailing non-JSON content")
	}
	return content, "", fmt.Errorf("stream completed with invalid JSON syntax")
}

func (c *Client) complete(ctx context.Context, messages []Message) (string, error) {
	if strings.TrimSpace(c.cfg.BaseURL) == "" {
		return "", fmt.Errorf("custom llm base url 未配置")
	}
	if strings.TrimSpace(c.cfg.APIKey) == "" {
		return "", fmt.Errorf("custom llm api key 未配置")
	}
	if strings.TrimSpace(c.cfg.Model) == "" {
		return "", fmt.Errorf("custom llm model 未配置")
	}
	if len(messages) == 0 {
		return "", fmt.Errorf("llm messages 不能为空")
	}
	for _, message := range messages {
		if strings.TrimSpace(message.Content) == "" {
			return "", fmt.Errorf("llm message 不能为空")
		}
	}

	body, err := json.Marshal(c.newCompletionRequest(messages, false))
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
	return c.stream(ctx, prompt, onDelta, streamOptions{})
}

type streamOptions struct {
	disableThinking  bool
	requireCompleted bool
}

func (c *Client) stream(ctx context.Context, prompt string, onDelta func(string) error, options streamOptions) (string, error) {
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
	request := c.newCompletionRequest([]Message{{Role: "user", Content: prompt}}, true)
	if options.disableThinking && c.isMiniMaxModel() {
		request.Thinking = &thinkingConfig{Type: "disabled"}
	}
	body, err := json.Marshal(request)
	if err != nil {
		return "", fmt.Errorf("encode llm stream request: %w", err)
	}
	endpoint := strings.TrimRight(c.cfg.BaseURL, "/")
	if !strings.HasSuffix(endpoint, "/chat/completions") {
		endpoint += "/chat/completions"
	}
	requestCtx, cancel := context.WithTimeout(ctx, streamTotalTimeout)
	defer cancel()

	// Streaming callers with a business deadline need that deadline to govern
	// the whole response. http.Client.Timeout would otherwise terminate a
	// healthy long-running stream even while response chunks are arriving.
	streamHTTP := *c.http
	streamHTTP.Timeout = 0
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	response, err := streamHTTP.Do(req)
	if err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return "", &ConnectionClosedError{Err: err}
		}
		return "", &TemporaryError{Err: fmt.Errorf("call llm stream: %w", err)}
	}
	defer response.Body.Close()
	if response.StatusCode >= 400 {
		body, _ := io.ReadAll(response.Body)
		statusErr := fmt.Errorf("llm stream status %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
		if response.StatusCode >= 500 {
			return "", &TemporaryError{Err: statusErr}
		}
		return "", statusErr
	}
	var output strings.Builder
	doneMarkerSeen := false
	finishReason := ""
	type scanResult struct {
		line string
		err  error
		done bool
	}
	scans := make(chan scanResult)
	go func() {
		reader := bufio.NewScanner(response.Body)
		reader.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
		for reader.Scan() {
			select {
			case scans <- scanResult{line: reader.Text()}:
			case <-requestCtx.Done():
				return
			}
		}
		select {
		case scans <- scanResult{err: reader.Err(), done: true}:
		case <-requestCtx.Done():
		}
	}()
	timer := time.NewTimer(streamFirstByteTimeout)
	defer timer.Stop()
	firstByteSeen := false
streamLoop:
	for {
		select {
		case <-requestCtx.Done():
			_ = response.Body.Close()
			if errors.Is(requestCtx.Err(), context.DeadlineExceeded) {
				return "", &StreamTimeoutError{Phase: "total"}
			}
			return "", requestCtx.Err()
		case <-timer.C:
			_ = response.Body.Close()
			phase := "first_byte"
			if firstByteSeen {
				phase = "idle"
			}
			return "", &StreamTimeoutError{Phase: phase}
		case scan := <-scans:
			if scan.done {
				if scan.err != nil {
					return "", &ConnectionClosedError{Err: scan.err}
				}
				break streamLoop
			}
			firstByteSeen = true
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(streamIdleTimeout)
			line := strings.TrimSpace(scan.line)
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if payload == "[DONE]" {
				doneMarkerSeen = true
				break streamLoop
			}
			var event streamingCompletionResponse
			if err := json.Unmarshal([]byte(payload), &event); err != nil {
				if options.requireCompleted {
					return "", &IncompleteOutputError{
						Reason:      "stream event ended before valid JSON was received",
						OutputBytes: output.Len(),
					}
				}
				return "", fmt.Errorf("decode llm stream event: %w", err)
			}
			for _, choice := range event.Choices {
				if choice.FinishReason != "" {
					finishReason = choice.FinishReason
				}
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
	}
	if options.requireCompleted && !doneMarkerSeen && finishReason == "" {
		return "", &IncompleteOutputError{Reason: "stream ended before completion marker", FinishReason: finishReason, OutputBytes: output.Len()}
	}
	if options.requireCompleted && finishReason == "length" {
		return "", &IncompleteOutputError{Reason: "output token limit reached", FinishReason: finishReason, OutputBytes: output.Len()}
	}
	if output.Len() == 0 {
		return "", fmt.Errorf("llm stream contains no content")
	}
	return output.String(), nil
}

func (c *Client) isMiniMaxModel() bool {
	if c == nil {
		return false
	}
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(c.cfg.Model)), "minimax-")
}

func (c *Client) newCompletionRequest(messages []Message, stream bool) completionRequest {
	request := completionRequest{
		Model:          c.cfg.Model,
		Temperature:    0,
		ResponseFormat: c.jsonResponseFormat(),
		Stream:         stream,
		Messages:       messages,
	}
	if c.isMiniMaxModel() {
		request.MaxCompletionTokens = c.cfg.MaxTokens
	} else {
		request.MaxTokens = c.cfg.MaxTokens
	}
	return request
}

func (c *Client) jsonResponseFormat() *responseFormat {
	if c == nil || c.isMiniMaxModel() {
		return nil
	}
	return &responseFormat{Type: "json_object"}
}
