// Package anthropicmessages implements the Anthropic Messages wire protocol
// (https://docs.anthropic.com/en/api/messages). MiniMax, Zhipu, Moonshot and
// Volcengine expose the same protocol on an ".../anthropic" base URL, so the
// package contains no vendor names: every deviation is a
// api.AnthropicMessagesSettings field.
package anthropicmessages

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
)

// Config is everything the client needs, already resolved by the api.
type Config struct {
	Endpoint       api.Endpoint
	Settings       api.AnthropicMessagesSettings
	ThinkingLevels api.ThinkingLevelMap
	Reasoning      bool
}

// Metadata keys used on messages and tool calls.
const (
	// MetadataRedactedThinking stores redacted_thinking blocks (JSON array
	// of {"data": ...}) that must be replayed verbatim. Superseded by
	// MetadataThinkingBlocks; still read so assistant turns stored by an
	// earlier build keep replaying.
	MetadataRedactedThinking = "anthropic_redacted_thinking"
	// MetadataThinkingBlocks stores the turn's thinking and
	// redacted_thinking blocks exactly as Claude emitted them, in order.
	//
	// Two properties make this the only correct source for replay. First,
	// Claude signs the exact text of each block, so one assistant turn with
	// interleaved thinking carries several (text, signature) pairs that a
	// single ReasoningContent / ReasoningSignature pair cannot represent —
	// concatenating them produces a signature that matches nothing. Second,
	// ReasoningContent is rewritten downstream (resource-handle encoding,
	// citation compaction, special-token neutralisation) for the benefit of
	// readers and of the next prompt, which invalidates the signature over
	// it. This metadata is opaque to all of that and travels unchanged.
	MetadataThinkingBlocks = "anthropic_thinking_blocks"
)

// thinkingBlock is one stored thinking or redacted_thinking block. It is the
// wire shape, so replay is a copy rather than a reconstruction.
type thinkingBlock struct {
	Type      string `json:"type"`
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`
	Data      string `json:"data,omitempty"`
}

// thinkingBlocksMetadata renders the blocks for storage on the assistant
// message. It returns nil when there is nothing to replay.
func thinkingBlocksMetadata(blocks []thinkingBlock) json.RawMessage {
	if len(blocks) == 0 {
		return nil
	}
	data, err := json.Marshal(blocks)
	if err != nil {
		return nil
	}
	return data
}

// newThinkingBlock records a signed thinking block.
func newThinkingBlock(thinking, signature string) thinkingBlock {
	return thinkingBlock{Type: "thinking", Thinking: thinking, Signature: signature}
}

// newRedactedThinkingBlock records an opaque redacted_thinking block.
func newRedactedThinkingBlock(data string) thinkingBlock {
	return thinkingBlock{Type: "redacted_thinking", Data: data}
}

type cacheControl struct {
	Type string `json:"type"`
	TTL  string `json:"ttl,omitempty"`
}

type contentBlock struct {
	Type         string          `json:"type"`
	Text         string          `json:"text,omitempty"`
	CacheControl *cacheControl   `json:"cache_control,omitempty"`
	ID           string          `json:"id,omitempty"`
	Name         string          `json:"name,omitempty"`
	Input        json.RawMessage `json:"input,omitempty"`
	ToolUseID    string          `json:"tool_use_id,omitempty"`
	Content      any             `json:"content,omitempty"`
	Thinking     string          `json:"thinking,omitempty"`
	Signature    string          `json:"signature,omitempty"`
	Data         string          `json:"data,omitempty"`
	Source       *imageSource    `json:"source,omitempty"`
}

type imageSource struct {
	Type      string `json:"type"` // "base64" | "url"
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
}

type message struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type tool struct {
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	InputSchema  json.RawMessage `json:"input_schema"`
	CacheControl *cacheControl   `json:"cache_control,omitempty"`
}

type toolChoice struct {
	Type                   string `json:"type"`
	Name                   string `json:"name,omitempty"`
	DisableParallelToolUse *bool  `json:"disable_parallel_tool_use,omitempty"`
}

// thinkingPlan is the resolved thinking request for one call.
type thinkingPlan struct {
	enabled bool
	body    map[string]any // the "thinking" object, nil when omitted
	effort  string         // output_config.effort, "" when omitted
	budget  int            // budget_tokens in budget mode
}

func (c *Client) planThinking(opts *api.Options) thinkingPlan {
	s := c.cfg.Settings
	levels := c.cfg.ThinkingLevels
	level, requested := opts.Reasoning()
	if !requested {
		return thinkingPlan{}
	}
	if level.Graded() {
		level = levels.Clamp(level)
	}
	if !level.Enabled() {
		if levels.Supports(api.ReasoningOff) && s.ThinkingMode == api.AnthropicThinkingAdaptive {
			return thinkingPlan{body: map[string]any{"type": "disabled"}}
		}
		// Budget mode: omitting the thinking object disables it.
		return thinkingPlan{}
	}
	plan := thinkingPlan{enabled: true}
	switch s.ThinkingMode {
	case api.AnthropicThinkingAdaptive:
		plan.body = map[string]any{"type": "adaptive"}
		if level.Graded() && s.SupportsEffort {
			plan.effort = levels.Value(level)
		}
	default:
		budget := 0
		if opts != nil && opts.ThinkingBudgetTokens > 0 {
			budget = opts.ThinkingBudgetTokens
		} else {
			key := level
			if !key.Graded() {
				key = api.ReasoningMedium
			}
			budget = s.ThinkingBudgets[key]
		}
		if budget < 1024 {
			budget = 1024
		}
		plan.budget = budget
		plan.body = map[string]any{"type": "enabled", "budget_tokens": budget}
	}
	return plan
}

// BuildRequestBody is the golden-test entry point.
func (c *Client) BuildRequestBody(messages []api.Message, opts *api.Options, stream bool) (map[string]any, error) {
	s := c.cfg.Settings
	body := map[string]any{"model": c.cfg.Endpoint.Model}
	if stream {
		body["stream"] = true
	}
	plan := c.planThinking(opts)

	maxTokens := opts.CompletionBudget()
	if maxTokens <= 0 {
		maxTokens = s.DefaultMaxTokens
		if maxTokens <= 0 {
			maxTokens = 4096
		}
	}
	if plan.budget > 0 && maxTokens <= plan.budget {
		// max_tokens must exceed budget_tokens; keep the caller's answer
		// budget on top of the thinking budget.
		maxTokens = plan.budget + max(maxTokens, 1024)
	}
	body["max_tokens"] = maxTokens

	if opts != nil {
		if s.SupportsTemperature && opts.Temperature > 0 && (!plan.enabled || s.TemperatureWithThinking) {
			body["temperature"] = opts.Temperature
		} else if s.SupportsTopP && opts.TopP > 0 && !plan.enabled {
			// temperature and top_p are mutually exclusive on current models;
			// temperature wins when both are set.
			body["top_p"] = opts.TopP
		}
	}

	retention := api.ResolveCacheRetention(opts)
	var marker *cacheControl
	if s.SupportsCacheControl {
		if m := api.CacheControlFor(retention, s.LongCacheTTL); m != nil {
			marker = &cacheControl{Type: m.Type, TTL: m.TTL}
		}
	}

	if opts != nil && len(opts.Tools) > 0 {
		tools := make([]tool, 0, len(opts.Tools))
		for _, t := range opts.Tools {
			schema := t.Function.Parameters
			if len(schema) == 0 {
				schema = json.RawMessage(`{"type":"object","properties":{}}`)
			}
			tools = append(tools, tool{Name: t.Function.Name, Description: t.Function.Description, InputSchema: schema})
		}
		if marker != nil && s.SupportsCacheControlOnTool {
			tools[len(tools)-1].CacheControl = marker
		}
		body["tools"] = tools
		choice := &toolChoice{Type: "auto"}
		switch opts.ToolChoice {
		case "", "auto":
		case "required":
			choice.Type = "any"
		case "none":
			choice.Type = "none"
		default:
			choice.Type, choice.Name = "tool", opts.ToolChoice
		}
		if opts.ParallelToolCalls != nil && choice.Type != "none" {
			disable := !*opts.ParallelToolCalls
			choice.DisableParallelToolUse = &disable
		}
		body["tool_choice"] = choice
	}

	systemParts, converted := c.convertMessages(messages, opts)
	systemText := strings.Join(systemParts, "\n\n")
	if marker == nil {
		if systemText != "" {
			body["system"] = systemText
		}
	} else {
		if systemText != "" {
			body["system"] = []contentBlock{{Type: "text", Text: systemText, CacheControl: marker}}
		}
		if len(converted) > 0 {
			last := &converted[len(converted)-1]
			switch content := last.Content.(type) {
			case string:
				if content != "" {
					last.Content = []contentBlock{{Type: "text", Text: content, CacheControl: marker}}
				}
			case []contentBlock:
				if len(content) > 0 {
					content[len(content)-1].CacheControl = marker
				}
			}
		}
	}
	if converted == nil {
		// "messages" is required and must be an array: a nil slice would
		// marshal to null and the API answers 400. A request whose only
		// entries were system messages legitimately produces no turns.
		converted = []message{}
	}
	body["messages"] = converted

	if plan.body != nil {
		body["thinking"] = plan.body
	}
	if plan.effort != "" {
		body["output_config"] = map[string]any{"effort": plan.effort}
	}
	for k, v := range s.ExtraBody {
		if _, exists := body[k]; !exists {
			body[k] = v
		}
	}
	return body, nil
}

// betaHeader composes the anthropic-beta header for this request.
func (c *Client) betaHeader(plan thinkingPlan, opts *api.Options, userValue string) string {
	s := c.cfg.Settings
	seen := map[string]bool{}
	var betas []string
	add := func(v string) {
		for _, part := range strings.Split(v, ",") {
			part = strings.TrimSpace(part)
			if part != "" && !seen[part] {
				seen[part] = true
				betas = append(betas, part)
			}
		}
	}
	for _, b := range s.BetaHeaders {
		add(b)
	}
	if plan.enabled && s.ThinkingMode != api.AnthropicThinkingAdaptive && s.InterleavedThinkingBeta != "" &&
		opts != nil && len(opts.Tools) > 0 {
		add(s.InterleavedThinkingBeta)
	}
	add(userValue)
	return strings.Join(betas, ",")
}

// convertMessages maps neutral messages onto Anthropic messages. System
// messages are lifted out; tool calls become tool_use blocks; consecutive
// tool results merge into one user message; thinking blocks are replayed
// only when they carry a signature (the API rejects unsigned ones).
func (c *Client) convertMessages(messages []api.Message, _ *api.Options) ([]string, []message) {
	var system []string
	var result []message
	for _, msg := range messages {
		msg = api.NeutralizeMessageSpecialTokens(msg)
		content := strings.TrimSpace(msg.Content)
		switch msg.Role {
		case "system":
			if content == "" {
				content = textFromMultiContent(msg.MultiContent)
			}
			if content != "" {
				system = append(system, content)
			}
		case "assistant":
			blocks := c.assistantBlocks(msg, content)
			if len(blocks) == 0 {
				continue
			}
			if len(blocks) == 1 && blocks[0].Type == "text" && len(msg.ToolCalls) == 0 {
				result = append(result, message{Role: "assistant", Content: blocks[0].Text})
				continue
			}
			result = append(result, message{Role: "assistant", Content: blocks})
		case "tool":
			block := contentBlock{Type: "tool_result", ToolUseID: msg.ToolCallID, Content: msg.Content}
			if len(result) > 0 && result[len(result)-1].Role == "user" {
				if blocks, ok := result[len(result)-1].Content.([]contentBlock); ok && len(blocks) > 0 &&
					blocks[0].Type == "tool_result" {
					result[len(result)-1].Content = append(blocks, block)
					continue
				}
			}
			result = append(result, message{Role: "user", Content: []contentBlock{block}})
		default:
			blocks := userBlocks(msg, content)
			if len(blocks) == 0 {
				continue
			}
			if len(blocks) == 1 && blocks[0].Type == "text" {
				result = append(result, message{Role: "user", Content: blocks[0].Text})
				continue
			}
			result = append(result, message{Role: "user", Content: blocks})
		}
	}
	return system, result
}

func (c *Client) assistantBlocks(msg api.Message, content string) []contentBlock {
	blocks := replayThinkingBlocks(msg)
	if content == "" {
		content = textFromMultiContent(msg.MultiContent)
	}
	if content != "" {
		blocks = append(blocks, contentBlock{Type: "text", Text: content})
	}
	for _, call := range msg.ToolCalls {
		input := json.RawMessage(call.Function.Arguments)
		if len(input) == 0 || !json.Valid(input) {
			input = json.RawMessage(`{}`)
		}
		blocks = append(blocks, contentBlock{Type: "tool_use", ID: call.ID, Name: call.Function.Name, Input: input})
	}
	return blocks
}

// replayThinkingBlocks rebuilds the leading thinking / redacted_thinking
// blocks of a stored assistant turn.
//
// Claude requires them back verbatim whenever that turn also carries tool_use
// and extended thinking was on: dropping them answers 400 ("Expected
// `thinking` or `redacted_thinking`, but found `tool_use`"), and altering the
// signed text answers 400 as well. Preference order is therefore the verbatim
// metadata first, then the legacy single-block fields for turns stored before
// that metadata existed.
func replayThinkingBlocks(msg api.Message) []contentBlock {
	if raw, ok := msg.ReasoningMetadata[MetadataThinkingBlocks]; ok && len(raw) > 0 {
		var stored []thinkingBlock
		if err := json.Unmarshal(raw, &stored); err == nil {
			blocks := make([]contentBlock, 0, len(stored))
			for _, b := range stored {
				switch b.Type {
				case "thinking":
					// An unsigned block cannot go back: Claude rejects a
					// thinking block without a signature. It means the stream
					// ended inside that block (truncation), so it is the last
					// one and produced no tool_use of its own. Skip just it —
					// discarding the whole turn's blocks instead would strip
					// the signed ones that a completed tool_use in the same
					// turn still needs, which is the 400 this replay exists
					// to prevent.
					if b.Signature == "" {
						continue
					}
					blocks = append(blocks, contentBlock{
						Type: "thinking", Thinking: b.Thinking, Signature: b.Signature,
					})
				case "redacted_thinking":
					blocks = append(blocks, contentBlock{Type: "redacted_thinking", Data: b.Data})
				}
			}
			return blocks
		}
	}

	var blocks []contentBlock
	if raw, ok := msg.ReasoningMetadata[MetadataRedactedThinking]; ok && len(raw) > 0 {
		var redacted []string
		if err := json.Unmarshal(raw, &redacted); err == nil {
			for _, data := range redacted {
				blocks = append(blocks, contentBlock{Type: "redacted_thinking", Data: data})
			}
		}
	}
	sig := api.SignatureFor(api.APIAnthropicMessages, msg.ReasoningSignature)
	if msg.ReasoningContent != "" && sig != "" {
		blocks = append(blocks, contentBlock{
			Type: "thinking", Thinking: msg.ReasoningContent, Signature: sig,
		})
	}
	return blocks
}

func userBlocks(msg api.Message, content string) []contentBlock {
	var blocks []contentBlock
	addImage := func(ref string) {
		if src := imageSourceFor(ref); src != nil {
			blocks = append(blocks, contentBlock{Type: "image", Source: src})
		}
	}
	if len(msg.MultiContent) > 0 {
		for _, part := range msg.MultiContent {
			switch part.Type {
			case "text":
				if t := strings.TrimSpace(part.Text); t != "" {
					blocks = append(blocks, contentBlock{Type: "text", Text: t})
				}
			case "image_url":
				if part.ImageURL != nil {
					addImage(part.ImageURL.URL)
				}
			}
		}
		if content != "" {
			blocks = append(blocks, contentBlock{Type: "text", Text: content})
		}
		return blocks
	}
	for _, img := range msg.Images {
		addImage(img)
	}
	if content != "" {
		blocks = append(blocks, contentBlock{Type: "text", Text: content})
	}
	return blocks
}

// imageSourceFor converts a data URI or URL into an image source block.
func imageSourceFor(ref string) *imageSource {
	ref = api.ResolveImageURLForLLM(ref)
	if strings.HasPrefix(ref, "data:") {
		semi := strings.Index(ref, ";base64,")
		if semi < 0 {
			return nil
		}
		mediaType := strings.TrimPrefix(ref[:semi], "data:")
		data := ref[semi+len(";base64,"):]
		if _, err := base64.StdEncoding.DecodeString(data); err != nil {
			return nil
		}
		return &imageSource{Type: "base64", MediaType: mediaType, Data: data}
	}
	if u, err := url.Parse(ref); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
		return &imageSource{Type: "url", URL: ref}
	}
	return nil
}

func textFromMultiContent(parts []api.MessageContentPart) string {
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

// mapStopReason converts Anthropic stop reasons to the shared vocabulary.
func mapStopReason(reason string, toolCalls int, incomplete bool) string {
	switch {
	case incomplete, reason == "":
		return types.FinishReasonIncomplete
	case reason == "max_tokens":
		return "length"
	case reason == "tool_use" || (toolCalls > 0 && reason == "end_turn"):
		return "tool_calls"
	case reason == "refusal":
		return "content_filter"
	}
	return reason
}
