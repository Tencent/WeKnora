package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// Responses API wire types (OpenAI Responses, non-streaming text for #15;
// streaming/vision/tools land in #18/#19). Wayfinder map #9, build #15.

type responsesRequest struct {
	Model           string `json:"model"`
	Input           string `json:"input"`
	MaxOutputTokens int    `json:"max_output_tokens,omitempty"`
	Stream          bool   `json:"stream,omitempty"`
}

type responsesOutputContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type responsesOutputItem struct {
	Type    string                   `json:"type"`
	Role    string                   `json:"role,omitempty"`
	Content []responsesOutputContent `json:"content,omitempty"`
}

type responsesUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type responsesError struct {
	Message string `json:"message"`
	Type    string `json:"type,omitempty"`
}

type responsesResponse struct {
	ID     string                `json:"id,omitempty"`
	Object string                `json:"object,omitempty"`
	Status string                `json:"status,omitempty"`
	Model  string                `json:"model,omitempty"`
	Output []responsesOutputItem `json:"output,omitempty"`
	Usage  responsesUsage        `json:"usage,omitempty"`
	Error  *responsesError       `json:"error,omitempty"`
}

// responsesEndpoint resolves the Responses endpoint with a double-append guard.
func responsesEndpoint(baseURL string) string {
	return appendOnce(baseURL, "/responses")
}

// chatCompletionsEndpoint resolves the chat-completions endpoint with an
// egress guard: stored full-path rows (e.g. deepseek-v4-flash with a base_url
// already ending in /chat/completions) must not double-append.
func chatCompletionsEndpoint(baseURL string) string {
	return appendOnce(baseURL, "/chat/completions")
}

// appendOnce joins base and suffix without double-appending when base
// already ends with the suffix (case-insensitive).
func appendOnce(base, suffix string) string {
	trimmed := strings.TrimRight(base, "/")
	if strings.HasSuffix(strings.ToLower(trimmed), strings.ToLower(suffix)) {
		return trimmed
	}
	return trimmed + suffix
}

// buildResponsesInput flattens conversation messages into the single-string
// Responses input. #15 covers text only; images/tools ride #19.
func buildResponsesInput(messages []Message) string {
	var sb strings.Builder
	for _, m := range messages {
		text := m.Content
		if text == "" {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(m.Role + ": " + text)
	}
	return sb.String()
}

// parseResponsesBody converts a raw /responses payload into a ChatResponse.
// Envelope-valid but textless bodies (e.g. status:incomplete when a reasoning
// model exhausts max_output_tokens) are NOT errors: content is empty and
// FinishReason carries the status for the caller (#16 test policy) to judge.
func parseResponsesBody(body []byte) (*types.ChatResponse, error) {
	var env struct {
		Error *responsesError `json:"error,omitempty"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("decode responses envelope: %w", err)
	}
	if env.Error != nil && env.Error.Message != "" {
		return nil, fmt.Errorf("responses API error: %s", env.Error.Message)
	}
	var resp responsesResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode responses body: %w", err)
	}
	out := &types.ChatResponse{
		Usage: types.TokenUsage{
			PromptTokens:     resp.Usage.InputTokens,
			CompletionTokens: resp.Usage.OutputTokens,
			TotalTokens:      resp.Usage.InputTokens + resp.Usage.OutputTokens,
		},
	}
	var texts []string
	for _, item := range resp.Output {
		for _, c := range item.Content {
			if c.Type == "output_text" && c.Text != "" {
				texts = append(texts, c.Text)
			}
		}
	}
	out.Content = strings.Join(texts, "")
	switch resp.Status {
	case "completed":
		out.FinishReason = "stop"
	case "":
		out.FinishReason = "stop"
	default:
		out.FinishReason = resp.Status
	}
	return out, nil
}

// chatWithResponses performs a non-streaming Responses API call.
func (c *RemoteAPIChat) chatWithResponses(ctx context.Context, messages []Message, opts *ChatOptions) (*types.ChatResponse, error) {
	req := responsesRequest{
		Model: c.modelName,
		Input: buildResponsesInput(messages),
	}
	if opts != nil {
		if opts.MaxCompletionTokens > 0 {
			req.MaxOutputTokens = opts.MaxCompletionTokens
		} else if opts.MaxTokens > 0 {
			req.MaxOutputTokens = opts.MaxTokens
		}
	}
	jsonData, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal responses request: %w", err)
	}
	endpoint := responsesEndpoint(c.baseURL)
	if err := secutils.ValidateURLForSSRF(endpoint); err != nil {
		return nil, fmt.Errorf("endpoint SSRF check failed: %w", err)
	}
	logger.Infof(ctx, "[LLM Request] Responses, endpoint=%s, model=%s, raw HTTP request:\n%s",
		endpoint, c.modelName, secutils.CompactImageDataURLForLog(string(jsonData)))

	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	c.adapter.Auth(httpReq, c.authCreds(), jsonData)
	secutils.ApplyCustomHeaders(httpReq, c.customHeaders)

	resp, err := rawHTTPClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(body))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	result, err := parseResponsesBody(body)
	if err != nil {
		return nil, err
	}
	logUsage(ctx, c.modelName, &result.Usage)
	return result, nil
}
