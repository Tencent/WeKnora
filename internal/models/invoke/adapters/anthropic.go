package adapters

// AnthropicAdapter: the Anthropic Messages protocol (design v2 §6.2, OQ4).
// Behavior is a line-for-line port of v1 internal/models/chat/anthropic.go
// (P1b strangler): system folding, thinking.budget_tokens mapping with the
// max_tokens raise, cache_control breakpoints, and the SSE stream bridge
// (anthropic_stream.go). Reconciliation gate: testdata/golden/anthropic_*.json.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/invoke"
)

const anthropicVersion = "2023-06-01"

// AnthropicAdapter serves the chat facet for provider "anthropic".
// Stateless: the model record's thinking config arrives folded into
// ChatOptions.ThinkingLevel (§5.2); the provider-tier fallback comes from the
// provider registry's ThinkingCaps.
type AnthropicAdapter struct {
	thinkingCaps invoke.ThinkingCaps
}

var _ invoke.ChatAdapter = (*AnthropicAdapter)(nil)

func init() {
	if err := invoke.Default.Register(newAnthropicAdapter()); err != nil {
		panic(err) // three-lock #2 fail fast: facet/capability mismatch is a programming error
	}
}

func newAnthropicAdapter() *AnthropicAdapter {
	var caps invoke.ThinkingCaps
	if info, ok := providerInfoFor(invoke.ProviderAnthropic); ok {
		if chatCaps := info.EffectiveCapabilities().Chat; chatCaps != nil {
			caps = chatCaps.Thinking
		}
	}
	return &AnthropicAdapter{thinkingCaps: caps}
}

// Provider returns the canonical provider name.
func (a *AnthropicAdapter) Provider() string { return string(invoke.ProviderAnthropic) }

// Capabilities reports the provider's effective capabilities, plus the
// model-listing flag this adapter serves (BuildListRequest in list.go).
func (a *AnthropicAdapter) Capabilities() invoke.Capabilities {
	if info, ok := providerInfoFor(invoke.ProviderAnthropic); ok {
		caps := info.EffectiveCapabilities()
		caps.Common.ModelListing = invoke.ModelListingCaps{Supported: true}
		return caps
	}
	return invoke.Capabilities{
		Common: invoke.CommonCaps{ModelListing: invoke.ModelListingCaps{Supported: true}},
		Chat:   &invoke.ChatCaps{Thinking: a.thinkingCaps},
	}
}

// --- Wire types (field order pinned: golden request bodies are compared
// byte-for-byte after compact normalization, so struct order matters). ---

type anthropicCacheControl struct {
	Type string `json:"type"`
	TTL  string `json:"ttl,omitempty"`
}

type anthropicContentBlock struct {
	Type         string                 `json:"type"`
	Text         string                 `json:"text,omitempty"`
	CacheControl *anthropicCacheControl `json:"cache_control,omitempty"`
	// Tool-use shapes (see anthropic_tools.go): tool_use blocks carry
	// ID/Name/Input; tool_result blocks carry ToolUseID/Content.
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   any             `json:"content,omitempty"`
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
	// Thinking carries the Messages-API thinking block. The API requires
	// max_tokens > thinking.budget_tokens; BuildChatRequest enforces this.
	Thinking *anthropicThinkingConfig `json:"thinking,omitempty"`
}

type anthropicThinkingConfig struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens"`
}

type anthropicResponse struct {
	Content []struct {
		Type     string          `json:"type"`
		Text     string          `json:"text"`
		Thinking string          `json:"thinking"`
		ID       string          `json:"id"`
		Name     string          `json:"name"`
		Input    json.RawMessage `json:"input"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
	Usage      struct {
		InputTokens              int  `json:"input_tokens"`
		OutputTokens             int  `json:"output_tokens"`
		CacheCreationInputTokens *int `json:"cache_creation_input_tokens"`
		CacheReadInputTokens     *int `json:"cache_read_input_tokens"`
	} `json:"usage"`
}

type anthropicStreamEvent struct {
	Type         string                 `json:"type"`
	Index        int                    `json:"index"`
	ContentBlock *anthropicContentBlock `json:"content_block,omitempty"`
	Message      *struct {
		Usage anthropicUsageFields `json:"usage"`
	} `json:"message,omitempty"`
	Delta *struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		Thinking    string `json:"thinking"`
		StopReason  string `json:"stop_reason"`
		PartialJSON string `json:"partial_json"`
	} `json:"delta,omitempty"`
	Usage *anthropicUsageFields `json:"usage,omitempty"`
	Error *anthropicErrorFields `json:"error,omitempty"`
}

type anthropicUsageFields struct {
	InputTokens              int  `json:"input_tokens"`
	OutputTokens             int  `json:"output_tokens"`
	CacheCreationInputTokens *int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     *int `json:"cache_read_input_tokens"`
}

type anthropicErrorFields struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// BuildChatRequest ports v1 buildRequest. SSRF/timeout/concurrency belong to
// the executor; the adapter only describes the call.
func (a *AnthropicAdapter) BuildChatRequest(
	ep invoke.Endpoint, model string, opts *invoke.ChatOptions,
) (*invoke.Request, error) {
	apiKey := strings.TrimSpace(ep.Credentials.APIKey)
	if apiKey == "" {
		return nil, fmt.Errorf("anthropic provider: API key is required")
	}
	baseURL := strings.TrimRight(ep.BaseURL, "/")
	if baseURL == "" {
		baseURL = invoke.AnthropicBaseURL
	}

	req := anthropicRequest{
		Model:     model,
		MaxTokens: 1024,
		Stream:    opts.Stream, // v1 openStream set reqBody.Stream=true after build
		Messages:  make([]anthropicMessage, 0, len(opts.Messages)),
	}
	if budget := opts.MaxCompletionTokens; budget > 0 {
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

	// Extended thinking (§5.3): resolve the level through the same chain the
	// openai-family adapters use. The model-record tier (level + selection set)
	// is folded upstream; the adapter applies the call level against the
	// provider's caps. Only enabled thinking carries the block — nil (model
	// default) and false (off) leave the request unchanged.
	if opts.Thinking != nil && *opts.Thinking {
		level := invoke.ResolveThinkingLevel(opts.ThinkingLevel, "", nil, a.thinkingCaps)
		if budget := anthropicBudgetTokens(level); budget > 0 {
			// max_tokens must exceed the thinking budget: the budget is carved
			// out of the output ceiling, so raise it to keep room for the answer.
			if req.MaxTokens <= budget {
				req.MaxTokens = budget + 4096
			}
			req.Thinking = &anthropicThinkingConfig{Type: "enabled", BudgetTokens: budget}
		}
	}

	anthropicToolOptions(&req, opts)

	var systemParts []string
	for _, msg := range opts.Messages {
		content := strings.TrimSpace(textFromParts(msg.Content))
		// Anthropic remaps the neutral vocabulary (no system/tool roles on
		// the wire); a new invoke.Role must be mapped here explicitly, not
		// silently fall through. Tool calls ride content blocks (tool_use /
		// tool_result via anthropic_tools.go), so emptiness is decided
		// per-case, not by a pre-switch skip: an assistant turn that only
		// calls tools has no text but must reach the wire, and empty tool
		// results stay (every tool_use needs a result).
		//exhaustive:enforce
		switch msg.Role {
		case invoke.RoleSystem:
			if content != "" {
				systemParts = append(systemParts, content)
			}
		case invoke.RoleAssistant:
			if len(msg.ToolCalls) > 0 {
				req.Messages = append(req.Messages, anthropicMessage{
					Role:    "assistant",
					Content: anthropicToolUseBlocks(content, msg.ToolCalls),
				})
				continue
			}
			if content == "" {
				continue
			}
			req.Messages = append(req.Messages, anthropicMessage{Role: "assistant", Content: content})
		case invoke.RoleTool:
			block := anthropicToolResultBlock(msg)
			// Parallel results share one user message: fold into the previous
			// message when it is already a tool_result block list.
			if len(req.Messages) > 0 {
				if last := &req.Messages[len(req.Messages)-1]; last.Role == "user" {
					blocks, ok := last.Content.([]anthropicContentBlock)
					if ok && len(blocks) > 0 && blocks[0].Type == "tool_result" {
						last.Content = append(blocks, block)
						continue
					}
				}
			}
			req.Messages = append(req.Messages, anthropicMessage{Role: "user", Content: []anthropicContentBlock{block}})
		case invoke.RoleUser:
			if content == "" {
				continue
			}
			req.Messages = append(req.Messages, anthropicMessage{Role: "user", Content: content})
		default:
			if content == "" {
				continue
			}
			req.Messages = append(req.Messages, anthropicMessage{Role: "user", Content: content})
		}
	}
	systemText := strings.Join(systemParts, "\n\n")
	retention := opts.CacheRetention
	if retention == "" {
		retention = invoke.CacheRetentionShort
	}
	marker := anthropicCacheMarker(retention, "1h")
	if marker == nil {
		req.System = systemText
	} else {
		if systemText != "" {
			req.System = []anthropicContentBlock{{
				Type: "text", Text: systemText,
				CacheControl: &anthropicCacheControl{Type: marker.Type, TTL: marker.TTL},
			}}
		}
		if len(req.Messages) > 0 {
			last := &req.Messages[len(req.Messages)-1]
			if text, ok := last.Content.(string); ok && text != "" {
				last.Content = []anthropicContentBlock{{
					Type: "text", Text: text,
					CacheControl: &anthropicCacheControl{Type: marker.Type, TTL: marker.TTL},
				}}
			}
		}
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	header := http.Header{}
	header.Set("Content-Type", "application/json")
	header.Set("x-api-key", apiKey)
	header.Set("anthropic-version", anthropicVersion)
	if req.Stream {
		header.Set("Accept", "text/event-stream")
	}
	return &invoke.Request{
		Method:           http.MethodPost,
		URL:              anthropicEndpoint(baseURL),
		Header:           header,
		Body:             body,
		ProtectedHeaders: []string{"X-Api-Key", "Anthropic-Version"},
		Stream:           req.Stream,
	}, nil
}

// anthropicCacheMarker ports v1 cacheControlFor: nil disables markers; short
// is 5-minute ephemeral; long adds the provider TTL.
func anthropicCacheMarker(retention, longTTL string) *anthropicCacheControl {
	if retention == invoke.CacheRetentionNone {
		return nil
	}
	marker := &anthropicCacheControl{Type: "ephemeral"}
	if retention == invoke.CacheRetentionLong && longTTL != "" {
		marker.TTL = longTTL
	}
	return marker
}

// anthropicEndpoint ports v1 endpoint() + the two suffix probes verbatim:
// a baseURL already ending in /messages is used as-is; /v1 or /v1beta gets
// /messages; anything else gets /v1/messages.
func anthropicEndpoint(baseURL string) string {
	baseURL = strings.TrimRight(baseURL, "/")
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
	return strings.HasSuffix(strings.TrimRight(u.Path, "/"), "/messages")
}

func isAnthropicVersionedBaseURL(baseURL string) bool {
	u, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	path := strings.TrimRight(u.Path, "/")
	return strings.HasSuffix(path, "/v1") || strings.HasSuffix(path, "/v1beta")
}

// anthropicBudgetTokens maps the platform thinking level to the Anthropic
// thinking budget (§5.3: low→2K / medium→8K / high→16K). Levels outside the
// mapping return 0 — no thinking block (defensive; the provider caps declare
// {low, medium, high} so the resolver never yields others here).
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

// textFromParts extracts the message text from the neutral part slice the
// same way v1 textFromMultiContent did: trimmed text parts joined with "\n".
func textFromParts(parts []invoke.Part) string {
	if len(parts) == 0 {
		return ""
	}
	texts := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part.Text) != "" {
			texts = append(texts, strings.TrimSpace(part.Text))
		}
	}
	return strings.Join(texts, "\n")
}

// ParseChatResponse ports v1 parseResponse plus the v1 quirk where a
// non-stream call may still receive an SSE body (some proxies); the executor
// has already classified non-2xx, so only success bodies arrive here.
func (a *AnthropicAdapter) ParseChatResponse(_ int, header http.Header, body []byte) (*invoke.ChatResponse, error) {
	if strings.Contains(strings.ToLower(header.Get("Content-Type")), "text/event-stream") {
		return aggregateAnthropicSSE(body)
	}
	var resp anthropicResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return parseAnthropicResponse(&resp), nil
}

func parseAnthropicResponse(resp *anthropicResponse) *invoke.ChatResponse {
	parts := make([]string, 0, len(resp.Content))
	var calls []invoke.ToolCall
	var thinking strings.Builder
	for _, part := range resp.Content {
		if part.Type == "thinking" && part.Thinking != "" {
			thinking.WriteString(part.Thinking)
		}
		if part.Type == "tool_use" {
			calls = append(calls, invoke.ToolCall{
				ID:   part.ID,
				Type: "function",
				Function: invoke.FunctionCall{
					Name:      part.Name,
					Arguments: string(part.Input),
				},
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
	out := &invoke.ChatResponse{
		Content: strings.Join(parts, ""),
		// No tools were streamed here, so the empty tool stream folds the
		// stop reason only (max_tokens → length, "" → incomplete).
		FinishReason: (anthropicToolStream{}).finishReason(resp.StopReason),
		ToolCalls:    calls,
		Usage: invoke.Usage{
			PromptTokens:     promptTokens,
			CompletionTokens: outputTokens,
			TotalTokens:      promptTokens + outputTokens,
			// v1 SetPromptCacheUsage parity (2026-09-13 review: the v2 port
			// dropped the detail fields — same regression as the stream side;
			// without them the engine's cache_hit_rate stats stay empty).
			CacheReadTokens:  cacheRead,
			CacheWriteTokens: cacheWrite,
			CacheMissTokens:  max(0, promptTokens-cacheRead),
			CacheReported:    resp.Usage.CacheReadInputTokens != nil || resp.Usage.CacheCreationInputTokens != nil,
		},
	}
	if thinking.Len() > 0 {
		thinkingText := thinking.String()
		out.Thinking = &thinkingText
	}
	return out
}

func valueOrZero(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}
