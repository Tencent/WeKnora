package openaicompletions

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
)

const defaultPath = "/chat/completions"

// Client talks Chat Completions to one endpoint.
type Client struct {
	cfg Config
}

// New creates a client. The endpoint must already carry auth and headers.
func New(cfg Config) *Client {
	if cfg.ThinkingLevels == nil {
		cfg.ThinkingLevels = api.ThinkingLevelMap{}
	}
	return &Client{cfg: cfg}
}

// GetModelName returns the wire model name.
func (c *Client) GetModelName() string { return c.cfg.Endpoint.Model }

// GetModelID returns WeKnora's model identifier.
func (c *Client) GetModelID() string { return c.cfg.Endpoint.ModelID }

// Settings exposes the resolved settings (diagnostics, tests).
func (c *Client) Settings() Config { return c.cfg }

func (c *Client) url() string { return c.cfg.Endpoint.Resolve(defaultPath) }

func (c *Client) send(
	ctx context.Context, messages []api.Message, opts *api.Options, stream bool,
) (*http.Response, []byte, error) {
	opts = api.WithSessionCacheKey(ctx, opts)
	body, err := c.BuildRequestBody(messages, opts, stream)
	if err != nil {
		return nil, nil, err
	}
	url := c.url()
	req, data, err := c.cfg.Endpoint.NewRequest(ctx, url, body, stream)
	if err != nil {
		return nil, nil, err
	}
	if c.cfg.Settings.PromptCacheKey {
		key := c.cfg.SessionID
		if opts != nil && opts.PromptCacheKey != "" {
			key = opts.PromptCacheKey
		}
		api.AttachSessionAffinityHeaders(req, api.ClampPromptCacheKey(key))
	}
	api.LogRequest(ctx, url, c.cfg.Endpoint.Model, data, stream)
	resp, err := c.cfg.Endpoint.Do(req)
	if err != nil {
		return nil, data, err
	}
	return resp, data, nil
}

// Chat performs a non-streaming completion.
func (c *Client) Chat(ctx context.Context, messages []api.Message, opts *api.Options) (*types.ChatResponse, error) {
	ctx, cancel := api.WithLLMTimeout(ctx, api.DefaultChatTimeout)
	defer cancel()

	resp, _, err := c.send(ctx, messages, opts, false)
	if err != nil {
		if api.IsMultimodalNotSupportedError(err) {
			logger.Warnf(ctx, "[LLM Request] Model %s does not support multimodal, retrying without images",
				c.cfg.Endpoint.Model)
			resp, _, err = c.send(ctx, api.StripImagesFromMessages(messages), opts, false)
		}
		if err != nil {
			return nil, fmt.Errorf("create chat completion: %w", err)
		}
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	result, err := c.parseResponse(raw)
	if err != nil {
		return nil, err
	}
	api.LogUsage(ctx, c.cfg.Endpoint.Model, &result.Usage)
	return result, nil
}

// ChatStream performs a streaming completion.
func (c *Client) ChatStream(
	ctx context.Context, messages []api.Message, opts *api.Options,
) (<-chan types.StreamResponse, error) {
	ctx, cancel := api.WithLLMTimeout(ctx, api.DefaultStreamTimeout)

	resp, data, err := c.send(ctx, messages, opts, true)
	if err != nil {
		if api.IsMultimodalNotSupportedError(err) {
			logger.Warnf(ctx, "[LLM Stream] Model %s does not support multimodal, retrying without images",
				c.cfg.Endpoint.Model)
			resp, data, err = c.send(ctx, api.StripImagesFromMessages(messages), opts, true)
		}
		if err != nil {
			cancel()
			return nil, fmt.Errorf("create chat completion stream: %w", err)
		}
	}

	ch := make(chan types.StreamResponse)
	dumper := api.NewStreamPacketDumper(c.cfg.Endpoint.Model, json.RawMessage(data))
	if dumper != nil {
		logger.Infof(ctx, "[LLM Stream Raw Dump] writing packets to %s", dumper.Path())
	}
	go func() {
		defer cancel()
		defer close(ch)
		defer func() { _ = resp.Body.Close() }()
		if dumper != nil {
			defer dumper.Close()
		}
		c.processStream(ctx, resp.Body, ch, dumper)
	}()
	return ch, nil
}

// --- response decoding ---

type rawToolCall struct {
	Index    *int   `json:"index,omitempty"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"function"`
}

type rawMessage struct {
	Role             string            `json:"role,omitempty"`
	Content          string            `json:"content,omitempty"`
	ReasoningContent string            `json:"reasoning_content,omitempty"`
	Reasoning        string            `json:"reasoning,omitempty"`
	ReasoningText    string            `json:"reasoning_text,omitempty"`
	ReasoningDetails json.RawMessage   `json:"reasoning_details,omitempty"`
	ToolCalls        []json.RawMessage `json:"tool_calls,omitempty"`
}

type rawChoice struct {
	Index        int        `json:"index"`
	Message      rawMessage `json:"message"`
	Delta        rawMessage `json:"delta"`
	FinishReason string     `json:"finish_reason,omitempty"`
}

type rawEnvelope struct {
	Choices []rawChoice      `json:"choices"`
	Usage   *api.OpenAIUsage `json:"usage,omitempty"`
	Error   *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    any    `json:"code"`
	} `json:"error,omitempty"`
}

func (c *Client) reasoningText(m rawMessage) string {
	for _, field := range c.cfg.Settings.ReasoningFields {
		switch field {
		case "reasoning_content":
			if m.ReasoningContent != "" {
				return m.ReasoningContent
			}
		case "reasoning":
			if m.Reasoning != "" {
				return m.Reasoning
			}
		case "reasoning_text":
			if m.ReasoningText != "" {
				return m.ReasoningText
			}
		}
	}
	return ""
}

func (c *Client) decodeToolCalls(raws []json.RawMessage) ([]types.LLMToolCall, []api.ToolCallDelta, error) {
	calls := make([]types.LLMToolCall, 0, len(raws))
	deltas := make([]api.ToolCallDelta, 0, len(raws))
	for i, raw := range raws {
		var tc rawToolCall
		if err := json.Unmarshal(raw, &tc); err != nil {
			// Dropping the entry turns a round that wanted to call a tool into
			// a plain answer, and the agent has no way to notice the action it
			// was told to take went missing. Surface it instead.
			return nil, nil, fmt.Errorf("decode tool call %d: %w", i, err)
		}
		idx := i
		if tc.Index != nil {
			idx = *tc.Index
		}
		call := types.LLMToolCall{
			ID:       tc.ID,
			Type:     orDefault(tc.Type, "function"),
			Function: types.FunctionCall{Name: tc.Function.Name, Arguments: tc.Function.Arguments},
		}
		call.ProviderMetadata = c.extractToolCallMetadata(raw)
		calls = append(calls, call)
		deltas = append(deltas, api.ToolCallDelta{
			Index: idx, ID: tc.ID, Type: tc.Type, Name: tc.Function.Name, Arguments: tc.Function.Arguments,
		})
	}
	return calls, deltas, nil
}

func (c *Client) extractToolCallMetadata(raw json.RawMessage) types.ToolCallMetadata {
	if len(c.cfg.Settings.ToolCallExtraFields) == 0 {
		return nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil
	}
	var md types.ToolCallMetadata
	for _, key := range c.cfg.Settings.ToolCallExtraFields {
		if v, ok := obj[key]; ok && len(v) > 0 && string(v) != "null" {
			if md == nil {
				md = types.ToolCallMetadata{}
			}
			md[key] = v
		}
	}
	return md
}

func (c *Client) parseResponse(raw []byte) (*types.ChatResponse, error) {
	var env rawEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if env.Error != nil && env.Error.Message != "" {
		return nil, fmt.Errorf("API error: %s", env.Error.Message)
	}
	if len(env.Choices) == 0 {
		return nil, fmt.Errorf("no response from API")
	}
	choice := env.Choices[0]
	content, inlineReasoning := splitThinkTags(choice.Message.Content)
	result := &types.ChatResponse{
		Content:          content,
		ReasoningContent: c.reasoningText(choice.Message),
		FinishReason:     choice.FinishReason,
	}
	if result.ReasoningContent == "" {
		result.ReasoningContent = inlineReasoning
	}
	if len(choice.Message.ReasoningDetails) > 0 {
		result.ReasoningMetadata = types.ProviderMetadata{metadataReasoningDetails: choice.Message.ReasoningDetails}
	}
	if len(choice.Message.ToolCalls) > 0 {
		calls, _, err := c.decodeToolCalls(choice.Message.ToolCalls)
		if err != nil {
			return nil, err
		}
		result.ToolCalls = calls
	}
	// Classify the cache status even when the vendor omitted the usage block,
	// so "no counters at all" is still distinguishable from a real miss on the
	// usage dashboards instead of landing as an empty CacheStatus.
	usage := api.OpenAIUsage{}
	if env.Usage != nil {
		usage = *env.Usage
	}
	result.Usage = usage.ToTokenUsage(c.cfg.Settings.PromptCacheAccounting)
	return result, nil
}

// splitThinkTags handles models that inline their chain of thought as a
// leading <think>...</think> block instead of a reasoning field.
func splitThinkTags(content string) (answer, reasoning string) {
	const start, end = "<think>", "</think>"
	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, start) {
		return content, ""
	}
	idx := strings.LastIndex(trimmed, end)
	if idx < 0 {
		// Unterminated: the whole output is (truncated) reasoning.
		return "", strings.TrimSpace(trimmed[len(start):])
	}
	return strings.TrimSpace(trimmed[idx+len(end):]), strings.TrimSpace(trimmed[len(start):idx])
}

func (c *Client) processStream(
	ctx context.Context, body io.Reader, ch chan<- types.StreamResponse, dumper *api.StreamPacketDumper,
) {
	assembler := api.NewStreamAssembler(ctx, c.cfg.Endpoint.Model)
	reader := api.NewSSEReader(body)
	var reasoningDetails []json.RawMessage

	finish := func() {
		if len(reasoningDetails) > 0 {
			merged, _ := json.Marshal(reasoningDetails)
			assembler.ReasoningMetadata = types.ProviderMetadata{metadataReasoningDetails: merged}
		}
		assembler.End(ch)
	}

	for {
		// The consumer went away (client disconnect, aborted agent turn): stop
		// decoding a stream nobody will read instead of spinning to EOF.
		if assembler.Aborted() {
			return
		}
		event, err := reader.ReadEvent()
		if err != nil {
			if err == io.EOF {
				finish()
			} else {
				assembler.Fail(ch, err)
			}
			return
		}
		if event == nil {
			continue
		}
		if event.Done {
			finish()
			return
		}
		if len(event.Data) == 0 {
			continue
		}
		if dumper != nil {
			raw := make([]byte, len(event.Data))
			copy(raw, event.Data)
			dumper.WritePacketRaw(raw)
		}

		var env rawEnvelope
		if err := json.Unmarshal(event.Data, &env); err != nil {
			// Fail rather than skip, as the Anthropic and Gemini loops do. A
			// chunk we cannot decode is a hole in the answer: skipping it ends
			// the stream on EOF, which reaches the caller as a *successful*
			// truncated reply and gets persisted as the model's answer. An
			// error at least surfaces the proxy that corrupted the stream.
			assembler.Fail(ch, fmt.Errorf("decode stream chunk: %w", err))
			return
		}
		if env.Error != nil && env.Error.Message != "" {
			assembler.Fail(ch, fmt.Errorf("API stream error: %s", env.Error.Message))
			return
		}
		if env.Usage != nil {
			assembler.SetUsage(env.Usage.ToTokenUsage(c.cfg.Settings.PromptCacheAccounting))
		}
		if len(env.Choices) == 0 {
			continue
		}
		choice := env.Choices[0]
		delta := api.Delta{
			Reasoning:    c.reasoningText(choice.Delta),
			Content:      choice.Delta.Content,
			FinishReason: choice.FinishReason,
		}
		if len(choice.Delta.ReasoningDetails) > 0 {
			var items []json.RawMessage
			if err := json.Unmarshal(choice.Delta.ReasoningDetails, &items); err == nil {
				reasoningDetails = append(reasoningDetails, items...)
			}
		}
		if len(choice.Delta.ToolCalls) > 0 {
			_, deltas, err := c.decodeToolCalls(choice.Delta.ToolCalls)
			if err != nil {
				assembler.Fail(ch, err)
				return
			}
			delta.ToolCalls = deltas
			for i, raw := range choice.Delta.ToolCalls {
				if md := c.extractToolCallMetadata(raw); len(md) > 0 {
					idx := i
					if i < len(deltas) {
						idx = deltas[i].Index
					}
					assembler.SetToolCallMetadata(idx, md)
				}
			}
		}
		assembler.Process(ch, delta)
	}
}
