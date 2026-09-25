// Package openaicompletions implements the OpenAI Chat Completions wire
// protocol. Every vendor-specific deviation is driven by
// api.OpenAICompletionsSettings; this package contains no vendor names.
package openaicompletions

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/api"
)

// Config is everything the client needs, already resolved by the api.
type Config struct {
	Endpoint api.Endpoint
	Settings api.OpenAICompletionsSettings
	// ThinkingLevels maps neutral levels to the vendor vocabulary.
	ThinkingLevels api.ThinkingLevelMap
	// Reasoning marks a reasoning model (developer role, no sampling params
	// are decided by Settings, this only gates role selection).
	Reasoning bool
	// SessionID feeds prompt_cache_key when the caller sets none.
	SessionID string
}

type wireToolCall struct {
	ID       string           `json:"id,omitempty"`
	Type     string           `json:"type"`
	Function wireFunctionCall `json:"function"`
	extra    map[string]json.RawMessage
}

type wireFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// MarshalJSON appends opaque vendor fields (Gemini extra_content) next to
// the standard members.
func (t wireToolCall) MarshalJSON() ([]byte, error) {
	type plain wireToolCall
	data, err := json.Marshal(plain(t))
	if err != nil {
		return nil, err
	}
	if len(t.extra) == 0 {
		return data, nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil, err
	}
	for k, v := range t.extra {
		obj[k] = v
	}
	return json.Marshal(obj)
}

type wireMessage struct {
	Role             string          `json:"role"`
	Content          any             `json:"content,omitempty"`
	Name             string          `json:"name,omitempty"`
	ToolCallID       string          `json:"tool_call_id,omitempty"`
	ToolCalls        []wireToolCall  `json:"tool_calls,omitempty"`
	ReasoningContent *string         `json:"reasoning_content,omitempty"`
	ReasoningDetails json.RawMessage `json:"reasoning_details,omitempty"`
}

type wirePart struct {
	Type     string        `json:"type"`
	Text     string        `json:"text,omitempty"`
	ImageURL *wireImageURL `json:"image_url,omitempty"`
}

type wireImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

// convertMessages maps neutral messages onto Chat Completions messages.
func (c *Client) convertMessages(messages []api.Message) []wireMessage {
	s := c.cfg.Settings
	instructionRole := "system"
	if c.cfg.Reasoning && s.SupportsDeveloperRole {
		instructionRole = "developer"
	}
	out := make([]wireMessage, 0, len(messages))
	for _, msg := range messages {
		msg = api.NeutralizeMessageSpecialTokens(msg)
		wm := wireMessage{Role: msg.Role}
		if msg.Role == "system" {
			wm.Role = instructionRole
		}

		switch {
		case len(msg.MultiContent) > 0:
			parts := make([]wirePart, 0, len(msg.MultiContent))
			for _, part := range msg.MultiContent {
				switch part.Type {
				case "text":
					parts = append(parts, wirePart{Type: "text", Text: part.Text})
				case "image_url":
					if part.ImageURL != nil {
						parts = append(parts, wirePart{Type: "image_url", ImageURL: &wireImageURL{
							URL: api.ResolveImageURLForLLM(part.ImageURL.URL), Detail: part.ImageURL.Detail,
						}})
					}
				}
			}
			wm.Content = c.contentParts(parts)
		case len(msg.Images) > 0 && msg.Role == "user":
			parts := make([]wirePart, 0, len(msg.Images)+1)
			for _, img := range msg.Images {
				parts = append(parts, wirePart{Type: "image_url", ImageURL: &wireImageURL{
					URL: api.ResolveImageURLForLLM(img), Detail: "auto",
				}})
			}
			parts = append(parts, wirePart{Type: "text", Text: msg.Content})
			wm.Content = c.contentParts(parts)
		default:
			if msg.Content != "" || len(msg.ToolCalls) == 0 {
				wm.Content = msg.Content
			}
		}

		if len(msg.ToolCalls) > 0 {
			wm.ToolCalls = make([]wireToolCall, 0, len(msg.ToolCalls))
			for _, tc := range msg.ToolCalls {
				wtc := wireToolCall{
					ID:       tc.ID,
					Type:     orDefault(tc.Type, "function"),
					Function: wireFunctionCall{Name: tc.Function.Name, Arguments: tc.Function.Arguments},
				}
				for _, key := range s.ToolCallExtraFields {
					if raw, ok := tc.ProviderMetadata[key]; ok && len(raw) > 0 {
						if wtc.extra == nil {
							wtc.extra = map[string]json.RawMessage{}
						}
						wtc.extra[key] = raw
					}
				}
				wm.ToolCalls = append(wm.ToolCalls, wtc)
			}
		}

		if msg.Role == "tool" {
			wm.ToolCallID = msg.ToolCallID
			wm.Name = msg.Name
		}

		if msg.Role == "assistant" {
			if s.ReplayReasoningContent && msg.ReasoningContent != "" {
				rc := msg.ReasoningContent
				wm.ReasoningContent = &rc
			}
			// reasoning_details is OpenRouter's format; other vendors reject it.
			raw, ok := msg.ReasoningMetadata[metadataReasoningDetails]
			if ok && len(raw) > 0 && s.ThinkingFormat == api.ThinkingFormatOpenRouter {
				wm.ReasoningDetails = raw
			}
		}
		out = append(out, wm)
	}
	return out
}

// contentParts flattens rich content to plain text for vendors that only
// accept strings (WeKnora Cloud), otherwise returns the parts as-is.
func (c *Client) contentParts(parts []wirePart) any {
	if c.cfg.Settings.SupportsMultiContent {
		return parts
	}
	var texts []string
	for _, p := range parts {
		if p.Type == "text" && p.Text != "" {
			texts = append(texts, p.Text)
		}
	}
	return strings.Join(texts, "\n")
}

const metadataReasoningDetails = "reasoning_details"

// buildBody assembles the request body. It returns a map so vendor-specific
// top-level fields can be added without a struct per vendor; key order in
// the encoded JSON is alphabetical, which keeps golden tests stable.
func (c *Client) buildBody(messages []api.Message, opts *api.Options, stream bool) (map[string]any, error) {
	s := c.cfg.Settings
	body := map[string]any{
		"model":    c.cfg.Endpoint.Model,
		"messages": c.convertMessages(messages),
	}
	if stream {
		body["stream"] = true
		if s.SupportsUsageInStreaming {
			body["stream_options"] = map[string]any{"include_usage": true}
		}
	}
	if s.SupportsStore {
		body["store"] = false
	}

	if opts != nil {
		c.applySampling(body, opts)
		if budget := opts.CompletionBudget(); budget > 0 {
			field := s.MaxTokensField
			if field == "" {
				field = "max_completion_tokens"
			}
			body[field] = budget
		}
		c.applyTools(body, opts)
		if len(opts.Format) > 0 && s.SupportsResponseFormat {
			body["response_format"] = map[string]any{"type": "json_object"}
			msgs := body["messages"].([]wireMessage)
			if len(msgs) > 0 {
				last := &msgs[len(msgs)-1]
				if text, ok := last.Content.(string); ok {
					last.Content = text + fmt.Sprintf("\nUse this JSON schema: %s", opts.Format)
				}
			}
		}
		if s.PromptCacheKey && api.ResolveCacheRetention(opts) != api.CacheRetentionNone {
			key := opts.PromptCacheKey
			if key == "" {
				key = c.cfg.SessionID
			}
			if key = api.ClampPromptCacheKey(key); key != "" {
				body["prompt_cache_key"] = key
				if api.ResolveCacheRetention(opts) == api.CacheRetentionLong {
					body["prompt_cache_retention"] = "24h"
				}
			}
		}
	}
	c.applyThinking(body, opts, stream)

	for k, v := range s.ExtraBody {
		if _, exists := body[k]; !exists {
			body[k] = v
		}
	}
	// These names are mutually exclusive, including when extra_body supplies
	// one or both. Keep the protocol's configured field; an explicit caller
	// budget already takes precedence over extra_body above.
	if _, legacy := body["max_tokens"]; legacy {
		if _, completion := body["max_completion_tokens"]; completion {
			if s.MaxTokensField == "max_tokens" {
				delete(body, "max_completion_tokens")
			} else {
				delete(body, "max_tokens")
			}
		}
	}
	return body, nil
}

func (c *Client) applySampling(body map[string]any, opts *api.Options) {
	s := c.cfg.Settings
	if !s.SupportsTemperature {
		return
	}
	if s.FixedTemperature != nil {
		body["temperature"] = *s.FixedTemperature
		return
	}
	if opts.Temperature > 0 {
		body["temperature"] = opts.Temperature
	}
	if opts.TopP > 0 {
		body["top_p"] = opts.TopP
	}
	if opts.FrequencyPenalty > 0 {
		body["frequency_penalty"] = opts.FrequencyPenalty
	}
	if opts.PresencePenalty > 0 {
		body["presence_penalty"] = opts.PresencePenalty
	}
	if opts.Seed > 0 && s.SupportsSeed {
		body["seed"] = opts.Seed
	}
}

func (c *Client) applyTools(body map[string]any, opts *api.Options) {
	s := c.cfg.Settings
	if len(opts.Tools) == 0 {
		return
	}
	tools := make([]map[string]any, 0, len(opts.Tools))
	for _, tool := range opts.Tools {
		fn := map[string]any{
			"name":        tool.Function.Name,
			"description": tool.Function.Description,
		}
		if len(tool.Function.Parameters) > 0 {
			fn["parameters"] = tool.Function.Parameters
		}
		tools = append(tools, map[string]any{"type": orDefault(tool.Type, "function"), "function": fn})
	}
	body["tools"] = tools
	if opts.ParallelToolCalls != nil && s.SupportsParallelToolCalls {
		body["parallel_tool_calls"] = *opts.ParallelToolCalls
	}
	if opts.ToolChoice == "" {
		return
	}
	switch opts.ToolChoice {
	case "none", "required", "auto":
		if s.AllowsToolChoice(opts.ToolChoice) {
			body["tool_choice"] = opts.ToolChoice
		}
	default:
		if s.AllowsToolChoice("function") {
			body["tool_choice"] = map[string]any{
				"type":     "function",
				"function": map[string]any{"name": opts.ToolChoice},
			}
		}
	}
}

// applyThinking encodes the requested reasoning level in the vendor's
// dialect. See api.ThinkingFormat for the dialects.
func (c *Client) applyThinking(body map[string]any, opts *api.Options, stream bool) {
	s := c.cfg.Settings
	if s.ThinkingFormat == api.ThinkingFormatNone || s.ThinkingFormat == "" {
		return
	}
	levels := c.cfg.ThinkingLevels
	level, requested := opts.Reasoning()
	if !requested {
		if !s.ThinkingAlwaysSend {
			return
		}
		level = api.ReasoningOff
	}
	if level.Graded() {
		level = levels.Clamp(level)
	}
	enabled := level.Enabled()
	if enabled && s.ThinkingDisableOnNonStream && !stream {
		enabled = false
	}
	if !enabled && !levels.Supports(api.ReasoningOff) {
		// Always-on reasoning model: there is nothing to switch off and the
		// vendor rejects the attempt.
		return
	}

	effort := ""
	if enabled && level.Graded() && s.SupportsReasoningEffort {
		effort = levels.Value(level)
	}
	effortField := orDefault(s.ReasoningEffortField, "reasoning_effort")
	budget := 0
	if enabled && opts != nil && s.ThinkingBudgetField != "" {
		budget = opts.ThinkingBudgetTokens
	}
	if effort != "" && s.ThinkingBudgetExcludesEffort {
		// The two fields are mutually exclusive on this vendor and sending
		// both is an error; the graded level wins because it is what the
		// caller picked.
		budget = 0
	}
	if budget > 0 {
		body[s.ThinkingBudgetField] = budget
	}

	switch s.ThinkingFormat {
	case api.ThinkingFormatOpenAI:
		if enabled {
			if effort != "" {
				body[effortField] = effort
			}
		} else if v, ok := levels[api.ReasoningOff]; ok && v != nil && *v != "" {
			body[effortField] = *v
		}
	case api.ThinkingFormatThinkingType:
		if enabled {
			body["thinking"] = map[string]any{"type": orDefault(s.ThinkingEnabledValue, "enabled")}
			if effort != "" {
				body[effortField] = effort
			}
		} else {
			body["thinking"] = map[string]any{"type": "disabled"}
		}
	case api.ThinkingFormatEnableThinking:
		body["enable_thinking"] = enabled
		if effort != "" {
			body[effortField] = effort
		}
	case api.ThinkingFormatChatTemplateKwargs:
		body["chat_template_kwargs"] = map[string]any{"enable_thinking": enabled}
	case api.ThinkingFormatOpenRouter:
		if enabled {
			reasoning := map[string]any{}
			if effort != "" {
				reasoning["effort"] = effort
			} else {
				reasoning["enabled"] = true
			}
			body["reasoning"] = reasoning
		} else {
			body["reasoning"] = map[string]any{"enabled": false}
		}
	}
}

// finalizeBody applies transformations that need the JSON object form
// (cache_control breakpoints) and returns the bytes to send.
func (c *Client) finalizeBody(body map[string]any, opts *api.Options) (map[string]any, error) {
	s := c.cfg.Settings
	if s.CacheControlFormat != "anthropic" {
		return body, nil
	}
	marker := api.CacheControlFor(api.ResolveCacheRetention(opts), "1h")
	if marker == nil {
		return body, nil
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	api.ApplyCacheControlBreakpoints(payload, marker)
	return payload, nil
}

// BuildRequestBody is the golden-test entry point: it returns the exact
// JSON object that would be sent for the given inputs.
func (c *Client) BuildRequestBody(messages []api.Message, opts *api.Options, stream bool) (map[string]any, error) {
	body, err := c.buildBody(messages, opts, stream)
	if err != nil {
		return nil, err
	}
	return c.finalizeBody(body, opts)
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
