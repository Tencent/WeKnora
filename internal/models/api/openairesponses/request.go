// Package openairesponses implements the OpenAI Responses wire protocol
// (POST /responses). Every vendor-specific deviation is driven by
// catalog.OpenAIResponsesSettings; this package contains no vendor names.
package openairesponses

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/catalog"
)

// Config is everything the client needs, already resolved by the catalog.
type Config struct {
	Endpoint api.Endpoint
	Settings catalog.OpenAIResponsesSettings
	// ThinkingLevels maps neutral levels to the vendor vocabulary.
	ThinkingLevels api.ThinkingLevelMap
	// Reasoning marks a reasoning model: sampling parameters are dropped and
	// encrypted reasoning items are requested where the vendor supports them.
	Reasoning bool
	// SessionID feeds prompt_cache_key when the caller sets none.
	SessionID string
}

const (
	// metadataReasoningItems is the ReasoningMetadata key carrying the raw
	// reasoning output items (JSON array) so they can be replayed verbatim on
	// the next turn.
	metadataReasoningItems = "openai_responses_reasoning"
	// metadataToolCallItemID is the ToolCallMetadata key carrying the
	// function_call item id (raw JSON string) so replayed calls keep it.
	metadataToolCallItemID = "openai_responses_item_id"

	minMaxOutputTokens = 16
)

// --- input items ---

type inputMessage struct {
	Type    string `json:"type"`
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type inputPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

type functionCallItem struct {
	Type      string          `json:"type"`
	ID        json.RawMessage `json:"id,omitempty"`
	CallID    string          `json:"call_id"`
	Name      string          `json:"name"`
	Arguments string          `json:"arguments"`
}

type functionCallOutputItem struct {
	Type   string `json:"type"`
	CallID string `json:"call_id"`
	Output string `json:"output"`
}

// convertMessages splits neutral messages into the instructions string and
// the ordered input item list.
func (c *Client) convertMessages(messages []api.Message) (string, []any) {
	var instructions []string
	items := make([]any, 0, len(messages))
	for _, msg := range messages {
		msg = api.NeutralizeMessageSpecialTokens(msg)
		switch msg.Role {
		case "system":
			if msg.Content != "" {
				instructions = append(instructions, msg.Content)
			}
		case "assistant":
			items = append(items, c.assistantItems(msg)...)
		case "tool":
			items = append(items, functionCallOutputItem{
				Type:   "function_call_output",
				CallID: msg.ToolCallID,
				Output: msg.Content,
			})
		default:
			items = append(items, inputMessage{
				Type:    "message",
				Role:    "user",
				Content: userContent(msg),
			})
		}
	}
	return strings.Join(instructions, "\n\n"), items
}

// userContent renders a user message either as a plain string or as a list
// of input_text / input_image parts.
func userContent(msg api.Message) any {
	switch {
	case len(msg.MultiContent) > 0:
		parts := make([]inputPart, 0, len(msg.MultiContent))
		for _, part := range msg.MultiContent {
			switch part.Type {
			case "text":
				parts = append(parts, inputPart{Type: "input_text", Text: part.Text})
			case "image_url":
				if part.ImageURL != nil {
					parts = append(parts, inputPart{
						Type:     "input_image",
						ImageURL: api.ResolveImageURLForLLM(part.ImageURL.URL),
						Detail:   orDefault(part.ImageURL.Detail, "auto"),
					})
				}
			}
		}
		return parts
	case len(msg.Images) > 0:
		parts := make([]inputPart, 0, len(msg.Images)+1)
		for _, img := range msg.Images {
			parts = append(parts, inputPart{
				Type:     "input_image",
				ImageURL: api.ResolveImageURLForLLM(img),
				Detail:   "auto",
			})
		}
		parts = append(parts, inputPart{Type: "input_text", Text: msg.Content})
		return parts
	default:
		return msg.Content
	}
}

// assistantItems replays a prior assistant turn: its reasoning items (only
// when the provider handed them back earlier), its text and its function
// calls, in that order.
func (c *Client) assistantItems(msg api.Message) []any {
	var items []any
	if raw, ok := msg.ReasoningMetadata[metadataReasoningItems]; ok && len(raw) > 0 {
		var reasoning []json.RawMessage
		if err := json.Unmarshal(raw, &reasoning); err == nil {
			for _, item := range reasoning {
				if len(item) > 0 && string(item) != "null" {
					items = append(items, item)
				}
			}
		}
	}
	if msg.Content != "" || len(msg.ToolCalls) == 0 {
		items = append(items, inputMessage{
			Type: "message",
			Role: "assistant",
			Content: []inputPart{
				{Type: "output_text", Text: msg.Content},
			},
		})
	}
	for _, tc := range msg.ToolCalls {
		item := functionCallItem{
			Type:      "function_call",
			CallID:    tc.ID,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		}
		if raw, ok := tc.ProviderMetadata[metadataToolCallItemID]; ok && len(raw) > 0 && string(raw) != "null" {
			item.ID = raw
		}
		items = append(items, item)
	}
	return items
}

// appendToLastUserText appends suffix to the text of the last user message
// in the input list. It reports whether a target was found.
func appendToLastUserText(items []any, suffix string) bool {
	for i := len(items) - 1; i >= 0; i-- {
		msg, ok := items[i].(inputMessage)
		if !ok || msg.Role != "user" {
			continue
		}
		switch content := msg.Content.(type) {
		case string:
			msg.Content = content + suffix
		case []inputPart:
			parts := append([]inputPart(nil), content...)
			appended := false
			for j := len(parts) - 1; j >= 0; j-- {
				if parts[j].Type == "input_text" {
					parts[j].Text += suffix
					appended = true
					break
				}
			}
			if !appended {
				parts = append(parts, inputPart{Type: "input_text", Text: strings.TrimPrefix(suffix, "\n")})
			}
			msg.Content = parts
		default:
			return false
		}
		items[i] = msg
		return true
	}
	return false
}

// buildBody assembles the request body. It returns a map so vendor-specific
// top-level fields can be added without a struct per vendor; key order in
// the encoded JSON is alphabetical, which keeps golden tests stable.
func (c *Client) buildBody(messages []api.Message, opts *api.Options, stream bool) (map[string]any, error) {
	s := c.cfg.Settings
	instructions, input := c.convertMessages(messages)
	body := map[string]any{
		"model": c.cfg.Endpoint.Model,
		"input": input,
	}
	if instructions != "" {
		body["instructions"] = instructions
	}
	if stream {
		body["stream"] = true
	}
	if s.SupportsStore {
		body["store"] = false
	}

	if opts != nil {
		c.applySampling(body, opts)
		if budget := opts.CompletionBudget(); budget > 0 && s.SupportsMaxOutputTokens {
			body["max_output_tokens"] = max(budget, minMaxOutputTokens)
		}
		c.applyTools(body, opts)
		if len(opts.Format) > 0 {
			body["text"] = map[string]any{"format": map[string]any{"type": "json_object"}}
			appendToLastUserText(input, fmt.Sprintf("\nUse this JSON schema: %s", opts.Format))
		}
		if s.PromptCacheKey && api.ResolveCacheRetention(opts) != api.CacheRetentionNone {
			key := opts.PromptCacheKey
			if key == "" {
				key = c.cfg.SessionID
			}
			if key = api.ClampPromptCacheKey(key); key != "" {
				body["prompt_cache_key"] = key
				if api.ResolveCacheRetention(opts) == api.CacheRetentionLong && s.SupportsLongCacheRetention {
					body["prompt_cache_retention"] = "24h"
				}
			}
		}
	}
	c.applyReasoning(body, opts)

	for k, v := range s.ExtraBody {
		if _, exists := body[k]; !exists {
			body[k] = v
		}
	}
	return body, nil
}

// applySampling sends temperature / top_p. Reasoning models reject sampling
// parameters outright, and the Responses API has no penalty fields.
func (c *Client) applySampling(body map[string]any, opts *api.Options) {
	if !c.cfg.Settings.SupportsTemperature || c.cfg.Reasoning {
		return
	}
	if opts.Temperature > 0 {
		body["temperature"] = opts.Temperature
	}
	if opts.TopP > 0 {
		body["top_p"] = opts.TopP
	}
}

// applyTools emits the flat Responses tool shape (no nested "function").
func (c *Client) applyTools(body map[string]any, opts *api.Options) {
	if len(opts.Tools) == 0 {
		return
	}
	tools := make([]map[string]any, 0, len(opts.Tools))
	for _, tool := range opts.Tools {
		entry := map[string]any{
			"type":        orDefault(tool.Type, "function"),
			"name":        tool.Function.Name,
			"description": tool.Function.Description,
		}
		if len(tool.Function.Parameters) > 0 {
			entry["parameters"] = tool.Function.Parameters
		}
		tools = append(tools, entry)
	}
	body["tools"] = tools
	if opts.ParallelToolCalls != nil && c.cfg.Settings.SupportsParallelToolCalls {
		body["parallel_tool_calls"] = *opts.ParallelToolCalls
	}
	switch opts.ToolChoice {
	case "":
	case "none", "required", "auto":
		body["tool_choice"] = opts.ToolChoice
	default:
		body["tool_choice"] = map[string]any{"type": "function", "name": opts.ToolChoice}
	}
}

// applyReasoning encodes the requested thinking level as the Responses
// "reasoning" object and asks for encrypted reasoning items on reasoning
// models so multi-turn tool loops can replay them statelessly.
func (c *Client) applyReasoning(body map[string]any, opts *api.Options) {
	s := c.cfg.Settings
	levels := c.cfg.ThinkingLevels
	level, requested := opts.Reasoning()
	if requested {
		if level.Enabled() {
			reasoning := map[string]any{}
			if level.Graded() {
				level = levels.Clamp(level)
			}
			if level.Graded() {
				reasoning["effort"] = levels.Value(level)
			}
			if s.SupportsReasoningSummary {
				reasoning["summary"] = "auto"
			}
			if len(reasoning) > 0 {
				body["reasoning"] = reasoning
			}
		} else if v, ok := levels[api.ReasoningOff]; ok && v != nil && *v != "" {
			body["reasoning"] = map[string]any{"effort": *v}
		}
	}
	if c.cfg.Reasoning && s.SupportsEncryptedReasoning {
		body["include"] = []string{"reasoning.encrypted_content"}
	}
}

// BuildRequestBody is the golden-test entry point: it returns the exact
// JSON object that would be sent for the given inputs.
func (c *Client) BuildRequestBody(messages []api.Message, opts *api.Options, stream bool) (map[string]any, error) {
	return c.buildBody(messages, opts, stream)
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
