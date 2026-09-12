package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/call"

	"github.com/Tencent/WeKnora/internal/models/provider"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

const anthropicVersion = "2023-06-01"

type AnthropicChat struct {
	modelName     string
	modelID       string
	baseURL       string
	apiKey        string
	customHeaders map[string]string
}

type anthropicCacheControl struct {
	Type string `json:"type"`
	TTL  string `json:"ttl,omitempty"`
}

type anthropicContentBlock struct {
	Type         string                 `json:"type"`
	Text         string                 `json:"text,omitempty"`
	CacheControl *anthropicCacheControl `json:"cache_control,omitempty"`
	ID           string                 `json:"id,omitempty"`
	Name         string                 `json:"name,omitempty"`
	Input        json.RawMessage        `json:"input,omitempty"`
	ToolUseID    string                 `json:"tool_use_id,omitempty"`
	Content      any                    `json:"content,omitempty"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type anthropicRequest struct {
	Model       string               `json:"model"`
	MaxTokens   int                  `json:"max_tokens"`
	Stream      bool                 `json:"stream,omitempty"`
	System      any                  `json:"system,omitempty"`
	Messages    []anthropicMessage   `json:"messages"`
	Temperature *float64             `json:"temperature,omitempty"`
	TopP        *float64             `json:"top_p,omitempty"`
	Tools       []anthropicTool      `json:"tools,omitempty"`
	ToolChoice  *anthropicToolChoice `json:"tool_choice,omitempty"`
}

type anthropicResponse struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Role    string `json:"role"`
	Content []struct {
		Type  string          `json:"type"`
		Text  string          `json:"text"`
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	} `json:"content"`
	StopReason string         `json:"stop_reason"`
	Usage      anthropicUsage `json:"usage"`
	Error      *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type anthropicStreamEvent struct {
	Type         string                 `json:"type"`
	Index        int                    `json:"index"`
	ContentBlock *anthropicContentBlock `json:"content_block,omitempty"`
	Message      *struct {
		Usage anthropicUsage `json:"usage"`
	} `json:"message,omitempty"`
	Delta *struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		StopReason  string `json:"stop_reason"`
		PartialJSON string `json:"partial_json"`
	} `json:"delta,omitempty"`
	Usage *anthropicUsage `json:"usage,omitempty"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func NewAnthropicChat(config *ChatConfig) (*AnthropicChat, error) {
	if config.BaseURL != "" {
		if err := secutils.ValidateURLForSSRF(config.BaseURL); err != nil {
			return nil, fmt.Errorf("baseURL SSRF check failed: %w", err)
		}
	}
	if strings.TrimSpace(config.APIKey) == "" {
		return nil, fmt.Errorf("Anthropic provider: API key is required")
	}

	baseURL := strings.TrimRight(config.BaseURL, "/")
	if baseURL == "" {
		baseURL = provider.AnthropicBaseURL
	}

	return &AnthropicChat{
		modelName:     config.ModelName,
		modelID:       config.ModelID,
		baseURL:       baseURL,
		apiKey:        config.APIKey,
		customHeaders: config.CustomHeaders,
	}, nil
}

// Chat sends one non-streaming request and returns provider content and usage.
func (c *AnthropicChat) Chat(
	ctx context.Context, messages []Message, opts *ChatOptions,
) (result *types.ChatResponse, err error) {
	// Anthropic's Messages API has no seed parameter: an explicit seed is
	// rejected with a typed error instead of being silently dropped.
	if OptionsSeedProvided(opts) {
		return nil, fmt.Errorf("anthropic chat %s: %w", c.modelName, ErrChatSeedUnsupported)
	}
	reqBody := c.buildRequest(ctx, messages, opts)
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	ctx, cancel := withLLMTimeout(ctx, defaultChatTimeout)
	defer cancel()

	endpoint := c.endpoint()
	if err := secutils.ValidateURLForSSRF(endpoint); err != nil {
		return nil, fmt.Errorf("endpoint SSRF check failed: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", c.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicVersion)
	secutils.ApplyCustomHeaders(httpReq, c.customHeaders)

	finish, err := call.Start(ctx, "chat")
	if err != nil {
		return nil, err
	}
	defer func() {
		var usage *types.TokenUsage
		if result != nil {
			usage = &result.Usage
		}
		err = finish(err, usage)
	}()
	resp, err := rawHTTPClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		chatResp, err := parseAnthropicSSE(bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			return nil, fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, chatResp.Content)
		}
		logUsage(ctx, c.modelName, &chatResp.Usage)
		return chatResp, nil
	}

	var chatResp anthropicResponse
	if err := json.Unmarshal(body, &chatResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		if chatResp.Error != nil && chatResp.Error.Message != "" {
			return nil, fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, chatResp.Error.Message)
		}
		return nil, fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	result = c.parseResponse(&chatResp)
	logUsage(ctx, c.modelName, &result.Usage)
	return result, nil
}

// ChatStream streams content and usage from the provider.
func (c *AnthropicChat) ChatStream(
	ctx context.Context,
	messages []Message,
	opts *ChatOptions,
) (<-chan types.StreamResponse, error) {
	if OptionsSeedProvided(opts) {
		return nil, fmt.Errorf("anthropic chat stream %s: %w", c.modelName, ErrChatSeedUnsupported)
	}
	reqBody := c.buildRequest(ctx, messages, opts)
	reqBody.Stream = true
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	endpoint := c.endpoint()
	if err := secutils.ValidateURLForSSRF(endpoint); err != nil {
		return nil, fmt.Errorf("endpoint SSRF check failed: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("x-api-key", c.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicVersion)
	secutils.ApplyCustomHeaders(httpReq, c.customHeaders)

	finish, err := call.Start(ctx, "chat_stream")
	if err != nil {
		return nil, err
	}
	resp, err := rawHTTPClient.Do(httpReq)
	if err != nil {
		return nil, finish(fmt.Errorf("send request: %w", err), nil)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, finish(fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(body)), nil)
	}

	streamChan := make(chan types.StreamResponse)
	go processAnthropicStream(ctx, c.modelName, resp, streamChan)
	return call.FinishStream(ctx, streamChan, finish), nil
}

func (c *AnthropicChat) GetModelName() string {
	return c.modelName
}

func (c *AnthropicChat) GetModelID() string {
	return c.modelID
}

func (c *AnthropicChat) endpoint() string {
	baseURL := strings.TrimRight(c.baseURL, "/")
	if isAnthropicMessagesEndpoint(baseURL) {
		return baseURL
	}
	if isAnthropicVersionedBaseURL(baseURL) {
		return baseURL + "/messages"
	}
	return baseURL + "/v1/messages"
}

func isAnthropicMessagesEndpoint(baseURL string) bool {
	u, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	path := strings.TrimRight(u.Path, "/")
	return strings.HasSuffix(path, "/messages")
}

func isAnthropicVersionedBaseURL(baseURL string) bool {
	u, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	path := strings.TrimRight(u.Path, "/")
	return strings.HasSuffix(path, "/v1") || strings.HasSuffix(path, "/v1beta")
}

func (c *AnthropicChat) buildRequest(_ context.Context, messages []Message, opts *ChatOptions) anthropicRequest {
	req := anthropicRequest{
		Model:     c.modelName,
		MaxTokens: 1024,
		Messages:  make([]anthropicMessage, 0, len(messages)),
	}
	if opts != nil {
		if budget := opts.CompletionBudget(); budget > 0 {
			req.MaxTokens = budget
		}
		if opts.Temperature > 0 {
			temperature := opts.Temperature
			req.Temperature = &temperature
		}
		if opts.TopP > 0 {
			topP := opts.TopP
			req.TopP = &topP
		}
	}

	anthropicToolOptions(&req, opts)
	systemParts, converted := anthropicMessages(messages)
	req.Messages = converted

	systemText := strings.Join(systemParts, "\n\n")
	retention := resolveCacheRetention(opts)
	marker := cacheControlFor(retention, "1h")
	if marker == nil {
		req.System = systemText
		return req
	}
	if systemText != "" {
		req.System = []anthropicContentBlock{{
			Type:         "text",
			Text:         systemText,
			CacheControl: &anthropicCacheControl{Type: marker.Type, TTL: marker.TTL},
		}}
	}
	if len(req.Messages) > 0 {
		last := &req.Messages[len(req.Messages)-1]
		if text, ok := last.Content.(string); ok && text != "" {
			last.Content = []anthropicContentBlock{{
				Type:         "text",
				Text:         text,
				CacheControl: &anthropicCacheControl{Type: marker.Type, TTL: marker.TTL},
			}}
		}
	}
	return req
}

func textFromMultiContent(parts []MessageContentPart) string {
	if len(parts) == 0 {
		return ""
	}
	textParts := make([]string, 0, len(parts))
	for _, part := range parts {
		if part.Type == "text" && strings.TrimSpace(part.Text) != "" {
			textParts = append(textParts, strings.TrimSpace(part.Text))
		}
	}
	return strings.Join(textParts, "\n")
}

func (c *AnthropicChat) parseResponse(resp *anthropicResponse) *types.ChatResponse {
	parts := make([]string, 0, len(resp.Content))
	var calls []types.LLMToolCall
	for _, part := range resp.Content {
		if part.Type == "tool_use" {
			calls = append(calls, types.LLMToolCall{
				ID: part.ID, Type: "function",
				Function: types.FunctionCall{Name: part.Name, Arguments: string(part.Input)},
			})
		}
		if part.Type == "text" && part.Text != "" {
			parts = append(parts, part.Text)
		}
	}
	inputTokens := resp.Usage.InputTokens
	outputTokens := resp.Usage.OutputTokens
	cacheRead := valueOrZero(resp.Usage.CacheReadInputTokens)
	cacheWrite := valueOrZero(resp.Usage.CacheCreationInputTokens)
	promptTokens := inputTokens + cacheRead + cacheWrite
	usage := types.TokenUsage{
		UsageReported:    resp.Usage.Present,
		PromptTokens:     promptTokens,
		CompletionTokens: outputTokens,
		TotalTokens:      promptTokens + outputTokens,
	}
	usage.SetPromptCacheUsage(cacheRead, cacheWrite, max(0, promptTokens-cacheRead),
		resp.Usage.CacheReadInputTokens != nil || resp.Usage.CacheCreationInputTokens != nil)
	applyAnthropicWriteBuckets(&usage, resp.Usage)
	return &types.ChatResponse{
		Content:      strings.Join(parts, ""),
		FinishReason: anthropicToolStream{}.finishReason(resp.StopReason),
		ToolCalls:    calls,
		Usage:        usage,
	}
}

func parseAnthropicSSE(reader io.Reader) (*types.ChatResponse, error) {
	stream := make(chan types.StreamResponse)
	go processAnthropicStream(context.Background(), "", &http.Response{Body: io.NopCloser(reader)}, stream)
	result := &types.ChatResponse{}
	var streamErr error
	for response := range stream {
		if response.Usage != nil {
			result.Usage = *response.Usage
		}
		if response.Done {
			result.ToolCalls = response.ToolCalls
		}
		if response.FinishReason != "" {
			result.FinishReason = response.FinishReason
		}
		if response.ResponseType == types.ResponseTypeError {
			streamErr = fmt.Errorf("anthropic stream: %s", response.Content)
		} else {
			result.Content += response.Content
		}
	}
	return result, streamErr
}

func processAnthropicStream(
	ctx context.Context,
	model string,
	resp *http.Response,
	streamChan chan types.StreamResponse,
) {
	defer close(streamChan)
	defer resp.Body.Close()
	reader := NewSSEReader(resp.Body)
	var usage *types.TokenUsage
	var finishReason string
	toolStream := anthropicToolStream{}
	terminal := func(err error) {
		out := types.StreamResponse{
			ResponseType: types.ResponseTypeAnswer,
			Done:         true,
			Usage:        usage,
			FinishReason: toolStream.finishReason(finishReason),
			ToolCalls:    toolStream.calls(),
		}
		if err != nil {
			out.ResponseType = types.ResponseTypeError
			out.Content = err.Error()
			out.FinishReason = types.FinishReasonIncomplete
		}
		logUsage(ctx, model, usage)
		emitStream(ctx, streamChan, out)
	}
	for {
		types.StreamActivity(ctx, true)
		event, err := reader.ReadEvent()
		types.StreamActivity(ctx, false)
		if err != nil {
			terminal(fmt.Errorf("anthropic stream incomplete: %w", err))
			return
		}
		if event == nil {
			continue
		}
		if event.Done {
			terminal(nil)
			return
		}
		if len(event.Data) == 0 {
			continue
		}
		var incoming anthropicStreamEvent
		if err = json.Unmarshal(event.Data, &incoming); err != nil {
			terminal(fmt.Errorf("decode SSE response: %w", err))
			return
		}
		if incoming.Error != nil {
			terminal(fmt.Errorf("anthropic stream: %s", incoming.Error.Message))
			return
		}
		toolStream.consume(incoming)
		if incoming.Type == "content_block_start" && incoming.ContentBlock != nil &&
			incoming.ContentBlock.Type == "tool_use" {
			block := incoming.ContentBlock
			if !emitStream(ctx, streamChan, types.StreamResponse{
				ResponseType: types.ResponseTypeToolCall,
				Data:         map[string]interface{}{"tool_call_id": block.ID, "tool_name": block.Name},
			}) {
				return
			}
		}
		if incoming.Message != nil && incoming.Message.Usage.Present {
			usage = mergeAnthropicUsage(usage, incoming.Message.Usage.InputTokens, incoming.Message.Usage.OutputTokens,
				incoming.Message.Usage.CacheReadInputTokens, incoming.Message.Usage.CacheCreationInputTokens)
			applyAnthropicWriteBuckets(usage, incoming.Message.Usage)
		}
		if incoming.Usage != nil {
			usage = mergeAnthropicUsage(usage, incoming.Usage.InputTokens, incoming.Usage.OutputTokens,
				incoming.Usage.CacheReadInputTokens, incoming.Usage.CacheCreationInputTokens)
			applyAnthropicWriteBuckets(usage, *incoming.Usage)
		}
		if incoming.Delta != nil {
			if incoming.Delta.StopReason != "" {
				finishReason = incoming.Delta.StopReason
			}
			if incoming.Delta.Type == "text_delta" && incoming.Delta.Text != "" {
				if !emitStream(
					ctx,
					streamChan,
					types.StreamResponse{
						ResponseType: types.ResponseTypeAnswer,
						Content:      incoming.Delta.Text,
					},
				) {
					return
				}
			}
		}
		if incoming.Type == "message_stop" {
			terminal(nil)
			return
		}
	}
}

func mergeAnthropicUsage(
	current *types.TokenUsage,
	inputTokens, outputTokens int,
	cacheRead, cacheWrite *int,
) *types.TokenUsage {
	if current == nil {
		current = &types.TokenUsage{}
	}
	read, write, reported := mergeAnthropicCacheCounters(
		current.CacheReadTokens, current.CacheWriteTokens, current.CacheReported,
		cacheRead, cacheWrite,
	)
	uncachedInput := max(0, current.PromptTokens-current.CacheReadTokens-current.CacheWriteTokens)
	uncachedInput = max(uncachedInput, inputTokens)
	current.UsageReported = true
	current.PromptTokens = uncachedInput + read + write
	current.CompletionTokens = max(current.CompletionTokens, outputTokens)
	current.TotalTokens = current.PromptTokens + current.CompletionTokens
	current.SetPromptCacheUsage(read, write, max(0, current.PromptTokens-read), reported)
	return current
}

func mergeAnthropicCacheCounters(
	currentRead, currentWrite int,
	currentReported bool,
	cacheRead, cacheWrite *int,
) (read, write int, reported bool) {
	read = currentRead
	write = currentWrite
	reported = currentReported || cacheRead != nil || cacheWrite != nil
	if cacheRead != nil {
		read = max(read, *cacheRead)
	}
	if cacheWrite != nil {
		write = max(write, *cacheWrite)
	}
	return read, write, reported
}

// RequestAccountingSupported reports support for accounting at each physical provider request.
func (c *AnthropicChat) RequestAccountingSupported() bool { return true }

type anthropicUsage struct {
	InputTokens              int  `json:"input_tokens"`
	OutputTokens             int  `json:"output_tokens"`
	CacheCreationInputTokens *int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     *int `json:"cache_read_input_tokens"`
	CacheCreation            *struct {
		Short *int `json:"ephemeral_5m_input_tokens"`
		Long  *int `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`
	Present bool `json:"-"`
}

func (u *anthropicUsage) UnmarshalJSON(data []byte) error {
	type plain anthropicUsage
	if err := json.Unmarshal(data, (*plain)(u)); err != nil {
		return err
	}
	u.Present = string(data) != "null"
	return nil
}

func applyAnthropicWriteBuckets(u *types.TokenUsage, raw anthropicUsage) {
	if u == nil || raw.CacheCreation == nil {
		return
	}
	if raw.CacheCreation.Short != nil {
		value := *raw.CacheCreation.Short
		u.CacheWrite5mTokens = &value
	}
	if raw.CacheCreation.Long != nil {
		value := *raw.CacheCreation.Long
		u.CacheWrite1hTokens = &value
	}
}
