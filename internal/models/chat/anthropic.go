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
	// modelThinkingLevel / selectedLevels are the model record's stored
	// thinking config (Chat shard); buildRequest resolves the effective level
	// through them plus the provider default (design §4.2) and maps it to
	// thinking.budget_tokens (design §4.3 continuous-vendor收编).
	modelThinkingLevel string
	selectedLevels     []string
	thinkingCaps       provider.ThinkingCaps
}

type anthropicCacheControl struct {
	Type string `json:"type"`
	TTL  string `json:"ttl,omitempty"`
}

type anthropicContentBlock struct {
	Type         string                 `json:"type"`
	Text         string                 `json:"text,omitempty"`
	CacheControl *anthropicCacheControl `json:"cache_control,omitempty"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type anthropicRequest struct {
	Model       string             `json:"model"`
	MaxTokens   int                `json:"max_tokens"`
	Stream      bool               `json:"stream,omitempty"`
	System      any                `json:"system,omitempty"`
	Messages    []anthropicMessage `json:"messages"`
	Temperature *float64           `json:"temperature,omitempty"`
	TopP        *float64           `json:"top_p,omitempty"`
	// Thinking carries the extended-thinking block. The API requires
	// max_tokens > thinking.budget_tokens; buildRequest enforces this.
	Thinking *anthropicThinkingConfig `json:"thinking,omitempty"`
}

// anthropicThinkingConfig is the Messages-API thinking block:
// {"type": "enabled", "budget_tokens": N}.
type anthropicThinkingConfig struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens"`
}

type anthropicResponse struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Role    string `json:"role"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
	Usage      struct {
		InputTokens              int  `json:"input_tokens"`
		OutputTokens             int  `json:"output_tokens"`
		CacheCreationInputTokens *int `json:"cache_creation_input_tokens"`
		CacheReadInputTokens     *int `json:"cache_read_input_tokens"`
	} `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type anthropicStreamEvent struct {
	Type    string `json:"type"`
	Message *struct {
		Usage struct {
			InputTokens              int  `json:"input_tokens"`
			OutputTokens             int  `json:"output_tokens"`
			CacheCreationInputTokens *int `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     *int `json:"cache_read_input_tokens"`
		} `json:"usage"`
	} `json:"message,omitempty"`
	Delta *struct {
		Type       string `json:"type"`
		Text       string `json:"text"`
		StopReason string `json:"stop_reason"`
	} `json:"delta,omitempty"`
	Usage *struct {
		InputTokens              int  `json:"input_tokens"`
		OutputTokens             int  `json:"output_tokens"`
		CacheCreationInputTokens *int `json:"cache_creation_input_tokens"`
		CacheReadInputTokens     *int `json:"cache_read_input_tokens"`
	} `json:"usage,omitempty"`
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

	// Cache the provider-level thinking caps for level resolution (same
	// pattern as RemoteAPIChat; the Anthropic path does not go through the
	// OpenAI-compatible funnel).
	var thinkingCaps provider.ThinkingCaps
	if p, ok := provider.Get(provider.ProviderAnthropic); ok {
		if chatCaps := p.Info().EffectiveCapabilities().Chat; chatCaps != nil {
			thinkingCaps = chatCaps.Thinking
		}
	}

	return &AnthropicChat{
		modelName:          config.ModelName,
		modelID:            config.ModelID,
		baseURL:            baseURL,
		apiKey:             config.APIKey,
		customHeaders:      config.CustomHeaders,
		modelThinkingLevel: config.ThinkingLevel,
		selectedLevels:     config.SelectedLevels,
		thinkingCaps:       thinkingCaps,
	}, nil
}

func (c *AnthropicChat) Chat(ctx context.Context, messages []Message, opts *ChatOptions) (*types.ChatResponse, error) {
	timeoutCtx, cancel := withLLMTimeout(ctx, defaultChatTimeout)
	defer cancel()
	// Provider-level retry (design §4.6), same policy as the OpenAI family.
	return withProviderRetry(timeoutCtx, func(context.Context) (*types.ChatResponse, error) {
		return c.chatOnce(timeoutCtx, messages, opts)
	})
}

// chatOnce is one Chat attempt for the Anthropic Messages protocol.
func (c *AnthropicChat) chatOnce(
	ctx context.Context, messages []Message, opts *ChatOptions,
) (*types.ChatResponse, error) {
	reqBody := c.buildRequest(ctx, messages, opts)
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
	httpReq.Header.Set("x-api-key", c.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicVersion)
	secutils.ApplyCustomHeaders(httpReq, c.customHeaders)

	resp, err := rawHTTPClient.Do(httpReq)
	if err != nil {
		return nil, wrapInvokeError("send request", err)
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
			return nil, classifyStatusBody(resp.StatusCode, chatResp.Content)
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
			return nil, classifyStatusBody(resp.StatusCode, chatResp.Error.Message)
		}
		return nil, classifyStatusBody(resp.StatusCode, string(body))
	}

	result := c.parseResponse(&chatResp)
	logUsage(ctx, c.modelName, &result.Usage)
	return result, nil
}

func (c *AnthropicChat) ChatStream(ctx context.Context, messages []Message, opts *ChatOptions) (<-chan types.StreamResponse, error) {
	// Retry scoped to the pre-stream window: build + send + status check.
	// Once processAnthropicStream starts feeding the channel, failures are
	// no longer retryable (the consumer has seen earlier chunks).
	resp, err := withProviderRetry(ctx, func(context.Context) (*http.Response, error) {
		return c.openStream(ctx, messages, opts)
	})
	if err != nil {
		return nil, err
	}
	streamChan := make(chan types.StreamResponse)
	go processAnthropicStream(ctx, c.modelName, resp, streamChan)
	return streamChan, nil
}

// openStream builds and sends one streaming request, returning the live
// response or a classified error. The caller owns closing resp on success.
func (c *AnthropicChat) openStream(ctx context.Context, messages []Message, opts *ChatOptions) (*http.Response, error) {
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

	resp, err := rawHTTPClient.Do(httpReq)
	if err != nil {
		return nil, wrapInvokeError("send request", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, classifyStatusBody(resp.StatusCode, string(body))
	}
	return resp, nil
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
		// Extended thinking (design §4.3 continuous-vendor收编): resolve the
		// level through the same chain the OpenAI funnel uses, then map it to
		// thinking.budget_tokens. Only enabled thinking carries the block —
		// nil (model default) and false (off) leave the request unchanged,
		// preserving the pre-thinking behavior for existing callers.
		if opts.Thinking != nil && *opts.Thinking {
			level := ResolveThinkingLevel(opts.ThinkingLevel, c.modelThinkingLevel, c.selectedLevels, c.thinkingCaps)
			if budget := anthropicBudgetTokens(level); budget > 0 {
				// The Messages API requires max_tokens > thinking.budget_tokens:
				// the budget is carved out of the output ceiling, so raise the
				// ceiling to keep room for the answer itself when the caller's
				// completion budget does not already exceed the thinking budget.
				if req.MaxTokens <= budget {
					req.MaxTokens = budget + 4096
				}
				req.Thinking = &anthropicThinkingConfig{Type: "enabled", BudgetTokens: budget}
			}
		}
	}

	var systemParts []string
	for _, msg := range messages {
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			content = textFromMultiContent(msg.MultiContent)
		}
		if content == "" {
			continue
		}
		switch msg.Role {
		case "system":
			systemParts = append(systemParts, content)
		case "assistant":
			req.Messages = append(req.Messages, anthropicMessage{Role: "assistant", Content: content})
		case "user":
			req.Messages = append(req.Messages, anthropicMessage{Role: "user", Content: content})
		default:
			req.Messages = append(req.Messages, anthropicMessage{Role: "user", Content: content})
		}
	}
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

// anthropicBudgetTokens maps the platform thinking level to an Anthropic
// thinking budget (design §4.3: low→2K / medium→8K / high→16K
// budget_tokens). Levels outside the mapping (xhigh/max, or empty) return 0 —
// no thinking block is emitted and the model keeps its own default. Anthropic
// provider caps declare SupportedLevels {low, medium, high}, so the resolver
// never produces xhigh/max here; the 0 return is a defensive fallback.
func anthropicBudgetTokens(level string) int {
	switch level {
	case "low":
		return 2048
	case "medium":
		return 8192
	case "high":
		return 16384
	default:
		return 0
	}
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
	for _, part := range resp.Content {
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
		PromptTokens:     promptTokens,
		CompletionTokens: outputTokens,
		TotalTokens:      promptTokens + outputTokens,
	}
	usage.SetPromptCacheUsage(cacheRead, cacheWrite, max(0, promptTokens-cacheRead),
		resp.Usage.CacheReadInputTokens != nil || resp.Usage.CacheCreationInputTokens != nil)
	return &types.ChatResponse{
		Content:      strings.Join(parts, ""),
		FinishReason: resp.StopReason,
		Usage:        usage,
	}
}

func parseAnthropicSSE(reader io.Reader) (*types.ChatResponse, error) {
	sseReader := NewSSEReader(reader)
	var contentParts []string
	var finishReason string
	var inputTokens int
	var outputTokens int
	var cacheReadTokens int
	var cacheWriteTokens int
	var cacheReported bool

	for {
		event, err := sseReader.ReadEvent()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read SSE response: %w", err)
		}
		if event.Done {
			break
		}
		if len(event.Data) == 0 {
			continue
		}

		var streamEvent anthropicStreamEvent
		if err := json.Unmarshal(event.Data, &streamEvent); err != nil {
			return nil, fmt.Errorf("decode SSE response: %w", err)
		}
		if streamEvent.Error != nil && streamEvent.Error.Message != "" {
			return nil, fmt.Errorf("API stream error: %s", streamEvent.Error.Message)
		}
		if streamEvent.Message != nil {
			inputTokens = max(inputTokens, streamEvent.Message.Usage.InputTokens)
			outputTokens = max(outputTokens, streamEvent.Message.Usage.OutputTokens)
			cacheReadTokens, cacheWriteTokens, cacheReported = mergeAnthropicCacheCounters(
				cacheReadTokens, cacheWriteTokens, cacheReported,
				streamEvent.Message.Usage.CacheReadInputTokens,
				streamEvent.Message.Usage.CacheCreationInputTokens,
			)
		}
		if streamEvent.Delta != nil {
			if streamEvent.Delta.Type == "text_delta" && streamEvent.Delta.Text != "" {
				contentParts = append(contentParts, streamEvent.Delta.Text)
			}
			if streamEvent.Delta.StopReason != "" {
				finishReason = streamEvent.Delta.StopReason
			}
		}
		if streamEvent.Usage != nil {
			inputTokens = max(inputTokens, streamEvent.Usage.InputTokens)
			outputTokens = max(outputTokens, streamEvent.Usage.OutputTokens)
			cacheReadTokens, cacheWriteTokens, cacheReported = mergeAnthropicCacheCounters(
				cacheReadTokens, cacheWriteTokens, cacheReported,
				streamEvent.Usage.CacheReadInputTokens,
				streamEvent.Usage.CacheCreationInputTokens,
			)
		}
	}
	promptTokens := inputTokens + cacheReadTokens + cacheWriteTokens
	usage := types.TokenUsage{
		PromptTokens:     promptTokens,
		CompletionTokens: outputTokens,
		TotalTokens:      promptTokens + outputTokens,
	}
	usage.SetPromptCacheUsage(cacheReadTokens, cacheWriteTokens,
		max(0, promptTokens-cacheReadTokens), cacheReported)

	return &types.ChatResponse{
		Content:      strings.Join(contentParts, ""),
		FinishReason: finishReason,
		Usage:        usage,
	}, nil
}

func processAnthropicStream(ctx context.Context, model string, resp *http.Response, streamChan chan types.StreamResponse) {
	defer close(streamChan)
	defer resp.Body.Close()

	sseReader := NewSSEReader(resp.Body)
	var usage *types.TokenUsage
	var finishReason string

	for {
		event, err := sseReader.ReadEvent()
		if err != nil {
			if err == io.EOF {
				logUsage(ctx, model, usage)
				streamChan <- types.StreamResponse{
					ResponseType: types.ResponseTypeAnswer,
					Content:      "",
					Done:         true,
					Usage:        usage,
					FinishReason: finishReason,
				}
			} else {
				streamChan <- types.StreamResponse{
					ResponseType: types.ResponseTypeError,
					Content:      err.Error(),
					Done:         true,
				}
			}
			return
		}
		if event.Done {
			logUsage(ctx, model, usage)
			streamChan <- types.StreamResponse{
				ResponseType: types.ResponseTypeAnswer,
				Content:      "",
				Done:         true,
				Usage:        usage,
				FinishReason: finishReason,
			}
			return
		}
		if len(event.Data) == 0 {
			continue
		}

		var streamEvent anthropicStreamEvent
		if err := json.Unmarshal(event.Data, &streamEvent); err != nil {
			streamChan <- types.StreamResponse{
				ResponseType: types.ResponseTypeError,
				Content:      fmt.Sprintf("decode SSE response: %v", err),
				Done:         true,
			}
			return
		}
		if streamEvent.Error != nil && streamEvent.Error.Message != "" {
			streamChan <- types.StreamResponse{
				ResponseType: types.ResponseTypeError,
				Content:      streamEvent.Error.Message,
				Done:         true,
			}
			return
		}
		if streamEvent.Message != nil {
			usage = mergeAnthropicUsage(usage, streamEvent.Message.Usage.InputTokens,
				streamEvent.Message.Usage.OutputTokens,
				streamEvent.Message.Usage.CacheReadInputTokens,
				streamEvent.Message.Usage.CacheCreationInputTokens)
		}
		if streamEvent.Delta != nil {
			if streamEvent.Delta.StopReason != "" {
				finishReason = streamEvent.Delta.StopReason
			}
			if streamEvent.Delta.Type == "text_delta" && streamEvent.Delta.Text != "" {
				streamChan <- types.StreamResponse{
					ResponseType: types.ResponseTypeAnswer,
					Content:      streamEvent.Delta.Text,
					Done:         false,
				}
			}
		}
		if streamEvent.Usage != nil {
			usage = mergeAnthropicUsage(usage, streamEvent.Usage.InputTokens,
				streamEvent.Usage.OutputTokens,
				streamEvent.Usage.CacheReadInputTokens,
				streamEvent.Usage.CacheCreationInputTokens)
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
