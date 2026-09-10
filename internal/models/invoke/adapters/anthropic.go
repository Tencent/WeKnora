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
	"github.com/Tencent/WeKnora/internal/models/provider"
)

const anthropicVersion = "2023-06-01"

// AnthropicAdapter serves the chat facet for provider "anthropic".
// Stateless: the model record's thinking config arrives folded into
// ChatOptions.ThinkingLevel (§5.2); the provider-tier fallback comes from the
// provider registry's ThinkingCaps.
type AnthropicAdapter struct {
	thinkingCaps provider.ThinkingCaps
}

var _ invoke.ChatAdapter = (*AnthropicAdapter)(nil)

func init() {
	if err := invoke.Default.Register(newAnthropicAdapter()); err != nil {
		panic(err) // three-lock #2 fail fast: facet/capability mismatch is a programming error
	}
}

func newAnthropicAdapter() *AnthropicAdapter {
	var caps provider.ThinkingCaps
	if p, ok := provider.Get(provider.ProviderAnthropic); ok {
		if chatCaps := p.Info().EffectiveCapabilities().Chat; chatCaps != nil {
			caps = chatCaps.Thinking
		}
	}
	return &AnthropicAdapter{thinkingCaps: caps}
}

// Provider returns the canonical provider name.
func (a *AnthropicAdapter) Provider() string { return string(provider.ProviderAnthropic) }

// Capabilities reports the provider's effective capabilities.
func (a *AnthropicAdapter) Capabilities() provider.Capabilities {
	if p, ok := provider.Get(provider.ProviderAnthropic); ok {
		return p.Info().EffectiveCapabilities()
	}
	return provider.Capabilities{Chat: &provider.ChatCaps{Thinking: a.thinkingCaps}}
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
}

type anthropicStreamEvent struct {
	Type    string `json:"type"`
	Message *struct {
		Usage anthropicUsageFields `json:"usage"`
	} `json:"message,omitempty"`
	Delta *struct {
		Type       string `json:"type"`
		Text       string `json:"text"`
		StopReason string `json:"stop_reason"`
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
		baseURL = provider.AnthropicBaseURL
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

	var systemParts []string
	for _, msg := range opts.Messages {
		content := strings.TrimSpace(textFromParts(msg.Content))
		if content == "" {
			continue
		}
		// Anthropic remaps the neutral vocabulary (no system/tool roles on
		// the wire); a new invoke.Role must be mapped here explicitly, not
		// silently fall through.
		//exhaustive:enforce
		switch msg.Role {
		case invoke.RoleSystem:
			systemParts = append(systemParts, content)
		case invoke.RoleAssistant:
			req.Messages = append(req.Messages, anthropicMessage{Role: "assistant", Content: content})
		case invoke.RoleUser, invoke.RoleTool:
			req.Messages = append(req.Messages, anthropicMessage{Role: "user", Content: content})
		default:
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
	return &invoke.ChatResponse{
		Content:      strings.Join(parts, ""),
		FinishReason: resp.StopReason,
		Usage: invoke.Usage{
			PromptTokens:     promptTokens,
			CompletionTokens: outputTokens,
			TotalTokens:      promptTokens + outputTokens,
		},
	}
}

func valueOrZero(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}
