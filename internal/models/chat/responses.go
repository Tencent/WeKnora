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
	modelutils "github.com/Tencent/WeKnora/internal/models/utils"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// Responses API wire types (OpenAI Responses, non-streaming text for #15;
// streaming/vision/tools land in #18/#19). Wayfinder map #9, build #15.

type responsesRequest struct {
	Model           string              `json:"model"`
	Input           any                 `json:"input"`
	MaxOutputTokens int                 `json:"max_output_tokens,omitempty"`
	Reasoning       *responsesReasoning `json:"reasoning,omitempty"`
	Tools           []responsesTool     `json:"tools,omitempty"`
	ToolChoice      any                 `json:"tool_choice,omitempty"`
	Stream          bool                `json:"stream,omitempty"`
}

// responsesReasoning carries the effort selector (none/minimal/low/medium/
// high, default medium) from ExtraConfig key reasoning_effort (#16).
type responsesReasoning struct {
	Effort string `json:"effort,omitempty"`
}

// responsesEffortLevels is the v1 allowlist; xhigh stays out until asked for.
var responsesEffortLevels = map[string]bool{
	"none": true, "minimal": true, "low": true, "medium": true, "high": true,
}

// resolveResponsesEffort reads ExtraConfig reasoning_effort, normalizing
// case/space; unknown or empty falls back to medium.
func resolveResponsesEffort(extra map[string]string) string {
	effort := ""
	if extra != nil {
		effort = strings.ToLower(strings.TrimSpace(extra["reasoning_effort"]))
	}
	if !responsesEffortLevels[effort] {
		return "medium"
	}
	return effort
}

type responsesOutputContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type responsesOutputItem struct {
	Type      string                   `json:"type"`
	Role      string                   `json:"role,omitempty"`
	Content   []responsesOutputContent `json:"content,omitempty"`
	ID        string                   `json:"id,omitempty"`
	CallID    string                   `json:"call_id,omitempty"`
	Name      string                   `json:"name,omitempty"`
	Arguments string                   `json:"arguments,omitempty"`
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
	return modelutils.AppendPathOnce(baseURL, "/responses")
}

// chatCompletionsEndpoint resolves the chat-completions endpoint with an
// egress guard: stored full-path rows (e.g. deepseek-v4-flash with a base_url
// already ending in /chat/completions) must not double-append.
func chatCompletionsEndpoint(baseURL string) string {
	return modelutils.AppendPathOnce(baseURL, "/chat/completions")
}

// newResponsesHTTPRequest builds the shared POST setup for Responses calls:
// marshal, endpoint + SSRF check, auth and custom headers.
func (c *RemoteAPIChat) newResponsesHTTPRequest(ctx context.Context, req responsesRequest) (*http.Request, []byte, error) {
	jsonData, err := json.Marshal(req)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal responses request: %w", err)
	}
	endpoint := responsesEndpoint(c.baseURL)
	if err := secutils.ValidateURLForSSRF(endpoint); err != nil {
		return nil, nil, fmt.Errorf("endpoint SSRF check failed: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	c.adapter.Auth(httpReq, c.authCreds(), jsonData)
	secutils.ApplyCustomHeaders(httpReq, c.customHeaders)
	return httpReq, jsonData, nil
}

// buildResponsesInput flattens conversation messages into the single-string
// Responses input. Text-only path; structured content rides
// buildResponsesInputValue (#19).
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

// responsesTool maps a WeKnora function tool onto a Responses function tool.
type responsesTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// buildResponsesTools maps ChatOptions tools + tool_choice. Nil opts (or no
// tools) yields no fields.
func buildResponsesTools(opts *ChatOptions) ([]responsesTool, any) {
	if opts == nil || len(opts.Tools) == 0 {
		return nil, nil
	}
	tools := make([]responsesTool, 0, len(opts.Tools))
	for _, tool := range opts.Tools {
		toolType := tool.Type
		if toolType == "" {
			toolType = "function"
		}
		tools = append(tools, responsesTool{
			Type:        toolType,
			Name:        tool.Function.Name,
			Description: tool.Function.Description,
			Parameters:  tool.Function.Parameters,
		})
	}
	var choice any
	switch opts.ToolChoice {
	case "":
		choice = nil
	case "none", "required", "auto":
		choice = opts.ToolChoice
	default:
		choice = map[string]any{"type": "function", "name": opts.ToolChoice}
	}
	return tools, choice
}

// responsesInputContent is one content part inside a structured input item.
type responsesInputContent struct {
	Type     string `json:"type"` // input_text | input_image
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
}

// responsesInputItem is one entry of a structured Responses input array.
type responsesInputItem struct {
	Type      string                  `json:"type"` // message | function_call | function_call_output
	Role      string                  `json:"role,omitempty"`
	Content   []responsesInputContent `json:"content,omitempty"`
	Name      string                  `json:"name,omitempty"`
	CallID    string                  `json:"call_id,omitempty"`
	Arguments string                  `json:"arguments,omitempty"`
	Output    string                  `json:"output,omitempty"`
}

// needsStructuredInput reports whether any message carries non-text content.
func needsStructuredInput(messages []Message) bool {
	for _, m := range messages {
		if len(m.ToolCalls) > 0 || m.Role == "tool" {
			return true
		}
		for _, part := range m.MultiContent {
			if part.Type == "image_url" && part.ImageURL != nil {
				return true
			}
		}
		if len(m.Images) > 0 {
			return true
		}
	}
	return false
}

// buildResponsesInputValue returns the Responses input: a plain string for
// text-only turns (wire shape unchanged), otherwise a structured item array
// carrying images, function calls and function outputs.
func buildResponsesInputValue(messages []Message) any {
	if !needsStructuredInput(messages) {
		return buildResponsesInput(messages)
	}
	items := make([]responsesInputItem, 0, len(messages))
	for _, m := range messages {
		switch {
		case m.Role == "tool":
			items = append(items, responsesInputItem{
				Type:   "function_call_output",
				CallID: m.ToolCallID,
				Output: m.Content,
			})
		case len(m.ToolCalls) > 0:
			if m.Content != "" {
				items = append(items, responsesInputItem{
					Type:    "message",
					Role:    m.Role,
					Content: []responsesInputContent{{Type: "input_text", Text: m.Content}},
				})
			}
			for _, tc := range m.ToolCalls {
				items = append(items, responsesInputItem{
					Type:      "function_call",
					CallID:    tc.ID,
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				})
			}
		default:
			content := make([]responsesInputContent, 0, 2)
			for _, part := range m.MultiContent {
				switch part.Type {
				case "image_url":
					if part.ImageURL != nil {
						content = append(content, responsesInputContent{
							Type:     "input_image",
							ImageURL: part.ImageURL.URL,
						})
					}
				case "text":
					if part.Text != "" {
						content = append(content, responsesInputContent{Type: "input_text", Text: part.Text})
					}
				}
			}
			for _, imgURL := range m.Images {
				content = append(content, responsesInputContent{
					Type:     "input_image",
					ImageURL: resolveImageURLForLLM(imgURL),
				})
			}
			if m.Content != "" {
				content = append(content, responsesInputContent{Type: "input_text", Text: m.Content})
			}
			items = append(items, responsesInputItem{
				Type:    "message",
				Role:    m.Role,
				Content: content,
			})
		}
	}
	return items
}

// Envelope-valid but textless bodies (e.g. status:incomplete when a reasoning
// model exhausts max_output_tokens) are NOT errors: content is empty and
// FinishReason carries the status for the caller (#16 test policy) to judge.
// parseResponsesBody converts a raw /responses payload into a ChatResponse.
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
	for _, item := range resp.Output {
		if item.Type != "function_call" {
			continue
		}
		id := item.CallID
		if id == "" {
			id = item.ID
		}
		out.ToolCalls = append(out.ToolCalls, types.LLMToolCall{
			ID:   id,
			Type: "function",
			Function: types.FunctionCall{
				Name:      item.Name,
				Arguments: item.Arguments,
			},
		})
	}
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
		Model:     c.modelName,
		Input:     buildResponsesInputValue(messages),
		Reasoning: &responsesReasoning{Effort: c.responsesEffort},
	}
	req.Tools, req.ToolChoice = buildResponsesTools(opts)
	if opts != nil {
		if opts.MaxCompletionTokens > 0 {
			req.MaxOutputTokens = opts.MaxCompletionTokens
		} else if opts.MaxTokens > 0 {
			req.MaxOutputTokens = opts.MaxTokens
		}
	}
	httpReq, jsonData, err := c.newResponsesHTTPRequest(ctx, req)
	if err != nil {
		return nil, err
	}
	logger.Infof(ctx, "[LLM Request] Responses, endpoint=%s, model=%s, raw HTTP request:\n%s",
		httpReq.URL.String(), c.modelName, secutils.CompactImageDataURLForLog(string(jsonData)))

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
