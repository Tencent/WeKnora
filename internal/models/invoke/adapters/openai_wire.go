package adapters

// openai_wire.go — behavior port of v1 internal/models/chat/openai_request.go
// (message conversion + request building), provider.go (ShapeRequest),
// thinking.go (thinking strategies), completion_budget.go (budget field
// selection) and prompt_cache.go (cache routing/breakpoints). Every function
// cites its v1 origin; the migration is a port, not a redesign.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/invoke"
	"github.com/sashabaranov/go-openai"
)

// --- Message conversion (v1 openai_request.go ConvertMessages) ---

// convertOpenAIMessages maps the neutral message model onto the OpenAI wire
// format. Content array form is used when any message carries more than one
// part or an image part (v1 MultiContent semantics); a single text part
// degrades to the plain string form (design §6.1). v1's Images-field auto
// conversion (resolveImageURLForLLM + Detail auto) is entry-level image
// preprocessing now — the adapter only formats refs it is given.
func convertOpenAIMessages(messages []invoke.Message) []openai.ChatCompletionMessage {
	out := make([]openai.ChatCompletionMessage, 0, len(messages))
	for _, msg := range messages {
		openaiMsg := openai.ChatCompletionMessage{Role: string(msg.Role)}
		multi := len(msg.Content) > 1
		for _, p := range msg.Content {
			if p.Image != nil {
				multi = true
				break
			}
		}
		if multi {
			openaiMsg.MultiContent = make([]openai.ChatMessagePart, 0, len(msg.Content))
			for _, part := range msg.Content {
				if part.Image != nil {
					openaiMsg.MultiContent = append(openaiMsg.MultiContent, openai.ChatMessagePart{
						Type: openai.ChatMessagePartTypeImageURL,
						ImageURL: &openai.ChatMessageImageURL{
							URL:    part.Image.URL,
							Detail: openai.ImageURLDetail(part.Image.Detail),
						},
					})
					continue
				}
				// v1 ConvertMessages only mapped text/image parts; audio rides
				// the ASR facet, unknown parts were dropped.
				openaiMsg.MultiContent = append(openaiMsg.MultiContent, openai.ChatMessagePart{
					Type: openai.ChatMessagePartTypeText,
					Text: part.Text,
				})
			}
		} else if len(msg.Content) == 1 && msg.Content[0].Text != "" {
			openaiMsg.Content = msg.Content[0].Text
		}

		if len(msg.ToolCalls) > 0 {
			openaiMsg.ToolCalls = make([]openai.ToolCall, 0, len(msg.ToolCalls))
			for _, tc := range msg.ToolCalls {
				openaiMsg.ToolCalls = append(openaiMsg.ToolCalls, openai.ToolCall{
					ID:   tc.ID,
					Type: openai.ToolType(tc.Type),
					Function: openai.FunctionCall{
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					},
				})
			}
		}
		if msg.Role == "tool" {
			openaiMsg.ToolCallID = msg.ToolCallID
			openaiMsg.Name = msg.Name
		}
		// 多轮回传：MiMo / DeepSeek V3.2+ 严格厂商缺失 reasoning_content 会 400
		// （v1 openai_request.go:86-88，DeepSeek/MiMo 多轮 400 生命线）。
		if msg.Role == "assistant" && msg.ReasoningContent != "" {
			openaiMsg.ReasoningContent = msg.ReasoningContent
		}
		out = append(out, openaiMsg)
	}
	return out
}

// --- Request building (v1 openai_request.go BuildChatCompletionRequest) ---

// buildChatCompletionRequest ports v1 BuildChatCompletionRequest: sampling
// params map directly (zero = not sent), the completion budget collapses to
// one wire field (completion_budget.go), then tools / tool_choice / format.
func buildChatCompletionRequest(
	name invoke.ProviderName, model string, opts *invoke.ChatOptions, isStream bool,
) openai.ChatCompletionRequest {
	req := openai.ChatCompletionRequest{
		Model:  model,
		Stream: isStream,
	}
	if opts != nil {
		req.Messages = convertOpenAIMessages(opts.Messages)
	}
	if isStream {
		req.StreamOptions = &openai.StreamOptions{IncludeUsage: true}
	}
	if opts == nil {
		return req
	}
	req.Temperature = float32(opts.Temperature)
	if opts.TopP > 0 {
		req.TopP = float32(opts.TopP)
	}
	if opts.FrequencyPenalty > 0 {
		req.FrequencyPenalty = float32(opts.FrequencyPenalty)
	}
	if opts.PresencePenalty > 0 {
		req.PresencePenalty = float32(opts.PresencePenalty)
	}
	// 完成预算收敛：单一预算字段按厂商选择（v1 applyCompletionBudget +
	// wireCompletionTokenField，#3014 互斥修复）。
	applyCompletionBudget(&req, opts.MaxCompletionTokens, wireCompletionTokenField(name, model))
	if len(opts.Tools) > 0 {
		req.Tools = make([]openai.Tool, 0, len(opts.Tools))
		for _, tool := range opts.Tools {
			openaiTool := openai.Tool{
				Type: openai.ToolTypeFunction,
				Function: &openai.FunctionDefinition{
					Name:        tool.Name,
					Description: tool.Description,
				},
			}
			if tool.Parameters != nil {
				openaiTool.Function.Parameters = tool.Parameters
			}
			req.Tools = append(req.Tools, openaiTool)
		}
	}
	if opts.ParallelToolCalls != nil {
		req.ParallelToolCalls = *opts.ParallelToolCalls
	}
	if opts.ToolChoice != "" {
		switch opts.ToolChoice {
		case "none", "required", "auto":
			req.ToolChoice = opts.ToolChoice
		default:
			req.ToolChoice = openai.ToolChoice{
				Type:     "function",
				Function: openai.ToolFunction{Name: opts.ToolChoice},
			}
		}
	}
	if len(opts.Format) > 0 {
		req.ResponseFormat = &openai.ChatCompletionResponseFormat{
			Type: openai.ChatCompletionResponseFormatTypeJSONObject,
		}
		if len(req.Messages) > 0 {
			last := len(req.Messages) - 1
			req.Messages[last].Content += fmt.Sprintf("\nUse this JSON schema: %s", opts.Format)
		}
	}
	return req
}

// --- Completion budget (v1 completion_budget.go) ---

type completionTokenField string

const (
	completionTokenFieldMaxTokens           completionTokenField = "max_tokens"
	completionTokenFieldMaxCompletionTokens completionTokenField = "max_completion_tokens"
)

// wireCompletionTokenField ports v1 completion_budget.go:43-60: one internal
// budget, exactly one outbound field. Default max_completion_tokens; legacy
// max_tokens for the vendors whose docs use it.
func wireCompletionTokenField(name invoke.ProviderName, model string) completionTokenField {
	if invoke.IsOpenAIReasoningOrGPT5Model(model) {
		return completionTokenFieldMaxCompletionTokens
	}
	switch name {
	case invoke.ProviderDeepSeek, // api-docs.deepseek.com: max_tokens only
		invoke.ProviderZhipu,       // open.bigmodel.cn: max_tokens only
		invoke.ProviderSiliconFlow, // docs.siliconflow.com schema
		invoke.ProviderMoonshot,    // Pi useMaxTokens
		invoke.ProviderNvidia,      // Pi useMaxTokens (NIM / vLLM)
		invoke.ProviderGeneric,     // self-hosted vLLM ignores the new field
		invoke.ProviderGPUStack,    // private vLLM-class runtime
		invoke.ProviderLKEAP:       // cloud.tencent.com max_tokens only
		return completionTokenFieldMaxTokens
	default:
		return completionTokenFieldMaxCompletionTokens
	}
}

func applyCompletionBudget(req *openai.ChatCompletionRequest, budget int, field completionTokenField) {
	if req == nil || budget <= 0 {
		return
	}
	switch field {
	case completionTokenFieldMaxTokens:
		req.MaxTokens = budget
	default:
		req.MaxCompletionTokens = budget
	}
}

// --- ShapeRequest ports (v1 provider.go) ---

// shapeOpenAIReasoning ports openAIReasoningProvider.ShapeRequest +
// applyReasoningEffort (provider.go:244-294, issue #1283, design §4.3):
// strips sampling params (unsupported by o-series / GPT-5), migrates
// max_tokens to max_completion_tokens, passes the thinking level through as
// reasoning_effort (level only meaningful when thinking is explicitly on).
func shapeOpenAIReasoning(req *openai.ChatCompletionRequest, opts *invoke.ChatOptions) {
	req.Temperature = 0
	req.TopP = 0
	req.FrequencyPenalty = 0
	req.PresencePenalty = 0
	if req.MaxCompletionTokens == 0 && req.MaxTokens > 0 {
		req.MaxCompletionTokens = req.MaxTokens
	}
	req.MaxTokens = 0
	if opts == nil || opts.ThinkingLevel == "" || opts.Thinking == nil || !*opts.Thinking {
		return
	}
	req.ReasoningEffort = opts.ThinkingLevel
}

// shapeDeepSeek ports deepseekProvider.ShapeRequest (provider.go:153-157):
// DeepSeek does not support tool_choice.
func shapeDeepSeek(req *openai.ChatCompletionRequest, opts *invoke.ChatOptions) {
	if opts != nil && opts.ToolChoice != "" {
		req.ToolChoice = nil
	}
}

// shapeMoonshotFixedTemp ports moonshotProvider.ShapeRequest (provider.go:257):
// v1 models accept only temperature=1; other sampling params drop.
func shapeMoonshotFixedTemp(req *openai.ChatCompletionRequest, _ *invoke.ChatOptions) {
	req.Temperature = 1
	req.TopP = 0
	req.FrequencyPenalty = 0
	req.PresencePenalty = 0
}

// --- Thinking strategies (v1 thinking.go) ---

// thinkingTypeApply ports thinkingTypeField: { "thinking": { "type": ... } }
// wrapper (LKEAP / Volcengine). Emits nothing when Thinking unset.
func thinkingTypeApply(req *openai.ChatCompletionRequest, opts *invoke.ChatOptions, _ bool) (any, bool) {
	if opts == nil || opts.Thinking == nil {
		return nil, false
	}
	thinkingType := "disabled"
	if *opts.Thinking {
		thinkingType = "enabled"
	}
	return struct {
		openai.ChatCompletionRequest
		Thinking *struct {
			Type string `json:"type"`
		} `json:"thinking,omitempty"`
	}{ChatCompletionRequest: *req, Thinking: &struct {
		Type string `json:"type"`
	}{Type: thinkingType}}, true
}

// enableThinkingApply ports enableThinking{alwaysSend, disableOnNonStream}:
// Qwen thinking models require enable_thinking on every request (default
// false) and Qwen3 rejects thinking in non-stream mode, so the field pins
// false on every non-stream call.
func enableThinkingApply(req *openai.ChatCompletionRequest, opts *invoke.ChatOptions, isStream bool) (any, bool) {
	thinking := false
	if opts != nil && opts.Thinking != nil {
		thinking = *opts.Thinking
	}
	if !isStream {
		thinking = false
	}
	return struct {
		openai.ChatCompletionRequest
		EnableThinking *bool `json:"enable_thinking,omitempty"`
	}{ChatCompletionRequest: *req, EnableThinking: &thinking}, true
}

// chatTemplateKwargsApply ports chatTemplateKwargs: mutates the standard
// request's chat_template_kwargs (vLLM / NVIDIA / generic local deployments)
// and forces the raw path so the field reaches the wire.
func chatTemplateKwargsApply(req *openai.ChatCompletionRequest, opts *invoke.ChatOptions, _ bool) (any, bool) {
	if opts == nil || opts.Thinking == nil {
		return nil, false
	}
	req.ChatTemplateKwargs = map[string]interface{}{
		"enable_thinking": *opts.Thinking,
	}
	return req, true
}

// --- Provider body form (v1 openai_request.go buildProviderOpenAIRequest) ---

// providerRequestMap ports the ForceRawHTTP map roundtrip: marshal →
// map[string]any → remarshal. v1 additionally re-injected per-message tool
// call metadata (gemini thought signatures) during the rebuild; the neutral
// message model has no metadata channel yet, so the rebuild collapses to the
// plain roundtrip. Number formatting survives: integral float64 marshals
// back without a fractional part.
func providerRequestMap(body any) (map[string]any, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal provider request: %w", err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("unmarshal provider request: %w", err)
	}
	return out, nil
}

// --- Prompt cache (v1 prompt_cache.go port) ---

const openAIPromptCacheKeyMaxLength = 64

// clampPromptCacheKey ports prompt_cache.go:162.
func clampPromptCacheKey(key string) string {
	if key == "" {
		return ""
	}
	runes := []rune(key)
	if len(runes) <= openAIPromptCacheKeyMaxLength {
		return key
	}
	return string(runes[:openAIPromptCacheKeyMaxLength])
}

// resolveCacheRetention ports prompt_cache.go:173 (empty = short).
func resolveCacheRetention(opts *invoke.ChatOptions) string {
	if opts != nil && opts.CacheRetention != "" {
		return opts.CacheRetention
	}
	return invoke.CacheRetentionShort
}

// promptCachePolicy ports prompt_cache.go:190 (sendKey / sendCacheControl /
// sendAffinity triple).
type promptCachePolicy struct {
	sendKey          bool
	sendCacheControl bool
	sendAffinity     bool
}

// promptCachePolicyFor ports prompt_cache.go:196-209. NOTE: v1's session-ID
// fallback (ctx SessionIDFromContext) stays caller-side — the adapter contract
// receives the session id via ChatOptions.PromptCacheKey only.
func promptCachePolicyFor(name invoke.ProviderName, baseURL string) promptCachePolicy {
	switch name {
	case invoke.ProviderOpenAI, invoke.ProviderAzureOpenAI, invoke.ProviderOpenRouter:
		return promptCachePolicy{sendKey: true, sendAffinity: true}
	case invoke.ProviderAliyun:
		return promptCachePolicy{sendCacheControl: true}
	}
	if strings.Contains(baseURL, "api.openai.com") {
		return promptCachePolicy{sendKey: true, sendAffinity: true}
	}
	return promptCachePolicy{}
}

type cacheControlMarker struct {
	Type string `json:"type"`
	TTL  string `json:"ttl,omitempty"`
}

// cacheControlFor ports prompt_cache.go:216 (aliyun long TTL = 1h).
func cacheControlFor(retention string, longTTL string) *cacheControlMarker {
	if retention == invoke.CacheRetentionNone {
		return nil
	}
	marker := &cacheControlMarker{Type: "ephemeral"}
	if retention == invoke.CacheRetentionLong && longTTL != "" {
		marker.TTL = longTTL
	}
	return marker
}

// applyPromptCacheToJSONBody ports prompt_cache.go:227-272: inject the cache
// routing key / retention and cache_control breakpoints into the shaped body.
// Rewriting forces the map (alphabetical-key) body form.
func applyPromptCacheToJSONBody(
	body any, policy promptCachePolicy, sessionID string, retention string,
) (any, bool, error) {
	if retention == invoke.CacheRetentionNone {
		return body, false, nil
	}
	if !policy.sendKey && !policy.sendCacheControl {
		return body, false, nil
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, false, err
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, false, err
	}
	rewritten := false
	if policy.sendKey && sessionID != "" {
		payload["prompt_cache_key"] = sessionID
		if retention == invoke.CacheRetentionLong {
			payload["prompt_cache_retention"] = "24h"
		}
		rewritten = true
	}
	if policy.sendCacheControl {
		marker := cacheControlFor(retention, "1h")
		if marker != nil {
			applyCacheControlBreakpoints(payload, marker)
			rewritten = true
		}
	}
	if !rewritten {
		return body, false, nil
	}
	return payload, true, nil
}

// applyCacheControlBreakpoints .. addCacheControlToMessageContent are verbatim
// ports of prompt_cache.go:274-365 (breakpoint placement: first instruction
// message, last tool, last conversation message).
func applyCacheControlBreakpoints(payload map[string]any, marker *cacheControlMarker) {
	if marker == nil {
		return
	}
	applyCacheControlToInstructionMessages(payload["messages"], marker)
	applyCacheControlToLastTool(payload["tools"], marker)
	applyCacheControlToLastConversationMessage(payload["messages"], marker)
}

func applyCacheControlToInstructionMessages(raw any, marker *cacheControlMarker) {
	messages, ok := raw.([]any)
	if !ok {
		return
	}
	for _, item := range messages {
		msg, ok := item.(map[string]any)
		if !ok {
			continue
		}
		role, _ := msg["role"].(string)
		if role == "system" || role == "developer" {
			addCacheControlToMessageContent(msg, marker)
			return
		}
	}
}

func applyCacheControlToLastConversationMessage(raw any, marker *cacheControlMarker) {
	messages, ok := raw.([]any)
	if !ok {
		return
	}
	for i := len(messages) - 1; i >= 0; i-- {
		msg, ok := messages[i].(map[string]any)
		if !ok {
			continue
		}
		role, _ := msg["role"].(string)
		if role == "user" || role == "assistant" || role == "tool" {
			if addCacheControlToMessageContent(msg, marker) {
				return
			}
		}
	}
}

func applyCacheControlToLastTool(raw any, marker *cacheControlMarker) {
	tools, ok := raw.([]any)
	if !ok || len(tools) == 0 {
		return
	}
	last, ok := tools[len(tools)-1].(map[string]any)
	if !ok {
		return
	}
	last["cache_control"] = marker
}

func addCacheControlToMessageContent(msg map[string]any, marker *cacheControlMarker) bool {
	content, ok := msg["content"]
	if !ok || content == nil {
		return false
	}
	if text, ok := content.(string); ok {
		if text == "" {
			return false
		}
		msg["content"] = []any{
			map[string]any{
				"type":          "text",
				"text":          text,
				"cache_control": marker,
			},
		}
		return true
	}
	parts, ok := content.([]any)
	if !ok {
		return false
	}
	for i := len(parts) - 1; i >= 0; i-- {
		part, ok := parts[i].(map[string]any)
		if !ok {
			continue
		}
		if partType, _ := part["type"].(string); partType == "text" || partType == "tool_result" {
			part["cache_control"] = marker
			return true
		}
	}
	return false
}

// attachPromptCacheHeaders ports prompt_cache.go:367-374 (session affinity
// triple for sendKey vendors).
func attachPromptCacheHeaders(header http.Header, policy promptCachePolicy, sessionID string) {
	if !policy.sendAffinity || sessionID == "" {
		return
	}
	header.Set("session_id", sessionID)
	header.Set("x-client-request-id", sessionID)
	header.Set("x-session-affinity", sessionID)
}

// removeThinkingContent ports openai_stream.go:86-104 (<think> strip fallback
// for thinking models that ignore Thinking=false, e.g. Miniax-M2.1).
func removeThinkingContent(content string) string {
	const thinkStartTag = "<think>"
	const thinkEndTag = "</think>"
	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, thinkStartTag) {
		return content
	}
	if lastEndIdx := strings.LastIndex(trimmed, thinkEndTag); lastEndIdx != -1 {
		if result := strings.TrimSpace(trimmed[lastEndIdx+len(thinkEndTag):]); result != "" {
			return result
		}
		return ""
	}
	return ""
}
